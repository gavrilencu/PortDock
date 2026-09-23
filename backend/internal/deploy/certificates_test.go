package deploy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/shipyard/shipyard/internal/proxy"
	"github.com/shipyard/shipyard/internal/store"
)

func selfSigned(t *testing.T, names []string, notAfter time.Time) (certPEM, keyPEM []byte) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: names[0]}, Issuer: pkix.Name{CommonName: "Test CA"},
		DNSNames: names, NotBefore: notAfter.Add(-90 * 24 * time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalPKCS8PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb})
}

func acmeDoc(t *testing.T, certs map[string][]struct {
	main     string
	sans     []string
	notAfter time.Time
}) []byte {
	doc := map[string]any{}
	for resolver, list := range certs {
		var entries []any
		for _, c := range list {
			pemBytes, _ := selfSigned(t, append([]string{c.main}, c.sans...), c.notAfter)
			entries = append(entries, map[string]any{"domain": map[string]any{"main": c.main, "sans": c.sans},
				"certificate": base64.StdEncoding.EncodeToString(pemBytes), "key": "a2V5", "Store": "default"})
		}
		doc[resolver] = map[string]any{"Account": map[string]any{"Email": "ops@example.com"}, "Certificates": entries}
	}
	b, _ := json.Marshal(doc)
	return b
}

type c = struct {
	main     string
	sans     []string
	notAfter time.Time
}

func TestACMEParseStatusAndRemove(t *testing.T) {
	now := time.Now()
	raw := acmeDoc(t, map[string][]c{
		"letsencrypt":     {{"shop.example.com", []string{"www.shop.example.com"}, now.Add(60 * 24 * time.Hour)}, {"old.example.com", nil, now.Add(5 * 24 * time.Hour)}},
		"letsencrypt-dns": {{"*.apps.example.com", nil, now.Add(80 * 24 * time.Hour)}},
	})
	certs, err := ParseACME(raw)
	if err != nil || len(certs) != 3 {
		t.Fatalf("parse: %v %d", err, len(certs))
	}
	dom := func(host, mode string) store.AppDomain {
		return store.AppDomain{Host: host, HTTPS: true, CertMode: mode, CreatedAt: now.Add(-48 * time.Hour).Format(time.RFC3339)}
	}
	cases := []struct {
		host, mode, want string
	}{
		{"shop.example.com", "auto", "valid"},
		{"www.shop.example.com", "auto", "valid"},
		{"old.example.com", "auto", "expiring"},
		{"api.apps.example.com", "auto", "valid"},   // covered by the wildcard
		{"a.b.apps.example.com", "auto", "missing"}, // wildcards match one label only
		{"app.localhost", "auto", "local"},
		{"*.other.example.com", "auto", "missing"},
		{"shop.example.com", "custom", "missing"}, // ACME certs don't satisfy custom mode
	}
	for _, tc := range cases {
		st, issuer, _, detail := certStatus(dom(tc.host, tc.mode), certs, now, 14)
		if st != tc.want {
			t.Errorf("%s (%s): got %s (%s) want %s", tc.host, tc.mode, st, detail, tc.want)
		}
		if st == "valid" && issuer == "" {
			t.Errorf("%s: issuer missing", tc.host)
		}
	}
	if st, _, _, _ := certStatus(store.AppDomain{Host: "new.example.com", HTTPS: true, CreatedAt: now.Format(time.RFC3339)}, certs, now, 14); st != "pending" {
		t.Errorf("new domain: %s", st)
	}

	// Forced renewal removes only the certificates covering the host; everything else is kept.
	out, removed, err := RemoveFromACME(raw, []string{"old.example.com", "x.apps.example.com"})
	if err != nil || removed != 2 {
		t.Fatalf("remove: %v %d", err, removed)
	}
	left, _ := ParseACME(out)
	if len(left) != 1 || left[0].Main != "shop.example.com" || !strings.Contains(string(out), `"Email": "ops@example.com"`) {
		t.Fatalf("remaining: %+v\n%s", left, out)
	}
}

func TestCustomCertificateMatching(t *testing.T) {
	now := time.Now()
	custom := []IssuedCert{{Resolver: "custom", Main: "*.corp.example", SANs: []string{"*.corp.example", "corp.example"}, NotAfter: now.Add(200 * 24 * time.Hour), Issuer: "Corp CA"}}
	d := store.AppDomain{Host: "portal.corp.example", HTTPS: true, CertMode: "custom"}
	if st, issuer, _, _ := certStatus(d, custom, now, 14); st != "valid" || issuer != "Corp CA" {
		t.Fatalf("custom: %s %s", st, issuer)
	}
	d.CertMode = "auto" // uploaded certs are only used for domains in custom mode
	if st, _, _, _ := certStatus(d, custom, now, 14); st == "valid" {
		t.Fatal("custom cert used for an auto domain")
	}
}

func TestRoutesUseResolverPerMode(t *testing.T) {
	out := string(proxy.RenderDynamic(proxy.AppRoutes{Name: "a", Upstreams: []string{"c1"}, Domains: []proxy.DomainRoute{
		{Host: "a.example.com", Port: 80, HTTPS: true},
		{Host: "*.apps.example.com", Port: 80, HTTPS: true, Cert: "dns"},
		{Host: "portal.corp.example", Port: 80, HTTPS: true, Cert: "custom"},
	}}))
	for _, want := range []string{"certResolver: letsencrypt\n", "certResolver: letsencrypt-dns", "main: '*.apps.example.com'", "tls: {}"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	a := proxy.Options{ACMEEmail: "a@b.c"}
	b := a
	b.DNSProvider, b.DNSEnv = "cloudflare", map[string]string{"CF_DNS_API_TOKEN": "x"}
	c := b
	c.DNSEnv = map[string]string{"CF_DNS_API_TOKEN": "y"}
	if a.Fingerprint() == b.Fingerprint() || b.Fingerprint() == c.Fingerprint() || a.Fingerprint() != (proxy.Options{ACMEEmail: "a@b.c"}).Fingerprint() {
		t.Fatal("fingerprint must change with the configuration (including credentials)")
	}
	d := a
	d.DataDir = "/data/other"
	if d.Fingerprint() == a.Fingerprint() {
		t.Fatal("fingerprint must include the data directory (mounts)")
	}
}
