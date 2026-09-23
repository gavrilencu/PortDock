package proxy

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type dyn struct {
	HTTP struct {
		Routers     map[string]router         `yaml:"routers"`
		Services    map[string]service        `yaml:"services"`
		Middlewares map[string]map[string]any `yaml:"middlewares"`
	} `yaml:"http"`
}

func parse(t *testing.T, b []byte) dyn {
	t.Helper()
	var d dyn
	if err := yaml.Unmarshal(b, &d); err != nil {
		t.Fatalf("invalid yaml: %v\n%s", err, b)
	}
	return d
}

func TestRenderDynamicHTTPSAndWWW(t *testing.T) {
	b := RenderDynamic(AppRoutes{
		Name:      "abc123",
		Upstreams: []string{"abc123-7-2", "abc123-7"},
		Domains: []DomainRoute{
			{Host: "example.com", Port: 3000, HTTPS: true, WWW: "to-www", Indexing: true},
			{Host: "api.example.com", Path: "/v1", Port: 8080, HTTPS: false, Indexing: false},
		},
	})
	d := parse(t, b)

	web := d.HTTP.Routers["abc123-d0-http"]
	if web.Middlewares[0] != "abc123-https" || web.TLS != nil {
		t.Errorf("http router must only redirect to https: %+v", web)
	}
	sec := d.HTTP.Routers["abc123-d0-https"]
	if sec.TLS == nil || sec.TLS.CertResolver != CertResolver {
		t.Error("https router must request a certificate")
	}
	if sec.Rule != "(Host(`example.com`) || Host(`www.example.com`))" {
		t.Errorf("rule = %s", sec.Rule)
	}
	if sec.Middlewares[0] != "abc123-d0-www" {
		t.Errorf("www redirect must run first: %v", sec.Middlewares)
	}
	rr := d.HTTP.Middlewares["abc123-d0-www"]["redirectRegex"].(map[string]any)
	if rr["regex"] != `^(https?)://example\.com(.*)$` || rr["replacement"] != "${1}://www.example.com${2}" {
		t.Errorf("www redirect = %v", rr)
	}

	api := d.HTTP.Routers["abc123-d1-http"]
	if api.Rule != "Host(`api.example.com`) && PathPrefix(`/v1`)" || api.Service != "abc123-p8080" {
		t.Errorf("api router = %+v", api)
	}
	if !strings.Contains(strings.Join(api.Middlewares, ","), "abc123-noindex") {
		t.Error("indexing disabled must add the noindex middleware")
	}
	if _, ok := d.HTTP.Routers["abc123-d1-https"]; ok {
		t.Error("no https router when HTTPS is off")
	}

	svc := d.HTTP.Services["abc123-p3000"].LoadBalancer.Servers
	if len(svc) != 2 || svc[0].URL != "http://abc123-7:3000" {
		t.Errorf("upstreams = %+v", svc)
	}
}

func TestRenderDynamicEmpty(t *testing.T) {
	if RenderDynamic(AppRoutes{Name: "x", Upstreams: []string{"c"}}) != nil {
		t.Error("no domains -> no file")
	}
	if RenderDynamic(AppRoutes{Name: "x", Domains: []DomainRoute{{Host: "a.com", Port: 80}}}) != nil {
		t.Error("no upstreams -> no file")
	}
}

func TestValidHost(t *testing.T) {
	for _, h := range []string{"example.com", "a.b.example.co.uk", "app.localhost", "x-1.io", "*.apps.example.com"} {
		if !ValidHost(h) {
			t.Errorf("%s should be valid", h)
		}
	}
	for _, h := range []string{"", "exa mple.com", "http://x.com", "-bad.com", "x", "a..b.com", "a.com/path"} {
		if ValidHost(h) {
			t.Errorf("%q should be invalid", h)
		}
	}
}

func TestWildcardRule(t *testing.T) {
	got := ruleFor(DomainRoute{Host: "*.apps.example.com"})
	if got != "HostRegexp(`^[a-z0-9-]+\\.apps\\.example\\.com$`)" {
		t.Errorf("wildcard rule = %s", got)
	}
}
