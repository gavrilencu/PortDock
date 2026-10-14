package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer pd_test" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/ok":
			_, _ = w.Write([]byte(`{"id":7,"name":"web"}`))
		case "/api/missing":
			w.WriteHeader(404)
		case "/api/bad":
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"name is required"}`))
		default:
			w.WriteHeader(502)
			_, _ = w.Write([]byte("bad gateway"))
		}
	}))
	defer srv.Close()
	c, ctx := newClient(srv.URL+"/", "pd_test", "test"), context.Background()

	var out struct {
		ID   int64
		Name string
	}
	if err := c.do(ctx, "GET", "/api/ok", nil, &out); err != nil || out.ID != 7 || out.Name != "web" {
		t.Fatalf("ok: %v %+v", err, out)
	}
	if err := c.do(ctx, "GET", "/api/missing", nil, nil); !errors.Is(err, errNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := c.do(ctx, "POST", "/api/bad", map[string]any{}, nil); err == nil || !strings.Contains(err.Error(), "name is required (HTTP 400)") {
		t.Fatalf("bad: %v", err)
	}
	if err := c.do(ctx, "GET", "/api/other", nil, nil); err == nil || !strings.Contains(err.Error(), "HTTP 502: bad gateway") {
		t.Fatalf("other: %v", err)
	}
	if err := newClient(srv.URL, "wrong", "test").do(ctx, "GET", "/api/ok", nil, nil); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("token: %v", err)
	}
}

func TestParseRef(t *testing.T) {
	if s, p, k, err := parseRef("vault-prod:/shop/db/#password"); err != nil || s != "vault-prod" || p != "shop/db" || k != "password" {
		t.Errorf("%s %s %s %v", s, p, k, err)
	}
	if _, p, k, err := parseRef("keyvault:db-password"); err != nil || p != "db-password" || k != "" {
		t.Errorf("key vault: %s %s %v", p, k, err)
	}
	for _, bad := range []string{"", "vault-prod", ":shop/db#k", "vault-prod:#k"} {
		if _, _, _, err := parseRef(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestHostPath(t *testing.T) {
	for in, want := range map[string]string{
		"https://App.Example.com":     "app.example.com",
		"app.example.com/api/":        "app.example.com/api",
		"http://shop.localtest.me/v1": "shop.localtest.me/v1",
	} {
		if h, p := hostPath(in); h+p != want {
			t.Errorf("%s: got %s%s, want %s", in, h, p, want)
		}
	}
}

func TestApplicationBody(t *testing.T) {
	m := applicationModel{
		Name: types.StringValue("web"), EnvironmentID: types.Int64Value(3), ServerID: types.Int64Value(1),
		DockerImage: types.StringValue("nginx:alpine"), GitRepository: types.StringNull(),
		BuildPack: types.StringUnknown(), Port: types.Int64Value(80), Replicas: types.Int64Unknown(),
		MemoryLimit: types.StringValue("256m"), AutoDeploy: types.BoolUnknown(),
	}
	b, err := m.body()
	if err != nil {
		t.Fatal(err)
	}
	if b["source_type"] != "image" || b["build_pack"] != "image" || b["docker_image"] != "nginx:alpine" || b["port"] != int64(80) || b["memory_limit"] != "256m" {
		t.Fatalf("image body: %v", b)
	}
	for _, k := range []string{"replicas", "auto_deploy", "git_repository"} {
		if _, ok := b[k]; ok {
			t.Errorf("unknown or unset %s sent: %v", k, b)
		}
	}

	m.GitRepository = types.StringValue("https://github.com/example/app")
	if _, err := m.body(); err == nil {
		t.Fatal("both sources accepted")
	}
	m.DockerImage, m.BuildPack = types.StringNull(), types.StringValue("dockerfile")
	if b, err = m.body(); err != nil || b["source_type"] != "git" || b["build_pack"] != "dockerfile" {
		t.Fatalf("git body: %v %v", err, b)
	}
	m.GitRepository = types.StringNull()
	if _, err := m.body(); err == nil {
		t.Fatal("no source accepted")
	}
}
