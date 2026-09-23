package github

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

func TestAppJWTVerifies(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	tok, err := appJWT(12345, pemKey)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT parts, got %d", len(parts))
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Iss string `json:"iss"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}
	_ = json.Unmarshal(raw, &claims)
	if claims.Iss != "12345" {
		t.Errorf("iss = %q", claims.Iss)
	}
	if ttl := time.Unix(claims.Exp, 0).Sub(time.Now()); ttl <= 0 || ttl > 10*time.Minute {
		t.Errorf("exp must be within GitHub's 10 minute limit, got %s", ttl)
	}

	// PKCS#8 keys are accepted too.
	p8, _ := x509.MarshalPKCS8PrivateKey(key)
	if _, err := appJWT(1, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: p8}))); err != nil {
		t.Errorf("PKCS8 key rejected: %v", err)
	}
	if _, err := appJWT(1, "not a key"); err == nil {
		t.Error("invalid key accepted")
	}
}

func TestManifestAndURLs(t *testing.T) {
	m := Manifest("PortDock", "https://deploy.example.com/")
	if m["redirect_url"] != "https://deploy.example.com/api/github/callback" {
		t.Errorf("redirect_url = %v", m["redirect_url"])
	}
	hook := m["hook_attributes"].(map[string]any)
	if hook["active"] != true || hook["url"] != "https://deploy.example.com/api/webhooks/github-app" {
		t.Errorf("hook = %v", hook)
	}
	for _, private := range []string{"http://localhost:8000", "http://192.168.1.10:8000", "http://portdock.local", "http://myhost:8000"} {
		if _, ok := Manifest("x", private)["hook_attributes"]; ok {
			t.Errorf("%s: GitHub rejects unreachable webhook URLs; the hook must be omitted", private)
		}
	}
	if APIURLFor("https://github.com") != DefaultAPIURL || APIURLFor("https://ghe.corp.com/") != "https://ghe.corp.com/api/v3" {
		t.Error("APIURLFor mismatch")
	}
}
