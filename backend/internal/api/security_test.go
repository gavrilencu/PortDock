package api

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/config"
	"github.com/portdock/portdock/internal/deploy"
	"github.com/portdock/portdock/internal/executor"
	gh "github.com/portdock/portdock/internal/github"
	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
	"github.com/portdock/portdock/internal/stream"
)

type testEnv struct {
	t   *testing.T
	srv *httptest.Server
	db  *store.DB
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	box, err := secrets.LoadOrCreate("", dir)
	if err != nil {
		t.Fatal(err)
	}
	hub := stream.NewHub()
	pool := executor.NewPool(db, box)
	n := notify.New(db, box)
	g := gh.New(db, box)
	cfg := config.Config{DataDir: filepath.ToSlash(dir), PublicURL: "http://placeholder", ProxyNetwork: "portdock-test"}
	engine := deploy.New(cfg, db, box, pool, hub, n, g)
	s := New(Deps{Config: cfg, DB: db, Box: box, Auth: auth.New(db, false), Pool: pool, Hub: hub, Engine: engine, Notifier: n, GitHub: g})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	_ = db.SetSetting(t.Context(), "instance_url", ts.URL)
	return &testEnv{t: t, srv: ts, db: db}
}

func (e *testEnv) user(email, password, role string) store.User {
	hash := ""
	if password != "" {
		hash, _ = auth.HashPassword(password)
	}
	u := store.User{Email: email, Name: strings.Split(email, "@")[0], PasswordHash: hash, Role: role, CreatedAt: store.Now()}
	if _, err := store.Insert(e.t.Context(), e.db, "users", &u); err != nil {
		e.t.Fatal(err)
	}
	return u
}

func (e *testEnv) client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (e *testEnv) do(c *http.Client, method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(res.Body)
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{"_raw": string(raw), "_location": res.Header.Get("Location")}
	}
	return res.StatusCode, out
}

func TestTwoFactorLoginFlow(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	c := e.client()

	if code, _ := e.do(c, "POST", "/api/auth/login", map[string]string{"email": "owner@corp.com", "password": "correct-horse"}); code != 200 {
		t.Fatalf("login: %d", code)
	}
	_, setup := e.do(c, "POST", "/api/auth/2fa/setup", map[string]string{"password": "correct-horse"})
	secret, _ := setup["secret"].(string)
	if secret == "" || !strings.HasPrefix(setup["otpauth_url"].(string), "otpauth://totp/") {
		t.Fatalf("setup: %v", setup)
	}
	if code, _ := e.do(c, "POST", "/api/auth/2fa/enable", map[string]string{"code": "000000"}); code != 400 {
		t.Fatalf("wrong code accepted: %d", code)
	}
	now := time.Now()
	code, res := e.do(c, "POST", "/api/auth/2fa/enable", map[string]string{"code": auth.TOTPCode(secret, now)})
	codes, _ := res["recovery_codes"].([]any)
	if code != 200 || len(codes) != 10 {
		t.Fatalf("enable: %d %v", code, res)
	}

	// New browser: the password alone must not create a session.
	c2 := e.client()
	_, res = e.do(c2, "POST", "/api/auth/login", map[string]string{"email": "owner@corp.com", "password": "correct-horse"})
	token, _ := res["mfa_token"].(string)
	if res["mfa_required"] != true || token == "" {
		t.Fatalf("expected MFA challenge: %v", res)
	}
	if code, _ := e.do(c2, "GET", "/api/auth/me", nil); code != 401 {
		t.Fatalf("session created before second factor: %d", code)
	}
	// The code used for enrollment can't be replayed.
	if code, _ := e.do(c2, "POST", "/api/auth/login/mfa", map[string]string{"mfa_token": token, "code": auth.TOTPCode(secret, now)}); code != 401 {
		t.Fatalf("replayed TOTP accepted: %d", code)
	}
	// A recovery code works exactly once.
	rc := codes[0].(string)
	if code, _ := e.do(c2, "POST", "/api/auth/login/mfa", map[string]string{"mfa_token": token, "code": rc}); code != 200 {
		t.Fatalf("recovery code rejected: %d", code)
	}
	if code, me := e.do(c2, "GET", "/api/auth/me", nil); code != 200 || me["totp_enabled"] != true {
		t.Fatalf("me after MFA: %d %v", code, me)
	}
	c3 := e.client()
	_, res = e.do(c3, "POST", "/api/auth/login", map[string]string{"email": "owner@corp.com", "password": "correct-horse"})
	if code, _ := e.do(c3, "POST", "/api/auth/login/mfa", map[string]string{"mfa_token": res["mfa_token"].(string), "code": rc}); code != 401 {
		t.Fatalf("recovery code reused: %d", code)
	}

	// Sessions: c2 sees itself plus c (enrollment kept the current session).
	var list []map[string]any
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/auth/sessions", nil)
	r2, _ := c2.Do(req)
	_ = json.NewDecoder(r2.Body).Decode(&list)
	r2.Body.Close()
	if len(list) != 2 {
		t.Fatalf("sessions: %v", list)
	}
	if code, _ := e.do(c2, "POST", "/api/auth/sessions/revoke-others", nil); code != 200 {
		t.Fatal("revoke others")
	}
	if code, _ := e.do(c, "GET", "/api/auth/me", nil); code != 401 {
		t.Fatalf("revoked session still valid: %d", code)
	}
}

func TestMFAPolicyAndLockout(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("admin@corp.com", "correct-horse", "admin")
	e.user("dev@corp.com", "correct-horse", "member")
	_ = e.db.SetSetting(t.Context(), "require_2fa", "admins")

	admin := e.client()
	e.do(admin, "POST", "/api/auth/login", map[string]string{"email": "admin@corp.com", "password": "correct-horse"})
	if code, st := e.do(admin, "GET", "/api/auth/status", nil); code != 200 || st["mfa_setup_required"] != true {
		t.Fatalf("status: %v", st)
	}
	if code, _ := e.do(admin, "GET", "/api/projects", nil); code != 403 {
		t.Fatalf("admin without 2FA reached the API: %d", code)
	}
	dev := e.client()
	e.do(dev, "POST", "/api/auth/login", map[string]string{"email": "dev@corp.com", "password": "correct-horse"})
	if code, _ := e.do(dev, "GET", "/api/projects", nil); code != 200 {
		t.Fatalf("member blocked by admins-only policy: %d", code)
	}

	// Password login disabled: members are refused, the owner still gets in.
	_ = e.db.SetSetting(t.Context(), "password_login", "false")
	if code, _ := e.do(e.client(), "POST", "/api/auth/login", map[string]string{"email": "dev@corp.com", "password": "correct-horse"}); code != 403 {
		t.Fatalf("password login not enforced: %d", code)
	}
	if code, _ := e.do(e.client(), "POST", "/api/auth/login", map[string]string{"email": "owner@corp.com", "password": "correct-horse"}); code != 200 {
		t.Fatalf("owner break-glass: %d", code)
	}

	// Brute force: the account locks after 10 failures.
	c := e.client()
	for range 10 {
		e.do(c, "POST", "/api/auth/login", map[string]string{"email": "owner@corp.com", "password": "nope"})
	}
	if code, _ := e.do(c, "POST", "/api/auth/login", map[string]string{"email": "owner@corp.com", "password": "correct-horse"}); code != 429 {
		t.Fatalf("expected lockout, got %d", code)
	}
}

// ---- SSO against a fake OpenID provider ----

type idp struct {
	*httptest.Server
	key    *rsa.PrivateKey
	claims map[string]any
	nonce  string
}

func newIdP(t *testing.T) *idp {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	p := &idp{key: key}
	mux := http.NewServeMux()
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": p.URL, "authorization_endpoint": p.URL + "/authorize",
			"token_endpoint": p.URL + "/token", "jwks_uri": p.URL + "/jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	})
	// /authorize immediately "logs the user in" and redirects back with a code.
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		p.nonce = q.Get("nonce")
		http.Redirect(w, r, q.Get("redirect_uri")+"?code=abc&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		claims := map[string]any{"iss": p.URL, "aud": "portdock", "exp": time.Now().Add(time.Hour).Unix(), "nonce": p.nonce}
		for k, v := range p.claims {
			claims[k] = v
		}
		signed := enc(map[string]string{"alg": "RS256", "kid": "k"}) + "." + enc(claims)
		sum := sha256.Sum256([]byte(signed))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": signed + "." + base64.RawURLEncoding.EncodeToString(sig)})
	})
	return p
}

// ssoLogin follows start → IdP → callback and returns the final redirect.
func (e *testEnv) ssoLogin(c *http.Client, slug string) string {
	e.t.Helper()
	loc := e.srv.URL + "/api/auth/sso/" + slug + "/start?next=/projects"
	for range 5 {
		res, err := c.Get(loc)
		if err != nil {
			e.t.Fatal(err)
		}
		res.Body.Close()
		next := res.Header.Get("Location")
		if next == "" {
			e.t.Fatalf("no redirect from %s (%d)", loc, res.StatusCode)
		}
		if strings.HasPrefix(next, "/") {
			return next
		}
		loc = next
	}
	e.t.Fatal("redirect loop")
	return ""
}

func TestSSOLogin(t *testing.T) {
	e := newTestEnv(t)
	owner := e.client()
	e.user("owner@corp.com", "correct-horse", "owner")
	e.do(owner, "POST", "/api/auth/login", map[string]string{"email": "owner@corp.com", "password": "correct-horse"})
	p := newIdP(t)

	code, prov := e.do(owner, "POST", "/api/sso/providers", map[string]any{"name": "Corp SSO", "kind": "keycloak", "issuer": p.URL, "client_id": "portdock",
		"client_secret": "s3cret", "auto_register": true, "allowed_domains": "corp.com", "admin_groups": "platform-admins"})
	if code != 200 || prov["slug"] != "corp-sso" || prov["client_secret_set"] != true {
		t.Fatalf("create provider: %d %v", code, prov)
	}
	if _, pub := e.do(e.client(), "GET", "/api/auth/providers", nil); len(pub["providers"].([]any)) != 1 {
		t.Fatalf("public providers: %v", pub)
	}

	// Just-in-time provisioning with group → admin mapping.
	p.claims = map[string]any{"sub": "u-1", "email": "jane@corp.com", "email_verified": true, "name": "Jane", "groups": []string{"platform-admins"}}
	c := e.client()
	if next := e.ssoLogin(c, "corp-sso"); next != "/projects" {
		t.Fatalf("sso redirect: %s", next)
	}
	_, me := e.do(c, "GET", "/api/auth/me", nil)
	if me["email"] != "jane@corp.com" || me["role"] != "admin" || me["auth_source"] != "corp-sso" || me["has_password"] != false {
		t.Fatalf("provisioned user: %v", me)
	}
	// SSO sessions are exempt from the TOTP policy.
	_ = e.db.SetSetting(t.Context(), "require_2fa", "all")
	if code, _ := e.do(c, "GET", "/api/projects", nil); code != 200 {
		t.Fatalf("SSO session blocked by 2FA policy: %d", code)
	}

	// Leaving the admin group demotes on next login; same subject → same user.
	p.claims["groups"] = []string{"devs"}
	e.ssoLogin(e.client(), "corp-sso")
	var role string
	_ = e.db.QueryRow("SELECT role FROM users WHERE email = 'jane@corp.com'").Scan(&role)
	if role != "member" {
		t.Fatalf("role not synced: %s", role)
	}

	// Foreign domains and unverified emails are refused.
	p.claims = map[string]any{"sub": "u-2", "email": "eve@evil.com", "email_verified": true}
	if next := e.ssoLogin(e.client(), "corp-sso"); !strings.Contains(next, "sso_error") || !strings.Contains(next, "evil.com") {
		t.Fatalf("foreign domain accepted: %s", next)
	}
	p.claims = map[string]any{"sub": "u-3", "email": "owner@corp.com", "email_verified": false}
	if next := e.ssoLogin(e.client(), "corp-sso"); !strings.Contains(next, "sso_error") {
		t.Fatalf("unverified email linked to owner: %s", next)
	}

	// A callback without the browser's state cookie is rejected (login CSRF).
	res, _ := e.client().Get(e.srv.URL + "/api/auth/sso/corp-sso/callback?code=abc&state=forged")
	if loc := res.Header.Get("Location"); !strings.Contains(loc, "sso_error") {
		t.Fatalf("forged callback: %s", loc)
	}
}
