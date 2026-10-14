package deploy

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/portdock/portdock/internal/proxy"
)

func TestParseTraefikCounts(t *testing.T) {
	c := parseTraefikCounts(`# HELP traefik_service_requests_total How many HTTP requests processed on a service, partitioned by status code, protocol, and method.
# TYPE traefik_service_requests_total counter
traefik_service_requests_total{code="200",method="GET",protocol="http",service="app-p80-canary@file"} 40
traefik_service_requests_total{code="502",method="GET",protocol="http",service="app-p80-canary@file"} 6
traefik_service_requests_total{code="500",method="POST",protocol="http",service="app-p80-canary@file"} 4
traefik_service_requests_total{code="200",method="GET",protocol="http",service="app-p80-stable@file"} 100
traefik_service_requests_total{code="404",method="GET",protocol="http",service="app-p80-stable@file"} 3
traefik_service_request_duration_seconds_count{code="200",method="GET",protocol="http",service="app-p80-stable@file"} 100
`)
	if got := c["app-p80-canary"]; got.total != 50 || got.errors != 10 || rate(got) != 20 {
		t.Errorf("canary: %+v", got)
	}
	if got := c["app-p80-stable"]; got.total != 103 || got.errors != 0 {
		t.Errorf("stable: %+v", got)
	}
}

func TestRenderCanary(t *testing.T) {
	out := proxy.RenderDynamic(proxy.AppRoutes{
		Name: "abc", Upstreams: []string{"abc-1"},
		Domains: []proxy.DomainRoute{{Host: "app.example.com", Port: 80, Indexing: true}},
		Canary:  &proxy.Canary{Upstreams: []string{"abc-2"}, Percent: 10, Tag: "7"},
	})
	var doc struct {
		HTTP struct {
			Services map[string]map[string]any `yaml:"services"`
			Routers  map[string]struct {
				Service string `yaml:"service"`
			} `yaml:"routers"`
		} `yaml:"http"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	weighted, stable, canary := proxy.CanaryService("abc", 80, "7")
	if canary != "abc-p80-canary-7" {
		t.Errorf("canary service %s", canary)
	}
	w := doc.HTTP.Services[weighted]["weighted"].(map[string]any)["services"].([]any)
	if len(w) != 2 || w[0].(map[string]any)["weight"] != 90 || w[1].(map[string]any)["name"] != canary {
		t.Errorf("weighted service: %v", w)
	}
	if !strings.Contains(string(out), "http://abc-2:80") || doc.HTTP.Services[stable] == nil {
		t.Errorf("halves:\n%s", out)
	}
	for _, r := range doc.HTTP.Routers {
		if r.Service != weighted {
			t.Errorf("router points to %s", r.Service)
		}
	}
	// Without a canary the service is a plain load balancer.
	plain := proxy.RenderDynamic(proxy.AppRoutes{Name: "abc", Upstreams: []string{"abc-1"}, Domains: []proxy.DomainRoute{{Host: "app.example.com", Port: 80}}})
	if strings.Contains(string(plain), "weighted") {
		t.Error("weighted service without a canary")
	}
}
