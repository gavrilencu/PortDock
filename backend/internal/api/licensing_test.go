package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/shipyard/shipyard/internal/license"
)

func TestLicensingGates(t *testing.T) {
	e := newTestEnv(t)
	_ = e.db.SetSetting(t.Context(), "license_trial_started", "") // start on Community
	e.user("owner@corp.com", "correct-horse", "owner")
	owner := e.login("owner@corp.com")

	lic := e.expect(owner, "GET", "/api/license", nil, 200)
	if lic["state"].(map[string]any)["plan"].(map[string]any)["id"] != "community" || lic["trial_available"] != true {
		t.Fatalf("license %v", lic)
	}
	// Community: 2 servers, no SSO / project roles; core features still work.
	for _, q := range []string{"INSERT INTO servers(name, host) VALUES ('a', '10.0.0.1')", "INSERT INTO servers(name, host) VALUES ('b', '10.0.0.2')"} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	keyID := e.expect(owner, "POST", "/api/ssh-keys", map[string]any{"name": "k"}, 200)["id"]
	third := map[string]any{"name": "c", "host": "127.0.0.1", "port": 1, "user": "root", "ssh_key_id": keyID}
	e.expect(owner, "POST", "/api/servers", third, 402)
	e.expect(owner, "POST", "/api/sso/providers", map[string]any{"name": "x", "issuer": "https://idp.example.com", "client_id": "c"}, 402)
	e.expect(owner, "POST", "/api/projects", map[string]any{"name": "private", "default_role": "none"}, 402)
	e.expect(owner, "POST", "/api/projects", map[string]any{"name": "open"}, 200)

	// Trial unlocks everything, once.
	e.expect(owner, "POST", "/api/license/trial", nil, 200)
	e.expect(owner, "POST", "/api/license/trial", nil, 400)
	e.expect(owner, "POST", "/api/servers", third, 200) // limit lifted (the server itself is unreachable)
	if st := e.expect(owner, "GET", "/api/auth/status", nil, 200)["license"].(map[string]any); st["source"] != "trial" {
		t.Fatalf("status license %v", st)
	}

	// Installing a signed key; forged keys are rejected.
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	license.SetPublicKeyForTesting(pub)
	key, _ := license.Sign(license.License{ID: "l", Customer: "Acme Bank", Plan: "enterprise", IssuedAt: time.Now().Format(time.RFC3339),
		ExpiresAt: time.Now().Add(365 * 24 * time.Hour).Format(time.RFC3339)}, priv)
	res := e.expect(owner, "PUT", "/api/license", map[string]string{"key": key}, 200)
	if st := res["state"].(map[string]any); st["source"] != "license" || st["license"].(map[string]any)["customer"] != "Acme Bank" {
		t.Fatalf("installed %v", st)
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	bad, _ := license.Sign(license.License{Plan: "enterprise", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)}, other)
	e.expect(owner, "PUT", "/api/license", map[string]string{"key": bad}, 400)
	e.expect(owner, "POST", "/api/sso/providers", map[string]any{"name": "Corp", "issuer": "https://idp.example.com", "client_id": "c"}, 200)
}
