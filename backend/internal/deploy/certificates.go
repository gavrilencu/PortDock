package deploy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/proxy"
	"github.com/portdock/portdock/internal/store"
)

// Certificates are issued and renewed by Traefik (ACME). PortDock watches them:
// it records the expiry of every domain, notifies before expiry and — when
// Traefik failed to renew in time — forces a new issuance.

// IssuedCert is a certificate found in a proxy's acme.json or uploaded by an admin.
type IssuedCert struct {
	Resolver  string    `json:"resolver"` // letsencrypt | letsencrypt-dns | custom
	Main      string    `json:"main"`
	SANs      []string  `json:"sans"`
	Issuer    string    `json:"issuer"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
	ServerID  int64     `json:"server_id"`
	CustomID  int64     `json:"custom_id,omitempty"`
}

// Covers reports whether the certificate is valid for host (wildcards match one label).
func (c IssuedCert) Covers(host string) bool {
	for _, n := range append([]string{c.Main}, c.SANs...) {
		if hostMatches(n, host) {
			return true
		}
	}
	return false
}

func hostMatches(pattern, host string) bool {
	pattern, host = strings.ToLower(pattern), strings.ToLower(host)
	if pattern == host {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".example.com"
		if strings.HasPrefix(host, "*.") {
			return host == pattern
		}
		return strings.HasSuffix(host, suffix) && !strings.Contains(strings.TrimSuffix(host, suffix), ".")
	}
	return false
}

// RenewThreshold returns the number of days before expiry that triggers action.
func (e *Engine) RenewThreshold(ctx context.Context) int {
	if n, err := strconv.Atoi(e.db.Setting(ctx, "cert_renew_days")); err == nil && n >= 3 && n <= 60 {
		return n
	}
	return 14
}

func (e *Engine) acmePath() string { return path.Join(e.cfg.DataDir, "proxy", "acme.json") }

type acmeEntry struct {
	Domain struct {
		Main string   `json:"main"`
		SANs []string `json:"sans"`
	} `json:"domain"`
	Certificate string `json:"certificate"`
}

// ParseACME reads the certificates from a Traefik acme.json document.
func ParseACME(raw []byte) ([]IssuedCert, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	var doc map[string]struct {
		Certificates []acmeEntry `json:"Certificates"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("acme.json: %w", err)
	}
	var out []IssuedCert
	for resolver, r := range doc {
		for _, c := range r.Certificates {
			ic := IssuedCert{Resolver: resolver, Main: c.Domain.Main, SANs: c.Domain.SANs}
			if pemBytes, err := base64.StdEncoding.DecodeString(c.Certificate); err == nil {
				if x, err := firstCert(pemBytes); err == nil {
					ic.NotBefore, ic.NotAfter, ic.Issuer = x.NotBefore, x.NotAfter, issuerName(x)
				}
			}
			out = append(out, ic)
		}
	}
	return out, nil
}

func firstCert(pemBytes []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	return x509.ParseCertificate(block.Bytes)
}

func issuerName(c *x509.Certificate) string {
	if c.Issuer.Organization != nil && len(c.Issuer.Organization) > 0 {
		name := c.Issuer.Organization[0]
		if c.Issuer.CommonName != "" {
			name += " (" + c.Issuer.CommonName + ")"
		}
		return name
	}
	return c.Issuer.CommonName
}

// ServerCertificates lists the ACME certificates stored by a server's proxy.
func (e *Engine) ServerCertificates(ctx context.Context, x executor.Executor, serverID int64) ([]IssuedCert, error) {
	out, err := executor.Output(ctx, x, "cat "+q(e.acmePath())+" 2>/dev/null || true")
	if err != nil {
		return nil, err
	}
	certs, err := ParseACME([]byte(out))
	for i := range certs {
		certs[i].ServerID = serverID
	}
	return certs, err
}

// CustomCertificates returns uploaded certificates in IssuedCert form.
func (e *Engine) CustomCertificates(ctx context.Context) []IssuedCert {
	rows, _ := store.Select[store.CustomCertificate](ctx, e.db, "SELECT * FROM custom_certificates")
	out := make([]IssuedCert, 0, len(rows))
	for _, r := range rows {
		nb, _ := time.Parse(time.RFC3339, r.NotBefore)
		na, _ := time.Parse(time.RFC3339, r.NotAfter)
		sans := store.SplitList(r.Domains)
		main := ""
		if len(sans) > 0 {
			main = sans[0]
		}
		out = append(out, IssuedCert{Resolver: "custom", Main: main, SANs: sans, Issuer: r.Issuer, NotBefore: nb, NotAfter: na, CustomID: r.ID})
	}
	return out
}

// SyncCustomCertificates writes uploaded certificates to every proxy (idempotent).
func (e *Engine) SyncCustomCertificates(ctx context.Context) error {
	rows, err := store.Select[store.CustomCertificate](ctx, e.db, "SELECT * FROM custom_certificates ORDER BY id")
	if err != nil {
		return err
	}
	servers, err := store.Select[store.Server](ctx, e.db, "SELECT * FROM servers WHERE proxy_enabled = 1 AND status = 'reachable'")
	if err != nil {
		return err
	}
	dir := path.Join(e.cfg.DataDir, "proxy", "custom")
	var errs []error
	for _, srv := range servers {
		x, err := e.pool.For(ctx, srv)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		var files []proxy.CustomCertFile
		keep := []string{}
		for _, c := range rows {
			base := fmt.Sprintf("cert-%d", c.ID)
			if err := executor.WriteFile(ctx, x, path.Join(dir, base+".crt"), []byte(c.CertPEM), "0644"); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", srv.Name, err))
				continue
			}
			if err := executor.WriteFile(ctx, x, path.Join(dir, base+".key"), []byte(e.box.MustDecrypt(c.KeyPEM)), "0600"); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", srv.Name, err))
				continue
			}
			files = append(files, proxy.CustomCertFile{CertFile: "/certs/custom/" + base + ".crt", KeyFile: "/certs/custom/" + base + ".key"})
			keep = append(keep, base+".crt", base+".key")
		}
		// Remove files of deleted certificates.
		cleanup := fmt.Sprintf("cd %s 2>/dev/null && for f in cert-*; do case \" %s \" in *\" $f \"*) ;; *) rm -f \"$f\";; esac; done; true", q(dir), strings.Join(keep, " "))
		_ = x.Run(ctx, cleanup, executor.RunOpts{})
		if err := writeRoutes(ctx, x, path.Join(e.dynamicDir(), "tls-custom.yml"), proxy.RenderCustomCerts(files)); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", srv.Name, err))
		}
	}
	return errors.Join(errs...)
}

// ---- status per domain ----

// isLocalHost: names Let's Encrypt can never issue for.
func isLocalHost(h string) bool {
	h = strings.TrimPrefix(h, "*.")
	return h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") ||
		net.ParseIP(h) != nil || !strings.Contains(h, ".")
}

// certStatus evaluates the certificate for one domain.
func certStatus(d store.AppDomain, certs []IssuedCert, now time.Time, threshold int) (status, issuer, expires, detail string) {
	if !d.HTTPS {
		return "", "", "", ""
	}
	mode := d.CertMode
	var best *IssuedCert
	for i := range certs {
		c := certs[i]
		if !c.Covers(d.Host) {
			continue
		}
		if mode == "custom" && c.Resolver != "custom" || mode != "custom" && c.Resolver == "custom" {
			continue
		}
		if best == nil || c.NotAfter.After(best.NotAfter) {
			best = &certs[i]
		}
	}
	if best == nil {
		created, _ := time.Parse(time.RFC3339, d.CreatedAt)
		switch {
		case mode == "custom":
			return "missing", "", "", "No uploaded certificate covers " + d.Host + "."
		case isLocalHost(d.Host) && mode != "dns":
			return "local", "", "", "Public certificate authorities don't issue certificates for local names; the proxy serves a self-signed certificate."
		case strings.HasPrefix(d.Host, "*.") && mode != "dns":
			return "missing", "", "", "Wildcard certificates need the DNS-01 challenge: configure a DNS provider and set the certificate mode to DNS."
		case now.Sub(created) < 15*time.Minute:
			return "pending", "", "", "Waiting for the certificate authority."
		}
		return "missing", "", "", "No certificate issued yet."
	}
	days := int(best.NotAfter.Sub(now).Hours() / 24)
	exp := best.NotAfter.UTC().Format(time.RFC3339)
	switch {
	case now.After(best.NotAfter):
		return "expired", best.Issuer, exp, fmt.Sprintf("Expired %s.", best.NotAfter.Format("2 Jan 2006"))
	case days < threshold:
		return "expiring", best.Issuer, exp, fmt.Sprintf("Expires in %d day(s).", days)
	}
	return "valid", best.Issuer, exp, ""
}

// proxyLogHint returns the latest ACME error the proxy logged for host.
func proxyLogHint(ctx context.Context, x executor.Executor, host string) string {
	out, _ := executor.Output(ctx, x, fmt.Sprintf("docker logs --since 48h %s 2>&1 | grep -i -- %s | grep -iE 'acme|certificate|challenge|error' | tail -1", proxy.ContainerName, q(host)))
	out = strings.TrimSpace(out)
	if i := strings.Index(out, "error="); i >= 0 {
		out = out[i+6:]
	}
	return truncateStr(strings.Trim(out, `"`), 400)
}

type domainRow struct {
	store.AppDomain
	AppName string `db:"app_name"`
}

// CheckCertificates refreshes the certificate status of every HTTPS domain and
// renews certificates Traefik failed to renew. It returns the number of domains checked.
func (e *Engine) CheckCertificates(ctx context.Context, autoRenew bool) (int, error) {
	now := time.Now()
	threshold := e.RenewThreshold(ctx)
	custom := e.CustomCertificates(ctx)
	domains, err := store.Select[domainRow](ctx, e.db, "SELECT d.*, a.name AS app_name FROM app_domains d JOIN applications a ON a.id = d.application_id")
	if err != nil {
		return 0, err
	}
	byServer := map[int64][]IssuedCert{}
	execs := map[int64]executor.Executor{}
	servers, _ := store.Select[store.Server](ctx, e.db, "SELECT * FROM servers WHERE proxy_enabled = 1 AND status = 'reachable'")
	for _, srv := range servers {
		x, err := e.pool.For(ctx, srv)
		if err != nil {
			continue
		}
		certs, err := e.ServerCertificates(ctx, x, srv.ID)
		if err != nil {
			slog.Warn("read certificates", "server", srv.Name, "err", err)
			continue
		}
		byServer[srv.ID], execs[srv.ID] = certs, x
	}
	renew := map[int64][]string{} // server → hosts to force-renew
	checked := 0
	for _, d := range domains {
		if !d.HTTPS {
			if d.CertStatus != "" {
				_, _ = e.db.ExecContext(ctx, "UPDATE app_domains SET cert_status = '', cert_issuer = '', cert_expires_at = '', cert_error = '' WHERE id = ?", d.ID)
			}
			continue
		}
		app, err := store.ByID[store.Application](ctx, e.db, "applications", d.ApplicationID)
		if err != nil {
			continue
		}
		// The worst status across the application's servers wins.
		status, issuer, expires, detail := "", "", "", ""
		rank := map[string]int{"valid": 1, "local": 1, "pending": 2, "expiring": 3, "missing": 4, "expired": 5}
		for _, sid := range e.AppServerIDs(ctx, app) {
			certs, ok := byServer[sid]
			if !ok {
				continue
			}
			st, is, ex, de := certStatus(d.AppDomain, append(certs, custom...), now, threshold)
			if st == "missing" && d.CertMode != "custom" {
				if hint := proxyLogHint(ctx, execs[sid], d.Host); hint != "" {
					de = hint
				}
			}
			if rank[st] >= rank[status] {
				status, issuer, expires, detail = st, is, ex, de
			}
			if autoRenew && (st == "expiring" || st == "expired") && d.CertMode != "custom" {
				renew[sid] = append(renew[sid], d.Host)
			}
		}
		if status == "" {
			continue // no reachable server with a proxy
		}
		checked++
		_, _ = e.db.ExecContext(ctx, "UPDATE app_domains SET cert_status = ?, cert_issuer = ?, cert_expires_at = ?, cert_error = ?, cert_checked_at = ? WHERE id = ?",
			status, issuer, expires, detail, store.Now(), d.ID)
		e.notifyCertificate(ctx, d, status, expires, detail)
	}
	for sid, hosts := range renew {
		hosts = e.throttleRenewals(ctx, hosts)
		if len(hosts) == 0 {
			continue
		}
		if err := e.ForceRenew(ctx, execs[sid], hosts); err != nil {
			slog.Warn("force renew", "server", sid, "hosts", hosts, "err", err)
			e.notify.Send(notify.Event{Name: "certificate.failed", Title: "Certificate renewal failed", Message: err.Error(), Fields: map[string]string{"Domains": strings.Join(hosts, ", ")}})
			continue
		}
		e.db.LogActivity(ctx, 0, "certificate.force_renew", strings.Join(hosts, ", "))
		go e.verifyRenewal(context.WithoutCancel(ctx), hosts)
	}
	e.checkCustomExpiry(ctx, custom, now)
	return checked, nil
}

// throttleRenewals allows one forced renewal per host per 24 h (Let's Encrypt rate limits).
func (e *Engine) throttleRenewals(ctx context.Context, hosts []string) []string {
	var out []string
	for _, h := range hosts {
		last, _ := time.Parse(time.RFC3339, e.db.Setting(ctx, "cert_renew:"+h))
		if time.Since(last) < 24*time.Hour {
			continue
		}
		_ = e.db.SetSetting(ctx, "cert_renew:"+h, store.Now())
		out = append(out, h)
	}
	return out
}

// notifyCertificate sends at most one expiry notification per domain per day.
func (e *Engine) notifyCertificate(ctx context.Context, d domainRow, status, expires, detail string) {
	if status != "expiring" && status != "expired" && status != "missing" {
		return
	}
	key := "cert_notified:" + d.Host
	today := time.Now().UTC().Format("2006-01-02")
	if e.db.Setting(ctx, key) == today {
		return
	}
	created, _ := time.Parse(time.RFC3339, d.CreatedAt)
	if status == "missing" && time.Since(created) < time.Hour {
		return // give the CA some time for new domains
	}
	_ = e.db.SetSetting(ctx, key, today)
	title := map[string]string{"expiring": "Certificate expiring", "expired": "Certificate expired", "missing": "Certificate could not be issued"}[status]
	e.notify.Send(notify.Event{Name: "certificate.expiring", Title: fmt.Sprintf("%s: %s", title, d.Host), Message: detail,
		Fields: map[string]string{"Application": d.AppName, "Expires": expires}})
}

func (e *Engine) checkCustomExpiry(ctx context.Context, custom []IssuedCert, now time.Time) {
	for _, c := range custom {
		days := int(c.NotAfter.Sub(now).Hours() / 24)
		if days > 30 {
			continue
		}
		key := fmt.Sprintf("cert_notified:custom-%d", c.CustomID)
		today := now.UTC().Format("2006-01-02")
		if e.db.Setting(ctx, key) == today || (days > 1 && days != 30 && days != 14 && days != 7 && days != 3) {
			continue
		}
		_ = e.db.SetSetting(ctx, key, today)
		e.notify.Send(notify.Event{Name: "certificate.expiring", Title: fmt.Sprintf("Uploaded certificate %s expires in %d day(s)", c.Main, max(days, 0)),
			Message: "Upload a renewed certificate in Settings → Certificates; uploaded certificates can't be renewed automatically."})
	}
}

// ForceRenew makes Traefik issue new certificates for hosts: the proxy is
// stopped, the stale certificates are removed from acme.json and the proxy is
// started again (it requests missing certificates on start). Sites on the
// server are unavailable for a few seconds.
func (e *Engine) ForceRenew(ctx context.Context, x executor.Executor, hosts []string) error {
	raw, err := executor.Output(ctx, x, "cat "+q(e.acmePath()))
	if err != nil {
		return fmt.Errorf("read acme.json: %w", err)
	}
	updated, removed, err := RemoveFromACME([]byte(raw), hosts)
	if err != nil {
		return err
	}
	if removed == 0 {
		return nil
	}
	if _, err := executor.Output(ctx, x, "docker stop -t 10 "+proxy.ContainerName); err != nil {
		return fmt.Errorf("stop proxy: %w", err)
	}
	werr := executor.WriteFile(ctx, x, e.acmePath(), updated, "0600")
	if _, err := executor.Output(ctx, x, "docker start "+proxy.ContainerName); err != nil {
		return fmt.Errorf("start proxy: %w", err)
	}
	if werr != nil {
		return fmt.Errorf("write acme.json: %w", werr)
	}
	return nil
}

// RemoveFromACME drops the certificates covering any of hosts, preserving everything else.
func RemoveFromACME(raw []byte, hosts []string) ([]byte, int, error) {
	var doc map[string]map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, 0, fmt.Errorf("acme.json: %w", err)
	}
	removed := 0
	for resolver, r := range doc {
		var certs []json.RawMessage
		if rawCerts, ok := r["Certificates"]; ok && string(rawCerts) != "null" {
			if err := json.Unmarshal(rawCerts, &certs); err != nil {
				return nil, 0, err
			}
		}
		keep := certs[:0]
		for _, c := range certs {
			var ent acmeEntry
			_ = json.Unmarshal(c, &ent)
			ic := IssuedCert{Main: ent.Domain.Main, SANs: ent.Domain.SANs}
			match := false
			for _, h := range hosts {
				if ic.Covers(h) {
					match = true
				}
			}
			if match {
				removed++
				continue
			}
			keep = append(keep, c)
		}
		b, _ := json.Marshal(keep)
		doc[resolver]["Certificates"] = b
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	return out, removed, err
}

// verifyRenewal re-checks a few minutes after a forced renewal and reports the outcome.
func (e *Engine) verifyRenewal(ctx context.Context, hosts []string) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(3 * time.Minute):
	}
	if _, err := e.CheckCertificates(ctx, false); err != nil {
		return
	}
	var ok, failed []string
	for _, h := range hosts {
		var status, detail string
		_ = e.db.QueryRowContext(ctx, "SELECT cert_status, cert_error FROM app_domains WHERE host = ? LIMIT 1", h).Scan(&status, &detail)
		if status == "valid" {
			ok = append(ok, h)
		} else {
			failed = append(failed, h+": "+detail)
		}
	}
	if len(ok) > 0 {
		e.notify.Send(notify.Event{Name: "certificate.renewed", Title: "Certificates renewed", Message: strings.Join(ok, ", "), Success: true})
	}
	if len(failed) > 0 {
		e.notify.Send(notify.Event{Name: "certificate.failed", Title: "Certificate renewal failed", Message: strings.Join(failed, "\n")})
	}
}

// CertificateWatch checks certificates shortly after start and then every interval.
func (e *Engine) CertificateWatch(ctx context.Context, interval time.Duration) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Minute):
	}
	for {
		if err := e.SyncCustomCertificates(ctx); err != nil {
			slog.Warn("sync custom certificates", "err", err)
		}
		if _, err := e.CheckCertificates(ctx, true); err != nil {
			slog.Warn("certificate check", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// ProbeTLS connects to host:443 and returns the certificate visitors receive.
func ProbeTLS(ctx context.Context, host string) (IssuedCert, error) {
	d := &tls.Dialer{Config: &tls.Config{ServerName: host, InsecureSkipVerify: true}, NetDialer: &net.Dialer{Timeout: 8 * time.Second}} //nolint:gosec // inspection only
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, "443"))
	if err != nil {
		return IssuedCert{}, err
	}
	defer conn.Close()
	st := conn.(*tls.Conn).ConnectionState()
	if len(st.PeerCertificates) == 0 {
		return IssuedCert{}, errors.New("no certificate presented")
	}
	c := st.PeerCertificates[0]
	return IssuedCert{Resolver: "live", Main: c.Subject.CommonName, SANs: c.DNSNames, Issuer: issuerName(c), NotBefore: c.NotBefore, NotAfter: c.NotAfter}, nil
}

// RefreshProxies re-applies the proxy configuration on every server (recreating
// a proxy only when its configuration fingerprint changed).
func (e *Engine) RefreshProxies(ctx context.Context) {
	servers, err := store.Select[store.Server](ctx, e.db, "SELECT * FROM servers WHERE proxy_enabled = 1 AND status = 'reachable'")
	if err != nil {
		return
	}
	for _, srv := range servers {
		x, err := e.pool.For(ctx, srv)
		if err != nil {
			continue
		}
		if proxy.Status(ctx, x) != "running" {
			continue
		}
		if err := proxy.Ensure(ctx, x, e.ProxyOptions(ctx), false); err != nil {
			slog.Warn("refresh proxy", "server", srv.Name, "err", err)
		}
	}
}
