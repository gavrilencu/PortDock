package sso

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type fakeIdP struct {
	*httptest.Server
	key      *rsa.PrivateKey
	claims   map[string]any
	verifier string // expected PKCE verifier
}

func sign(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signed := enc(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"}) + "." + enc(claims)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func newFakeIdP(t *testing.T) *fakeIdP {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	f := &fakeIdP{key: key}
	mux := http.NewServeMux()
	f.Server = httptest.NewServer(mux)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Metadata{Issuer: f.URL, AuthorizationEndpoint: f.URL + "/authorize",
			TokenEndpoint: f.URL + "/token", UserinfoEndpoint: f.URL + "/userinfo", JWKSURI: f.URL + "/jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("code") != "good-code" || PKCE(r.Form.Get("code_verifier")) != PKCE(f.verifier) {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "bad code or verifier"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "at", "id_token": sign(t, key, "k1", f.claims)})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"sub": f.claims["sub"], "groups": []string{"platform-admins", "devs"}})
	})
	t.Cleanup(f.Close)
	return f
}

func TestExchangeAndVerify(t *testing.T) {
	idp := newFakeIdP(t)
	ctx := context.Background()
	m, err := Discover(ctx, idp.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	idp.verifier = "verifier-123"
	idp.claims = map[string]any{"iss": idp.URL, "aud": "client-1", "sub": "user-42", "nonce": "n1",
		"exp": time.Now().Add(time.Hour).Unix(), "email": "Jane@Corp.com", "email_verified": true, "name": "Jane"}

	auth, _ := url.Parse(AuthURL(m, "client-1", "https://shipyard/cb", "openid email", "st", "n1", idp.verifier))
	if auth.Query().Get("code_challenge_method") != "S256" || auth.Query().Get("nonce") != "n1" {
		t.Fatalf("bad auth url %s", auth)
	}

	c, err := Exchange(ctx, m, "client-1", "secret", "https://shipyard/cb", "good-code", idp.verifier, "n1", "groups")
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != "user-42" || c.Email != "jane@corp.com" || c.EmailVerified == nil || !*c.EmailVerified || len(c.Groups) != 2 {
		t.Fatalf("unexpected claims %+v", c)
	}

	// wrong nonce, audience, expired and tampered tokens must fail
	if _, err := Exchange(ctx, m, "client-1", "secret", "https://shipyard/cb", "good-code", idp.verifier, "other", "groups"); err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("nonce mismatch not detected: %v", err)
	}
	if _, err := Exchange(ctx, m, "client-2", "secret", "https://shipyard/cb", "good-code", idp.verifier, "n1", "groups"); err == nil || !strings.Contains(err.Error(), "audience") {
		t.Fatalf("audience mismatch not detected: %v", err)
	}
	if _, err := Exchange(ctx, m, "client-1", "secret", "https://shipyard/cb", "good-code", "wrong-verifier", "n1", "groups"); err == nil {
		t.Fatal("PKCE verifier not checked")
	}
	idp.claims["exp"] = time.Now().Add(-time.Hour).Unix()
	if _, err := Exchange(ctx, m, "client-1", "secret", "https://shipyard/cb", "good-code", idp.verifier, "n1", "groups"); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expiry not detected: %v", err)
	}
	idp.claims["exp"] = time.Now().Add(time.Hour).Unix()
	tok := sign(t, idp.key, "k1", idp.claims)
	parts := strings.Split(tok, ".")
	forged, _ := json.Marshal(map[string]any{"iss": idp.URL, "aud": "client-1", "sub": "admin", "nonce": "n1", "exp": time.Now().Add(time.Hour).Unix()})
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString(forged) + "." + parts[2]
	if _, err := VerifyIDToken(ctx, m, tampered, "client-1", "n1", time.Now()); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("tampered token accepted: %v", err)
	}
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	if _, err := VerifyIDToken(ctx, m, sign(t, other, "k1", idp.claims), "client-1", "n1", time.Now()); err == nil {
		t.Fatal("token signed by a foreign key accepted")
	}
}

func TestClaimsFromEntra(t *testing.T) {
	c := ClaimsFrom(map[string]any{"sub": "s", "preferred_username": "Bob@Contoso.onmicrosoft.com", "groups": []any{"g1"}, "given_name": "Bob", "family_name": "Smith"}, "")
	if c.Email != "bob@contoso.onmicrosoft.com" || c.Name != "Bob Smith" || c.EmailVerified != nil || len(c.Groups) != 1 {
		t.Fatalf("%+v", c)
	}
	if !issuerMatches("https://login.microsoftonline.com/{tenantid}/v2.0", "https://login.microsoftonline.com/abc/v2.0", map[string]any{"tid": "abc"}) {
		t.Fatal("entra multi-tenant issuer")
	}
}
