package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shipyard/shipyard/internal/store"
)

// AWS SigV4 test suite, "get-vanilla".
func TestSignV4Vanilla(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.amazonaws.com/", nil)
	now, _ := time.Parse("20060102T150405Z", "20150830T123600Z")
	SignV4(req, nil, "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "us-east-1", "service", now)
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestECRToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Amz-Target") != "AmazonEC2ContainerRegistry_V20150921.GetAuthorizationToken" ||
			!strings.Contains(r.Header.Get("Authorization"), "Credential=AKID/") || !strings.Contains(r.Header.Get("Authorization"), "/eu-central-1/ecr/aws4_request") {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"__type":"UnrecognizedClientException","message":"bad signature"}`))
			return
		}
		tok := base64.StdEncoding.EncodeToString([]byte("AWS:registry-password"))
		_ = json.NewEncoder(w).Encode(map[string]any{"authorizationData": []map[string]string{{"authorizationToken": tok}}})
	}))
	defer srv.Close()
	old := ecrEndpoint
	ecrEndpoint = func(string) string { return srv.URL + "/" }
	defer func() { ecrEndpoint = old }()

	reg := store.Registry{Kind: "ecr", Host: "123456789012.dkr.ecr.eu-central-1.amazonaws.com", Username: "AKID"}
	user, pass, err := Credentials(context.Background(), reg, "secret")
	if err != nil || user != "AWS" || pass != "registry-password" {
		t.Fatalf("%s %s %v", user, pass, err)
	}
	reg.Username = "OTHER"
	if _, _, err := Credentials(context.Background(), reg, "secret"); err == nil || !strings.Contains(err.Error(), "bad signature") {
		t.Fatalf("expected rejection, got %v", err)
	}
	if _, err := ECRRegion("ghcr.io"); err == nil {
		t.Fatal("non-ECR host accepted")
	}
}

func TestPingBearer(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/":
			if r.Header.Get("Authorization") == "Bearer good-token" {
				w.WriteHeader(200)
				return
			}
			w.Header().Set("Www-Authenticate", `Bearer realm="`+srv.URL+`/token",service="test-registry"`)
			w.WriteHeader(401)
		case "/token":
			u, p, _ := r.BasicAuth()
			if u != "bot" || p != "s3cret" || r.URL.Query().Get("service") != "test-registry" {
				w.WriteHeader(401)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "good-token"})
		}
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://") // 127.0.0.1 → plain HTTP, like Docker
	reg := store.Registry{Kind: "harbor", Host: host, Username: "bot"}
	if err := Ping(context.Background(), reg, "s3cret"); err != nil {
		t.Fatalf("valid credentials rejected: %v", err)
	}
	if err := Ping(context.Background(), reg, "wrong"); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("invalid credentials accepted: %v", err)
	}
}

func TestRepositoryNaming(t *testing.T) {
	reg := store.Registry{Host: "ghcr.io", Namespace: "Acme/"}
	if got := Repository(reg, "", "My API (prod)"); got != "ghcr.io/acme/my-api-prod" {
		t.Fatal(got)
	}
	if got := Repository(reg, "/team/custom/", "x"); got != "ghcr.io/team/custom" {
		t.Fatal(got)
	}
	for in, want := range map[string]string{"https://GHCR.io/": "ghcr.io", "harbor.corp:8443/project": "harbor.corp:8443"} {
		if got, err := NormalizeHost(in); err != nil || got != want {
			t.Errorf("%s → %s %v", in, got, err)
		}
	}
	if _, err := NormalizeHost("bad host"); err == nil {
		t.Error("invalid host accepted")
	}
}
