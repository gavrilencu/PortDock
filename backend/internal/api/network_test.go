package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNetworkSettings(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("dev@corp.com", "correct-horse", "member")
	c := e.login("owner@corp.com")

	e.expect(e.login("dev@corp.com"), "PATCH", "/api/settings", map[string]any{"proxy_http": "http://p:3128"}, 403)
	for _, bad := range []map[string]any{
		{"proxy_http": "proxy.corp:3128"},
		{"proxy_http": "http://user:pass@proxy.corp:3128"},
		{"proxy_builds": "true"}, // no proxy yet
		{"registry_mirror": "nexus:8443"},
		{"build_env": "NOT VALID"},
	} {
		e.expect(c, "PATCH", "/api/settings", bad, 400)
	}
	got := e.expect(c, "PATCH", "/api/settings", map[string]any{
		"proxy_http": "http://proxy.corp:3128", "proxy_no": "localhost,.corp", "proxy_username": "svc", "proxy_password": "s3cret",
		"proxy_builds": "true", "proxy_containers": "true", "registry_mirror": "https://nexus.corp:8443", "npm_registry": "https://nexus.corp/npm/",
		"build_env": "MAVEN_OPTS=-Dx=y",
	}, 200)
	if got["proxy_password_set"] != true || got["proxy_password"] != nil || got["proxy_builds"] != "true" || got["registry_mirror"] != "https://nexus.corp:8443" {
		t.Fatalf("settings: %v", got)
	}
	if raw := fmt.Sprint(e.expect(c, "GET", "/api/settings", nil, 200)); strings.Contains(raw, "s3cret") {
		t.Fatal("the proxy password is returned")
	}
	var stored string
	_ = e.db.QueryRow("SELECT value FROM settings WHERE key = 'proxy_password'").Scan(&stored)
	if stored == "" || stored == "s3cret" {
		t.Fatalf("the password must be stored encrypted: %q", stored)
	}
	// An empty password keeps the stored one; "-" clears it.
	e.expect(c, "PATCH", "/api/settings", map[string]any{"proxy_password": ""}, 200)
	if got := e.expect(c, "GET", "/api/settings", nil, 200); got["proxy_password_set"] != true {
		t.Fatal("empty password cleared the stored one")
	}
	e.expect(c, "PATCH", "/api/settings", map[string]any{"proxy_password": "-"}, 200)
	if got := e.expect(c, "GET", "/api/settings", nil, 200); got["proxy_password_set"] != false {
		t.Fatal("password not cleared")
	}

	// The connection test uses the settings being edited, through a real proxy.
	var through atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		through.Add(1)
		if r.Header.Get("Proxy-Authorization") == "" {
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer proxy.Close()
	res := e.expect(c, "POST", "/api/network/test", map[string]any{"settings": map[string]string{"proxy_http": proxy.URL, "proxy_username": ""}, "url": "http://registry.example.test/v2/"}, 200)
	if res["ok"] != false || !strings.Contains(fmt.Sprint(res["error"]), "407") {
		t.Fatalf("407: %v", res)
	}
	res = e.expect(c, "POST", "/api/network/test", map[string]any{"settings": map[string]string{"proxy_http": proxy.URL, "proxy_username": "svc", "proxy_password": "pw"}, "url": "http://registry.example.test/v2/"}, 200)
	if res["ok"] != true || res["status"].(float64) != 200 || through.Load() != 2 {
		t.Fatalf("through the proxy: %v (%d requests)", res, through.Load())
	}
	res = e.expect(c, "POST", "/api/network/test", map[string]any{"settings": map[string]string{"proxy_http": proxy.URL, "proxy_no": ".example.test"}, "url": "http://registry.example.test/v2/"}, 200)
	if res["via"] != "direct" {
		t.Fatalf("NO_PROXY: %v", res)
	}
}

func TestFleetViews(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("dev@corp.com", "correct-horse", "member")
	// Two servers PortDock can't reach: each is reported, the view still answers.
	if _, err := e.db.Exec(`INSERT INTO servers(id, name, host, port, user, status) VALUES (1, 'edge-a', '127.0.0.1', 1, 'root', 'unreachable'), (2, 'edge-b', '127.0.0.1', 1, 'root', 'unreachable')`); err != nil {
		t.Fatal(err)
	}
	c := e.login("owner@corp.com")
	res := e.expect(c, "GET", "/api/fleet/containers?stats=1", nil, 200)
	servers := res["servers"].([]any)
	if len(servers) != 2 || servers[0].(map[string]any)["error"] == "" || len(res["containers"].([]any)) != 0 {
		t.Fatalf("containers: %v", res)
	}
	if one := e.expect(c, "GET", "/api/fleet/containers?server=2", nil, 200)["servers"].([]any); len(one) != 1 || one[0].(map[string]any)["name"] != "edge-b" {
		t.Fatalf("one server: %v", one)
	}
	e.expect(c, "GET", "/api/fleet/images", nil, 200)
	e.expect(c, "GET", "/api/fleet/volumes", nil, 200)
	m := e.login("dev@corp.com")
	e.expect(m, "GET", "/api/fleet/containers", nil, 200)
	e.expect(m, "GET", "/api/fleet/images", nil, 403)
	e.expect(m, "GET", "/api/fleet/volumes", nil, 403)
	e.expect(m, "GET", "/api/servers/1/docker-network", nil, 403)
	e.expect(c, "POST", "/api/servers/1/docker-network", map[string]any{"mode": "bogus"}, 400)
}
