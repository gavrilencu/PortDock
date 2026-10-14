package proxy

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRenderDynamicProtection(t *testing.T) {
	r := AppRoutes{
		Name: "app1", Upstreams: []string{"app1-1"},
		Domains: []DomainRoute{{Host: "admin.example.com", Port: 3000, HTTPS: true, Indexing: true}},
		Protection: Protection{
			IPAllow: []string{"10.0.0.0/8", "203.0.113.7"}, IPDepth: 1,
			RateAverage: 20, RateBurst: 50,
			ForwardAuth: "http://portdock-dashboard:8000/api/forward-auth/app1",
		},
	}
	var cfg struct {
		HTTP struct {
			Routers map[string]struct {
				Middlewares []string `yaml:"middlewares"`
			} `yaml:"routers"`
			Middlewares map[string]map[string]map[string]any `yaml:"middlewares"`
		} `yaml:"http"`
	}
	if err := yaml.Unmarshal(RenderDynamic(r), &cfg); err != nil {
		t.Fatal(err)
	}
	chain := cfg.HTTP.Routers["app1-d0-https"].Middlewares
	want := []string{"app1-ipallow", "app1-ratelimit", "app1-sso", "app1-gzip"}
	if len(chain) != len(want) {
		t.Fatalf("chain %v", chain)
	}
	for i := range want {
		if chain[i] != want[i] {
			t.Fatalf("chain %v, want %v", chain, want)
		}
	}
	// The HTTP router only redirects to HTTPS: nothing to protect there.
	if c := cfg.HTTP.Routers["app1-d0-http"].Middlewares; len(c) != 1 || c[0] != "app1-https" {
		t.Fatalf("http router %v", c)
	}
	m := cfg.HTTP.Middlewares
	if ip := m["app1-ipallow"]["ipAllowList"]; len(ip["sourceRange"].([]any)) != 2 || ip["ipStrategy"].(map[string]any)["depth"] != 1 {
		t.Fatalf("ipAllowList %v", ip)
	}
	if rl := m["app1-ratelimit"]["rateLimit"]; rl["average"] != 20 || rl["burst"] != 50 || rl["period"] != "1s" ||
		rl["sourceCriterion"].(map[string]any)["ipStrategy"].(map[string]any)["depth"] != 1 {
		t.Fatalf("rateLimit %v", rl)
	}
	if fa := m["app1-sso"]["forwardAuth"]; fa["address"] != r.Protection.ForwardAuth || len(fa["authResponseHeaders"].([]any)) != 3 {
		t.Fatalf("forwardAuth %v", fa)
	}

	// Without a forwarded-address depth, no sourceCriterion at all: Traefik
	// refuses an empty one and would keep serving the previous routes.
	r.Protection = Protection{RateAverage: 5}
	if out := string(RenderDynamic(r)); strings.Contains(out, "sourceCriterion") || strings.Contains(out, "ipStrategy") {
		t.Fatalf("empty source criterion rendered:\n%s", out)
	}

	// A password prompt instead of single sign-on.
	r.Protection = Protection{BasicUsers: []string{"ops:$2a$10$abcdefghijklmnopqrstuuJ0x5q5jF3cO1A1VpI3XzQ2y0QfJbG6"}}
	if err := yaml.Unmarshal(RenderDynamic(r), &cfg); err != nil {
		t.Fatal(err)
	}
	if ba := cfg.HTTP.Middlewares["app1-auth"]["basicAuth"]; len(ba["users"].([]any)) != 1 || ba["removeHeader"] != true {
		t.Fatalf("basicAuth %v", ba)
	}
	if chain := cfg.HTTP.Routers["app1-d0-https"].Middlewares; chain[0] != "app1-auth" {
		t.Fatalf("chain %v", chain)
	}
}
