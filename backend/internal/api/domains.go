package api

import (
	"context"
	"fmt"
	"github.com/portdock/portdock/internal/composefile"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/proxy"
	"github.com/portdock/portdock/internal/store"
)

// ---- DNS verification ----

var cloudflareNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18", "108.162.192.0/18",
		"190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
		"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22", "2400:cb00::/32", "2606:4700::/32", "2803:f800::/32",
		"2405:b500::/32", "2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
	} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

func isCloudflare(ip net.IP) bool {
	return slices.ContainsFunc(cloudflareNets, func(n *net.IPNet) bool { return n.Contains(ip) })
}

// serverIPs returns the public addresses a domain should point to for srv.
func (s *Server) serverIPs(ctx context.Context, srv store.Server) []net.IP {
	var out []net.IP
	if srv.IsLocal || srv.Host == "" || srv.Host == "localhost" {
		for _, k := range []string{"public_ipv4", "public_ipv6"} {
			if ip := net.ParseIP(strings.TrimSpace(s.db.Setting(ctx, k))); ip != nil {
				out = append(out, ip)
			}
		}
		return out
	}
	if ip := net.ParseIP(srv.Host); ip != nil {
		return []net.IP{ip}
	}
	ips, _ := net.DefaultResolver.LookupIP(ctx, "ip", srv.Host)
	return ips
}

// checkDNS resolves host and compares it with the expected server addresses.
func checkDNS(ctx context.Context, host string, expected []net.IP) (status, detail string) {
	if strings.HasPrefix(host, "*.") {
		host = "portdock-dns-check." + strings.TrimPrefix(host, "*.")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return "local", "Resolves to this machine only (development). Use a real domain for other visitors."
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return "unresolved", "No A/AAAA record found for " + host
	}
	var got []string
	for _, ip := range ips {
		got = append(got, ip.String())
		if slices.ContainsFunc(expected, func(e net.IP) bool { return e.Equal(ip) }) {
			return "ok", "Points to " + ip.String()
		}
	}
	if isCloudflare(ips[0]) {
		return "proxied", "Proxied through Cloudflare (" + strings.Join(got, ", ") + "); make sure the origin record points to this server"
	}
	if len(expected) == 0 {
		return "mismatch", "Resolves to " + strings.Join(got, ", ") + ". Set the server's public IP in Settings to verify it."
	}
	var want []string
	for _, e := range expected {
		want = append(want, e.String())
	}
	return "mismatch", "Resolves to " + strings.Join(got, ", ") + " but the server is " + strings.Join(want, ", ")
}

func (s *Server) checkDomain(ctx context.Context, d *store.AppDomain, expected []net.IP) {
	d.DNSStatus, d.DNSDetail = checkDNS(ctx, d.Host, expected)
	d.DNSCheckedAt = store.Now()
	_, _ = s.db.ExecContext(ctx, "UPDATE app_domains SET dns_status = ?, dns_detail = ?, dns_checked_at = ? WHERE id = ?", d.DNSStatus, d.DNSDetail, d.DNSCheckedAt, d.ID)
}

// ---- domain CRUD ----

type domainInput struct {
	Domain      *string `json:"domain"` // URL or host, e.g. https://app.example.com/api
	Port        *int    `json:"port"`
	Service     *string `json:"service"`
	HTTPS       *bool   `json:"https"`
	WWWRedirect *string `json:"www_redirect"`
	Indexing    *bool   `json:"indexing"`
	CertMode    *string `json:"cert_mode"`
}

// parseDomain accepts "app.example.com", "https://app.example.com/path", "http://…".
func parseDomain(raw string) (host, path string, https *bool, err error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return "", "", nil, badRequest("domain is required")
	}
	if strings.Contains(raw, "://") {
		u, perr := url.Parse(raw)
		if perr != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return "", "", nil, badRequest("invalid domain %q", raw)
		}
		v := u.Scheme == "https"
		https = &v
		raw = u.Host + u.Path
	}
	host, path, _ = strings.Cut(raw, "/")
	if h, _, e := net.SplitHostPort(host); e == nil {
		return "", "", nil, badRequest("remove the port from %q; set the internal port separately", h)
	}
	if !proxy.ValidHost(host) {
		return "", "", nil, badRequest("%q is not a valid domain name", host)
	}
	if path != "" {
		path = "/" + strings.Trim(path, "/")
	}
	return host, path, https, nil
}

func (s *Server) loadDomain(r *http.Request) (store.AppDomain, store.Application, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.AppDomain{}, store.Application{}, err
	}
	d, err := store.ByID[store.AppDomain](r.Context(), s.db, "app_domains", id)
	if err != nil {
		return d, store.Application{}, err
	}
	a, err := store.ByID[store.Application](r.Context(), s.db, "applications", d.ApplicationID)
	return d, a, err
}

type domainsResponse struct {
	Domains         []store.AppDomain `json:"domains"`
	RequiresDeploy  bool              `json:"requires_deploy"`
	RoutingWarning  string            `json:"routing_warning,omitempty"`
	ServerAddresses []string          `json:"server_addresses"`
}

// afterDomainChange syncs the denormalized column and re-applies routing.
func (s *Server) afterDomainChange(ctx context.Context, app store.Application) domainsResponse {
	_ = s.db.SyncDomainsColumn(ctx, app.ID)
	s.hub.Event("application", app.ID, app.Status)
	res := s.domainsOf(ctx, app)
	if err := s.engine.ApplyRoutes(ctx, app); err != nil {
		res.RoutingWarning = "Saved, but routing could not be updated: " + err.Error()
	}
	return res
}

func (s *Server) domainsOf(ctx context.Context, app store.Application) domainsResponse {
	ds, _ := s.db.Domains(ctx, app.ID)
	if ds == nil {
		ds = []store.AppDomain{}
	}
	srv, _ := store.ByID[store.Server](ctx, s.db, "servers", app.ServerID)
	res := domainsResponse{Domains: ds, ServerAddresses: []string{}}
	for _, ip := range s.serverIPs(ctx, srv) {
		res.ServerAddresses = append(res.ServerAddresses, ip.String())
	}
	return res
}

func (s *Server) listDomains(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	return ok(w, s.domainsOf(r.Context(), app))
}

// domainConflict reports which resource already uses host+path.
func (s *Server) domainConflict(ctx context.Context, host, path string, exceptID int64) error {
	var name string
	err := s.db.QueryRowContext(ctx, `SELECT a.name FROM app_domains d JOIN applications a ON a.id = d.application_id
		WHERE d.host = ? AND d.path = ? AND d.id != ?`, host, path, exceptID).Scan(&name)
	if err == nil {
		return errStatus(409, "%s%s is already used by application %q", host, path, name)
	}
	if iu, _ := url.Parse(s.db.Setting(ctx, "instance_url")); iu != nil && strings.EqualFold(iu.Hostname(), host) {
		return errStatus(409, "%s is the domain of this PortDock instance", host)
	}
	return nil
}

func (s *Server) createDomain(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	var in domainInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.Domain == nil {
		return badRequest("domain is required")
	}
	host, path, schemeHTTPS, err := parseDomain(*in.Domain)
	if err != nil {
		return err
	}
	d := store.AppDomain{ApplicationID: app.ID, Host: host, Path: path, HTTPS: app.HTTPS, WWWRedirect: "none", Indexing: true, DNSStatus: "unknown", CreatedAt: store.Now()}
	if schemeHTTPS != nil {
		d.HTTPS = *schemeHTTPS
	}
	if err := applyDomainInput(&d, in); err != nil {
		return err
	}
	if err := s.domainConflict(r.Context(), d.Host, d.Path, 0); err != nil {
		return err
	}
	if _, err := store.Insert(r.Context(), s.db, "app_domains", &d); err != nil {
		return err
	}
	srv, _ := store.ByID[store.Server](r.Context(), s.db, "servers", app.ServerID)
	s.checkDomain(r.Context(), &d, s.serverIPs(r.Context(), srv))
	s.activity(r, "domain.add", app.Name+": "+d.Host+d.Path)
	return ok(w, s.afterDomainChange(r.Context(), app))
}

// composeServiceRe matches Docker Compose service names.
var composeServiceRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)

func applyDomainInput(d *store.AppDomain, in domainInput) error {
	if in.Port != nil {
		if *in.Port < 0 || *in.Port > 65535 {
			return badRequest("invalid port")
		}
		d.Port = *in.Port
	}
	if in.Service != nil {
		svc := strings.TrimSpace(*in.Service)
		if svc != "" && !composeServiceRe.MatchString(svc) {
			return badRequest("invalid compose service name")
		}
		d.Service = svc
	}
	if in.HTTPS != nil {
		d.HTTPS = *in.HTTPS
	}
	if in.Indexing != nil {
		d.Indexing = *in.Indexing
	}
	if in.WWWRedirect != nil {
		switch *in.WWWRedirect {
		case "none", "to-www", "to-apex":
			d.WWWRedirect = *in.WWWRedirect
		default:
			return badRequest("www_redirect must be none, to-www or to-apex")
		}
	}
	if in.CertMode != nil {
		switch *in.CertMode {
		case "auto", "dns", "custom":
			d.CertMode = *in.CertMode
		default:
			return badRequest("cert_mode must be auto, dns or custom")
		}
	}
	if d.WWWRedirect != "none" && strings.HasPrefix(d.Host, "*.") {
		return badRequest("www redirects are not available for wildcard domains")
	}
	return nil
}

func (s *Server) updateDomain(w http.ResponseWriter, r *http.Request) error {
	d, app, err := s.loadDomain(r)
	if err != nil {
		return err
	}
	var in domainInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	hostChanged := false
	if in.Domain != nil {
		host, path, schemeHTTPS, err := parseDomain(*in.Domain)
		if err != nil {
			return err
		}
		hostChanged = host != d.Host
		d.Host, d.Path = host, path
		if schemeHTTPS != nil && in.HTTPS == nil {
			d.HTTPS = *schemeHTTPS
		}
	}
	if err := applyDomainInput(&d, in); err != nil {
		return err
	}
	if err := s.domainConflict(r.Context(), d.Host, d.Path, d.ID); err != nil {
		return err
	}
	if err := store.Update(r.Context(), s.db, "app_domains", &d); err != nil {
		return err
	}
	if hostChanged {
		srv, _ := store.ByID[store.Server](r.Context(), s.db, "servers", app.ServerID)
		s.checkDomain(r.Context(), &d, s.serverIPs(r.Context(), srv))
	}
	s.activity(r, "domain.update", app.Name+": "+d.Host+d.Path)
	return ok(w, s.afterDomainChange(r.Context(), app))
}

func (s *Server) deleteDomain(w http.ResponseWriter, r *http.Request) error {
	d, app, err := s.loadDomain(r)
	if err != nil {
		return err
	}
	if err := store.Delete(r.Context(), s.db, "app_domains", d.ID); err != nil {
		return err
	}
	s.activity(r, "domain.remove", app.Name+": "+d.Host+d.Path)
	return ok(w, s.afterDomainChange(r.Context(), app))
}

func (s *Server) checkDomainDNS(w http.ResponseWriter, r *http.Request) error {
	d, app, err := s.loadDomain(r)
	if err != nil {
		return err
	}
	srv, _ := store.ByID[store.Server](r.Context(), s.db, "servers", app.ServerID)
	s.checkDomain(r.Context(), &d, s.serverIPs(r.Context(), srv))
	return ok(w, d)
}

func (s *Server) checkAllDomains(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	ds, err := s.db.Domains(r.Context(), app.ID)
	if err != nil {
		return err
	}
	srv, _ := store.ByID[store.Server](r.Context(), s.db, "servers", app.ServerID)
	expected := s.serverIPs(r.Context(), srv)
	for i := range ds {
		s.checkDomain(r.Context(), &ds[i], expected)
	}
	return ok(w, s.domainsOf(r.Context(), app))
}

// setAppHTTPS toggles HTTPS (with HTTP -> HTTPS redirect) on every domain.
func (s *Server) setAppHTTPS(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(r.Context(), "UPDATE app_domains SET https = ? WHERE application_id = ?", in.Enabled, app.ID); err != nil {
		return err
	}
	app.HTTPS = in.Enabled
	_, _ = s.db.ExecContext(r.Context(), "UPDATE applications SET https = ? WHERE id = ?", in.Enabled, app.ID)
	s.activity(r, "domain.https", fmt.Sprintf("%s: %v", app.Name, in.Enabled))
	return ok(w, s.afterDomainChange(r.Context(), app))
}

type dnsRecord struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
	For   string `json:"for"`
}

// dnsRecords lists the records to create at the DNS provider.
func (s *Server) dnsRecords(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	ds, _ := s.db.Domains(r.Context(), app.ID)
	srv, _ := store.ByID[store.Server](r.Context(), s.db, "servers", app.ServerID)
	ips := s.serverIPs(r.Context(), srv)
	out := []dnsRecord{}
	for _, d := range ds {
		hosts := []string{d.Host}
		if d.WWWRedirect != "none" {
			a := strings.TrimPrefix(d.Host, "www.")
			hosts = []string{a, "www." + a}
		}
		for _, h := range hosts {
			for _, ip := range ips {
				t := "A"
				if ip.To4() == nil {
					t = "AAAA"
				}
				out = append(out, dnsRecord{Type: t, Name: h, Value: ip.String(), For: d.Host + d.Path})
			}
		}
	}
	return ok(w, map[string]any{"records": out, "server_addresses": len(ips) > 0})
}

// ---- instance network ----

func detectIP(ctx context.Context, endpoint string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64))
	ip := net.ParseIP(strings.TrimSpace(string(b)))
	if ip == nil {
		return ""
	}
	return ip.String()
}

// detectPublicIPs looks up this machine's public IPv4/IPv6 addresses.
func (s *Server) detectPublicIPs(w http.ResponseWriter, r *http.Request) error {
	v4 := detectIP(r.Context(), "https://api4.ipify.org")
	v6 := detectIP(r.Context(), "https://api6.ipify.org")
	if v6 != "" && net.ParseIP(v6).To4() != nil {
		v6 = "" // no IPv6 connectivity, the service answered over IPv4
	}
	return ok(w, map[string]string{"public_ipv4": v4, "public_ipv6": v6})
}

// checkHostDNS verifies an arbitrary host (e.g. the instance URL) against this machine.
func (s *Server) checkHostDNS(w http.ResponseWriter, r *http.Request) error {
	host := strings.TrimSpace(r.URL.Query().Get("host"))
	if u, err := url.Parse(host); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	if !proxy.ValidHost(strings.ToLower(host)) {
		return badRequest("invalid host")
	}
	var expected []net.IP
	for _, k := range []string{"public_ipv4", "public_ipv6"} {
		if ip := net.ParseIP(s.db.Setting(r.Context(), k)); ip != nil {
			expected = append(expected, ip)
		}
	}
	status, detail := checkDNS(r.Context(), strings.ToLower(host), expected)
	return ok(w, map[string]string{"status": status, "detail": detail})
}

// suggestDomain proposes a reachable domain for an application, like Coolify:
// the server's wildcard domain, <name>.<public-ip>.sslip.io, or <name>.localhost
// for instances running on a workstation.
func (s *Server) suggestDomain(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	srv, _ := store.ByID[store.Server](ctx, s.db, "servers", app.ServerID)
	name := proxy.SafeName(app.Name)
	if name == "" {
		name = "app"
	}
	var base, kind, scheme = "", "", "http://"
	switch {
	case srv.WildcardDomain != "":
		base, kind, scheme = strings.TrimPrefix(srv.WildcardDomain, "*."), "wildcard", "https://"
	default:
		for _, ip := range s.serverIPs(ctx, srv) {
			if v4 := ip.To4(); v4 != nil && !v4.IsPrivate() && !v4.IsLoopback() {
				base, kind = v4.String()+".sslip.io", "sslip"
				break
			}
		}
		if base == "" {
			base, kind = "localhost", "localhost"
		}
	}
	host := name + "." + base
	for i := 2; s.domainConflict(ctx, host, "", 0) != nil && i < 100; i++ {
		host = fmt.Sprintf("%s-%d.%s", name, i, base)
	}
	return ok(w, map[string]string{"domain": scheme + host, "kind": kind})
}

// composeServices lists the services of a compose application's last deployed
// compose file, so domains can target them. Empty before the first deploy.
func (s *Server) composeServices(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	out := []composefile.Service{}
	if app.BuildPack == "compose" {
		if ss, err := s.engine.ComposeServices(r.Context(), app); err == nil {
			out = ss
		}
	}
	return ok(w, map[string]any{"services": out})
}
