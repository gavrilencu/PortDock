package proxy

import (
	"strings"
	"testing"
)

func TestLabelsHTTPS(t *testing.T) {
	l := Labels(Route{Name: "Abc123", Domains: []string{"https://app.example.com", "www.example.com/api/"}, Port: 3000, HTTPS: true, Network: "shipyard"})
	if l["traefik.enable"] != "true" {
		t.Fatal("traefik not enabled")
	}
	rule := l["traefik.http.routers.abc123-https.rule"]
	if rule != "Host(`app.example.com`) || (Host(`www.example.com`) && PathPrefix(`/api`))" {
		t.Errorf("unexpected rule %q", rule)
	}
	if l["traefik.http.routers.abc123-https.tls.certresolver"] != CertResolver {
		t.Error("missing cert resolver")
	}
	if l["traefik.http.routers.abc123-http.middlewares"] != "abc123-redirect" {
		t.Error("http router should redirect to https")
	}
	if l["traefik.http.services.abc123.loadbalancer.server.port"] != "3000" {
		t.Error("wrong port")
	}
}

func TestLabelsEmpty(t *testing.T) {
	if len(Labels(Route{Name: "x", Port: 80})) != 0 {
		t.Error("no domains should produce no labels")
	}
}

func TestLabelArgsQuoted(t *testing.T) {
	args := LabelArgs(map[string]string{"traefik.http.routers.x.rule": "Host(`a.b`)"})
	if !strings.Contains(args, "--label 'traefik.http.routers.x.rule=Host(`a.b`)'") {
		t.Errorf("label not shell-quoted: %s", args)
	}
}
