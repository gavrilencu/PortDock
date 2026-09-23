package api

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/store"
)

// ---- inventory ----

type certRow struct {
	DomainID      int64  `json:"domain_id"`
	Host          string `json:"host"`
	ApplicationID int64  `json:"application_id"`
	Application   string `json:"application"`
	Mode          string `json:"mode"`
	Status        string `json:"status"`
	Issuer        string `json:"issuer"`
	ExpiresAt     string `json:"expires_at"`
	DaysLeft      *int   `json:"days_left"`
	CheckedAt     string `json:"checked_at"`
	Detail        string `json:"detail"`
}

func (s *Server) listCertificates(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	type row struct {
		store.AppDomain
		AppName string `db:"app_name"`
		EnvID   int64  `db:"env_id"`
	}
	rows, err := store.Select[row](ctx, s.db, "SELECT d.*, a.name AS app_name, a.environment_id AS env_id FROM app_domains d JOIN applications a ON a.id = d.application_id WHERE d.https = 1 ORDER BY d.host")
	if err != nil {
		return err
	}
	visible := s.envVisibility(ctx)
	out := []certRow{}
	for _, d := range rows {
		if !visible(d.EnvID) {
			continue
		}
		cr := certRow{DomainID: d.ID, Host: d.Host, ApplicationID: d.ApplicationID, Application: d.AppName, Mode: firstNonEmpty(d.CertMode, "auto"),
			Status: d.CertStatus, Issuer: d.CertIssuer, ExpiresAt: d.CertExpiresAt, CheckedAt: d.CertCheckedAt, Detail: d.CertError}
		if t, err := time.Parse(time.RFC3339, d.CertExpiresAt); err == nil {
			days := int(time.Until(t).Hours() / 24)
			cr.DaysLeft = &days
		}
		out = append(out, cr)
	}
	custom, _ := store.Select[store.CustomCertificate](ctx, s.db, "SELECT * FROM custom_certificates ORDER BY not_after")
	for i := range custom {
		custom[i].CertPEM = ""
	}
	return ok(w, map[string]any{"domains": out, "custom": custom, "renew_days": s.engine.RenewThreshold(ctx)})
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func (s *Server) checkCertificatesNow(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	_ = s.engine.SyncCustomCertificates(ctx)
	n, err := s.engine.CheckCertificates(ctx, false)
	if err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "certificate.check", fmt.Sprintf("%d domain(s)", n))
	return s.listCertificates(w, r)
}

// renewDomainCertificate forces a new certificate for one domain on all its servers.
func (s *Server) renewDomainCertificate(w http.ResponseWriter, r *http.Request) error {
	d, app, err := s.loadDomain(r)
	if err != nil {
		return err
	}
	if !d.HTTPS {
		return badRequest("HTTPS is disabled for this domain")
	}
	if d.CertMode == "custom" {
		return badRequest("this domain uses an uploaded certificate; upload a renewed one in Settings → Certificates")
	}
	ctx := r.Context()
	last, _ := time.Parse(time.RFC3339, s.db.Setting(ctx, "cert_renew:"+d.Host))
	if time.Since(last) < 10*time.Minute {
		return errStatus(429, "a renewal for %s was started %s ago; wait a few minutes", d.Host, time.Since(last).Round(time.Second))
	}
	_ = s.db.SetSetting(ctx, "cert_renew:"+d.Host, store.Now())
	var errs []error
	for _, sid := range s.engine.AppServerIDs(ctx, app) {
		x, srv, err := s.pool.ForID(ctx, sid)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := s.engine.ForceRenew(ctx, x, []string{d.Host}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", srv.Name, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return badRequest("%v", err)
	}
	_, _ = s.db.ExecContext(ctx, "UPDATE app_domains SET cert_status = 'pending', cert_error = 'Renewal requested.' WHERE id = ?", d.ID)
	s.activity(r, "certificate.renew", d.Host)
	d.CertStatus, d.CertError = "pending", "Renewal requested."
	return ok(w, d)
}

// ---- custom certificates ----

func parseKey(keyPEM []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("the private key is not PEM encoded")
	}
	if strings.Contains(block.Type, "ENCRYPTED") || x509.IsEncryptedPEMBlock(block) { //nolint:staticcheck // detection only
		return nil, errors.New("the private key is password protected; export it without a passphrase")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if s, ok := k.(crypto.Signer); ok {
			return s, nil
		}
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	return nil, errors.New("unsupported private key format")
}

func publicKeysEqual(a, b crypto.PublicKey) bool {
	switch k := a.(type) {
	case *rsa.PublicKey:
		return k.Equal(b)
	case *ecdsa.PublicKey:
		return k.Equal(b)
	case ed25519.PublicKey:
		return k.Equal(b)
	}
	return false
}

// ValidateCustomCertificate checks a PEM chain + key and extracts its metadata.
func ValidateCustomCertificate(certPEM, keyPEM string, now time.Time) (store.CustomCertificate, error) {
	var c store.CustomCertificate
	certPEM, keyPEM = strings.TrimSpace(certPEM)+"\n", strings.TrimSpace(keyPEM)+"\n"
	if _, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); err != nil {
		return c, fmt.Errorf("certificate and key don't form a valid pair: %v", err)
	}
	block, _ := pem.Decode([]byte(certPEM))
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return c, fmt.Errorf("invalid certificate: %v", err)
	}
	key, err := parseKey([]byte(keyPEM))
	if err != nil {
		return c, err
	}
	if !publicKeysEqual(leaf.PublicKey, key.Public()) {
		return c, errors.New("the private key does not belong to the first certificate")
	}
	if now.After(leaf.NotAfter) {
		return c, fmt.Errorf("the certificate expired on %s", leaf.NotAfter.Format("2 Jan 2006"))
	}
	names := append([]string{}, leaf.DNSNames...)
	if len(names) == 0 && leaf.Subject.CommonName != "" {
		names = []string{leaf.Subject.CommonName}
	}
	if len(names) == 0 {
		return c, errors.New("the certificate has no DNS names")
	}
	issuer := leaf.Issuer.CommonName
	if len(leaf.Issuer.Organization) > 0 {
		issuer = leaf.Issuer.Organization[0] + " (" + leaf.Issuer.CommonName + ")"
	}
	c = store.CustomCertificate{CertPEM: certPEM, Domains: strings.ToLower(strings.Join(names, ",")), Issuer: issuer,
		NotBefore: leaf.NotBefore.UTC().Format(time.RFC3339), NotAfter: leaf.NotAfter.UTC().Format(time.RFC3339)}
	return c, nil
}

func (s *Server) uploadCustomCertificate(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name        string `json:"name"`
		Certificate string `json:"certificate"`
		PrivateKey  string `json:"private_key"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	c, err := ValidateCustomCertificate(in.Certificate, in.PrivateKey, time.Now())
	if err != nil {
		return badRequest("%v", err)
	}
	c.Name = strings.TrimSpace(in.Name)
	if c.Name == "" {
		c.Name = strings.Split(c.Domains, ",")[0]
	}
	c.KeyPEM = s.box.Encrypt(strings.TrimSpace(in.PrivateKey) + "\n")
	c.CreatedAt = store.Now()
	if _, err := store.Insert(r.Context(), s.db, "custom_certificates", &c); err != nil {
		return err
	}
	if err := s.engine.SyncCustomCertificates(r.Context()); err != nil {
		w.Header().Set("X-PortDock-Warning", err.Error())
	}
	s.activity(r, "certificate.upload", c.Name+" ("+c.Domains+")")
	c.CertPEM, c.KeyPEM = "", ""
	return ok(w, c)
}

func (s *Server) deleteCustomCertificate(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, err := store.ByID[store.CustomCertificate](r.Context(), s.db, "custom_certificates", id)
	if err != nil {
		return err
	}
	if err := store.Delete(r.Context(), s.db, "custom_certificates", id); err != nil {
		return err
	}
	_ = s.engine.SyncCustomCertificates(r.Context())
	s.activity(r, "certificate.delete", c.Name)
	return ok(w, map[string]bool{"ok": true})
}

// ---- ACME / DNS provider settings ----

// dnsProviders lists common lego DNS providers and their credential variables.
var dnsProviders = []map[string]any{
	{"id": "cloudflare", "name": "Cloudflare", "env": []string{"CF_DNS_API_TOKEN"}, "hint": "API token with Zone:DNS:Edit permission."},
	{"id": "route53", "name": "Amazon Route 53", "env": []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_REGION", "AWS_HOSTED_ZONE_ID"}, "hint": "IAM user with route53:ChangeResourceRecordSets. Hosted zone ID is optional."},
	{"id": "azuredns", "name": "Azure DNS", "env": []string{"AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET", "AZURE_TENANT_ID", "AZURE_SUBSCRIPTION_ID", "AZURE_RESOURCE_GROUP"}, "hint": "Service principal with the DNS Zone Contributor role."},
	{"id": "gcloud", "name": "Google Cloud DNS", "env": []string{"GCE_PROJECT", "GCE_SERVICE_ACCOUNT"}, "hint": "GCE_SERVICE_ACCOUNT is the service-account JSON."},
	{"id": "digitalocean", "name": "DigitalOcean", "env": []string{"DO_AUTH_TOKEN"}},
	{"id": "hetzner", "name": "Hetzner DNS", "env": []string{"HETZNER_API_KEY"}},
	{"id": "ovh", "name": "OVHcloud", "env": []string{"OVH_ENDPOINT", "OVH_APPLICATION_KEY", "OVH_APPLICATION_SECRET", "OVH_CONSUMER_KEY"}},
	{"id": "godaddy", "name": "GoDaddy", "env": []string{"GODADDY_API_KEY", "GODADDY_API_SECRET"}},
	{"id": "rfc2136", "name": "RFC 2136 (Windows DNS, BIND, PowerDNS)", "env": []string{"RFC2136_NAMESERVER", "RFC2136_TSIG_KEY", "RFC2136_TSIG_SECRET", "RFC2136_TSIG_ALGORITHM"},
		"hint": "Dynamic DNS updates signed with TSIG — works with on-premises DNS such as Active Directory-integrated zones that accept secure updates via BIND."},
	{"id": "other", "name": "Other provider", "env": []string{}, "hint": "Any provider code from the Traefik/lego documentation with its environment variables."},
}

var acmeServers = []map[string]string{
	{"id": "", "name": "Let's Encrypt (production)"},
	{"id": "https://acme-staging-v02.api.letsencrypt.org/directory", "name": "Let's Encrypt (staging — for tests, untrusted)"},
}

var envKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

func (s *Server) certSettings(ctx context.Context) map[string]any {
	env := map[string]string{}
	_ = json.Unmarshal([]byte(s.box.MustDecrypt(s.db.Setting(ctx, "dns_env"))), &env)
	masked := map[string]string{}
	for k, v := range env {
		if v != "" {
			masked[k] = "********"
		}
	}
	return map[string]any{
		"acme_email":    s.db.Setting(ctx, "acme_email"),
		"acme_server":   s.db.Setting(ctx, "acme_server"),
		"dns_provider":  s.db.Setting(ctx, "dns_provider"),
		"dns_env":       masked,
		"dns_resolvers": s.db.Setting(ctx, "dns_resolvers"),
		"renew_days":    s.engine.RenewThreshold(ctx),
		"providers":     dnsProviders,
		"acme_servers":  acmeServers,
	}
}

func (s *Server) getCertSettings(w http.ResponseWriter, r *http.Request) error {
	return ok(w, s.certSettings(r.Context()))
}

func (s *Server) updateCertSettings(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ACMEEmail    *string           `json:"acme_email"`
		ACMEServer   *string           `json:"acme_server"`
		DNSProvider  *string           `json:"dns_provider"`
		DNSEnv       map[string]string `json:"dns_env"`
		DNSResolvers *string           `json:"dns_resolvers"`
		RenewDays    *int              `json:"renew_days"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	if in.ACMEEmail != nil {
		if v := strings.TrimSpace(*in.ACMEEmail); v != "" && !strings.Contains(v, "@") {
			return badRequest("invalid email address")
		}
		_ = s.db.SetSetting(ctx, "acme_email", strings.TrimSpace(*in.ACMEEmail))
	}
	if in.ACMEServer != nil {
		v := strings.TrimSpace(*in.ACMEServer)
		if v != "" {
			if u, err := url.Parse(v); err != nil || u.Scheme != "https" || u.Host == "" {
				return badRequest("the ACME directory must be an https:// URL")
			}
		}
		_ = s.db.SetSetting(ctx, "acme_server", v)
	}
	if in.DNSProvider != nil {
		v := strings.TrimSpace(*in.DNSProvider)
		if v != "" && !regexp.MustCompile(`^[a-z0-9.-]+$`).MatchString(v) {
			return badRequest("invalid DNS provider code")
		}
		_ = s.db.SetSetting(ctx, "dns_provider", v)
	}
	if in.DNSEnv != nil {
		prev := map[string]string{}
		_ = json.Unmarshal([]byte(s.box.MustDecrypt(s.db.Setting(ctx, "dns_env"))), &prev)
		next := map[string]string{}
		for k, v := range in.DNSEnv {
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			if !envKeyRe.MatchString(k) {
				return badRequest("invalid variable name %q", k)
			}
			if v == "********" {
				v = prev[k]
			}
			if v != "" {
				next[k] = v
			}
		}
		b, _ := json.Marshal(next)
		_ = s.db.SetSetting(ctx, "dns_env", s.box.Encrypt(string(b)))
	}
	if in.DNSResolvers != nil {
		_ = s.db.SetSetting(ctx, "dns_resolvers", strings.ReplaceAll(strings.TrimSpace(*in.DNSResolvers), " ", ""))
	}
	if in.RenewDays != nil {
		if *in.RenewDays < 3 || *in.RenewDays > 60 {
			return badRequest("renewal threshold must be between 3 and 60 days")
		}
		_ = s.db.SetSetting(ctx, "cert_renew_days", strconv.Itoa(*in.RenewDays))
	}
	s.activity(r, "certificate.settings", "")
	// Proxies pick up the new configuration (recreated when their fingerprint changes).
	go s.engine.RefreshProxies(context.WithoutCancel(ctx))
	return ok(w, s.certSettings(ctx))
}
