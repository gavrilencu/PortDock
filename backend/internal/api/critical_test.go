package api

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/portdock/portdock/internal/auth"
)

// A member with a project opens the dashboard: this used to deadlock the
// whole process (a query inside an open cursor on the single connection).
func TestMemberDashboardDoesNotDeadlock(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("member@corp.com", "correct-horse", "member")
	c := e.login("member@corp.com")
	e.expect(c, "POST", "/api/projects", map[string]any{"name": "p1"}, 200)
	done := make(chan map[string]any, 1)
	go func() { done <- e.expect(c, "GET", "/api/dashboard", nil, 200) }()
	select {
	case d := <-done:
		if d["counts"].(map[string]any)["projects"] != float64(1) {
			t.Fatalf("dashboard counts: %v", d["counts"])
		}
	case <-time.After(10 * time.Second):
		t.Fatal("/api/dashboard did not answer a member within 10s")
	}
}

// Administrators cannot take over the owner account by setting its password.
func TestAdminCannotSetOwnerPassword(t *testing.T) {
	e := newTestEnv(t)
	owner := e.user("owner@corp.com", "correct-horse", "owner")
	admin := e.user("admin@corp.com", "correct-horse", "admin")
	oc, ac := e.login("owner@corp.com"), e.login("admin@corp.com")
	e.expect(ac, "PATCH", fmt.Sprintf("/api/users/%d", owner.ID), map[string]any{"password": "taken-over-1"}, 403)
	e.expect(ac, "PATCH", fmt.Sprintf("/api/users/%d", owner.ID), map[string]any{"name": "The Owner"}, 200)
	e.expect(ac, "PATCH", fmt.Sprintf("/api/users/%d", admin.ID), map[string]any{"password": "new-password-1"}, 200)
	e.expect(oc, "PATCH", fmt.Sprintf("/api/users/%d", owner.ID), map[string]any{"password": "owner-sets-own-1"}, 200)
	if code, _ := e.do(e.client(), "POST", "/api/auth/login", map[string]string{"email": "owner@corp.com", "password": "taken-over-1"}); code == 200 {
		t.Fatal("the admin's password took effect on the owner")
	}
	if code, _ := e.do(e.client(), "POST", "/api/auth/login", map[string]string{"email": "owner@corp.com", "password": "owner-sets-own-1"}); code != 200 {
		t.Fatalf("owner's own password change: %d", code)
	}
}

// The interface is served with a strict Content-Security-Policy that still
// allows the theme bootstrap inlined in index.html, and index.html is never
// cached.
func TestPageSecurityHeaders(t *testing.T) {
	e := newTestEnv(t)
	ui := fstest.MapFS{
		"index.html":          {Data: []byte("<!doctype html><html><head><script>\n  document.documentElement.classList.add(\"dark\");\n</script></head><body><div id=\"root\"></div><script type=\"module\" src=\"/assets/index-abc.js\"></script></body></html>")},
		"assets/index-abc.js": {Data: []byte("console.log(1)")},
	}
	e.api.ui = ui
	srv := httptest.NewServer(e.api.Handler())
	defer srv.Close()
	res, err := http.Get(srv.URL + "/projects/1")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	csp := res.Header.Get("Content-Security-Policy")
	sum := sha256.Sum256([]byte("\n  document.documentElement.classList.add(\"dark\");\n"))
	want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	for _, part := range []string{"default-src 'self'", "script-src 'self' " + want, "frame-ancestors 'none'", "object-src 'none'", "connect-src 'self' ws://" + strings.TrimPrefix(srv.URL, "http://")} {
		if !strings.Contains(csp, part) {
			t.Errorf("CSP %q lacks %q", csp, part)
		}
	}
	if res.Header.Get("Cache-Control") != "no-cache" || res.Header.Get("Permissions-Policy") == "" || res.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("headers: %v", res.Header)
	}
	if res.Header.Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS on a plain-http instance")
	}
	res, _ = http.Get(srv.URL + "/assets/index-abc.js")
	res.Body.Close()
	if res.Header.Get("Content-Security-Policy") != "" || !strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("asset headers: %v", res.Header)
	}
}

// Second-factor codes are throttled per account across sign-in attempts.
func TestSecondFactorLockout(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	if _, err := e.db.Exec("UPDATE users SET totp_secret = ?, totp_enabled = 1 WHERE email = 'owner@corp.com'", e.api.box.Encrypt("JBSWY3DPEHPK3PXP")); err != nil {
		t.Fatal(err)
	}
	// Each client is a different address as far as the limiter can tell? No —
	// all requests come from 127.0.0.1, so use the per-account bucket: 10 failures.
	fails := 0
	for round := 0; round < 4 && fails < 10; round++ {
		c := e.client()
		_, res := e.do(c, "POST", "/api/auth/login", map[string]string{"email": "owner@corp.com", "password": "correct-horse"})
		token, _ := res["mfa_token"].(string)
		if token == "" {
			t.Fatalf("no MFA challenge: %v", res)
		}
		for i := 0; i < 4 && fails < 10; i++ {
			if code, _ := e.do(c, "POST", "/api/auth/login/mfa", map[string]string{"mfa_token": token, "code": "000000"}); code != 401 {
				t.Fatalf("attempt %d: %d", fails, code)
			}
			fails++
		}
	}
	c := e.client()
	code, res := e.do(c, "POST", "/api/auth/login", map[string]string{"email": "owner@corp.com", "password": "correct-horse"})
	if code == 429 {
		return // the address is locked before the second step; also fine
	}
	token, _ := res["mfa_token"].(string)
	if token == "" {
		t.Fatalf("no MFA challenge after failures: %v", res)
	}
	if code, _ := e.do(c, "POST", "/api/auth/login/mfa", map[string]string{"mfa_token": token, "code": auth.TOTPCode("JBSWY3DPEHPK3PXP", time.Now())}); code != 429 {
		t.Fatalf("expected the second factor to be locked, got %d", code)
	}
	// The account bucket alone locks too: reset the address, keep the account.
	e.api.sec.limiter.Reset("ip:127.0.0.1")
	c = e.client()
	_, res = e.do(c, "POST", "/api/auth/login", map[string]string{"email": "owner@corp.com", "password": "correct-horse"})
	token, _ = res["mfa_token"].(string)
	if code, _ := e.do(c, "POST", "/api/auth/login/mfa", map[string]string{"mfa_token": token, "code": auth.TOTPCode("JBSWY3DPEHPK3PXP", time.Now())}); code != 429 {
		t.Fatalf("expected the account's second factor to be locked, got %d", code)
	}
}

// Members get a small subset of the settings and cannot browse the servers'
// images, volumes, networks or setup logs.
func TestMemberInfrastructureVisibility(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("dev@corp.com", "correct-horse", "member")
	owner, dev := e.login("owner@corp.com"), e.login("dev@corp.com")
	e.expect(owner, "PATCH", "/api/settings", map[string]string{"s3_access_key": "AKIA-SECRET", "proxy_http": "http://proxy:3128", "proxy_username": "svc", "build_env": "NPM_TOKEN=xyz", "instance_name": "Acme"}, 200)
	got := e.expect(dev, "GET", "/api/settings", nil, 200)
	for _, k := range []string{"s3_access_key", "s3_access_key_set", "proxy_http", "proxy_username", "build_env", "public_ipv4", "env_public_url", "registry_mirror"} {
		if _, ok := got[k]; ok {
			t.Errorf("member sees %s", k)
		}
	}
	if got["instance_name"] != "Acme" || got["public_url"] == "" || got["version"] == nil {
		t.Fatalf("member subset: %v", got)
	}
	adminView := e.expect(owner, "GET", "/api/settings", nil, 200)
	if adminView["s3_access_key_set"] != true || adminView["proxy_http"] != "http://proxy:3128" {
		t.Fatalf("admin view: %v", adminView)
	}
	if _, ok := adminView["s3_access_key"]; ok {
		t.Fatal("the S3 access key is returned in clear")
	}
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/servers/1/images", "/api/servers/1/volumes", "/api/servers/1/networks", "/api/servers/1/setup/stream?k=x"} {
		if code, _ := e.do(dev, "GET", path, nil); code != 403 {
			t.Errorf("member GET %s: %d", path, code)
		}
	}
	if code, _ := e.do(dev, "POST", "/api/servers/1/validate", nil); code != 403 {
		t.Errorf("member validate: %d", code)
	}
}

// JSON answers are gzip-compressed for browsers that accept it; streams are not.
func TestGzipResponses(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	c := e.login("owner@corp.com")
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/auth/me", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("headers: %v", res.Header)
	}
	zr, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	var me map[string]any
	if err := json.NewDecoder(zr).Decode(&me); err != nil || me["email"] != "owner@corp.com" {
		t.Fatalf("decoded: %v %v", me, err)
	}
	// Without Accept-Encoding, plain JSON.
	res2, _ := c.Get(e.srv.URL + "/api/auth/me")
	res2.Body.Close()
	if res2.Header.Get("Content-Encoding") != "" {
		t.Fatal("compressed without Accept-Encoding")
	}
	// The event stream is never compressed.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req3, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/api/events", nil)
	req3.Header.Set("Accept-Encoding", "gzip")
	if res3, err := c.Do(req3); err == nil {
		res3.Body.Close()
		if res3.Header.Get("Content-Encoding") == "gzip" {
			t.Fatal("event stream compressed")
		}
	}
}

// Two concurrent first registrations produce exactly one owner.
func TestFirstOwnerRace(t *testing.T) {
	e := newTestEnv(t)
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i], _ = e.do(e.client(), "POST", "/api/auth/register", map[string]string{"email": fmt.Sprintf("u%d@corp.com", i), "password": "correct-horse"})
		}()
	}
	wg.Wait()
	okCount := 0
	for _, c := range codes {
		if c == 200 {
			okCount++
		}
	}
	var owners int
	_ = e.db.QueryRow("SELECT COUNT(*) FROM users WHERE role = 'owner'").Scan(&owners)
	if okCount != 1 || owners != 1 {
		t.Fatalf("registrations ok=%d owners=%d codes=%v", okCount, owners, codes)
	}
}
