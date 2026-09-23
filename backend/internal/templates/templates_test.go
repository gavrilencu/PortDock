package templates

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRenderStablePlaceholders(t *testing.T) {
	out := Render("a: ${PORTDOCK_PASSWORD_DB}\nb: ${PORTDOCK_PASSWORD_DB}\nc: ${PORTDOCK_URL}\nd: ${PORTDOCK_FQDN}", "https://n8n.example.com")
	lines := strings.Split(out, "\n")
	if lines[0][3:] != lines[1][3:] {
		t.Error("same placeholder must render the same value")
	}
	if lines[2] != "c: https://n8n.example.com" || lines[3] != "d: n8n.example.com" {
		t.Errorf("unexpected render: %q", out)
	}
	if strings.Contains(out, "${PORTDOCK") {
		t.Error("unrendered placeholder left")
	}
}

func TestServiceTemplatesAreValidYAML(t *testing.T) {
	for slug, tpl := range Services {
		var spec struct {
			X struct {
				Service string `yaml:"service"`
				Port    int    `yaml:"port"`
			} `yaml:"x-portdock"`
			Services map[string]any `yaml:"services"`
		}
		if err := yaml.Unmarshal([]byte(Render(tpl.Compose, "x.example.com")), &spec); err != nil {
			t.Errorf("%s: invalid yaml: %v", slug, err)
			continue
		}
		if _, ok := spec.Services[spec.X.Service]; !ok || spec.X.Port == 0 {
			t.Errorf("%s: x-portdock must reference an existing service and port", slug)
		}
	}
}

func TestEnginesComplete(t *testing.T) {
	for _, e := range EngineList() {
		if e.DefaultImage == "" || e.Port == 0 || e.DataPath == "" || e.health == nil || e.url == nil || e.env == nil {
			t.Errorf("engine %s is incomplete", e.ID)
		}
		if u := e.URL("user", "p@ss", "host", e.Port, "db"); !strings.Contains(u, "host") {
			t.Errorf("engine %s: bad url %s", e.ID, u)
		}
	}
}

func TestImageCatalog(t *testing.T) {
	c := ImageCatalog()
	if len(c.Images) < 100 {
		t.Fatalf("catalog has only %d images", len(c.Images))
	}
	cats := map[string]bool{}
	for _, cat := range c.Categories {
		cats[cat] = true
	}
	seen := map[string]bool{}
	for _, img := range c.Images {
		if seen[img.ID] {
			t.Errorf("duplicate id %q", img.ID)
		}
		seen[img.ID] = true
		if !cats[img.Category] {
			t.Errorf("%s: unknown category %q", img.ID, img.Category)
		}
		if img.Image == "" || img.Port <= 0 || img.Name == "" || img.Description == "" {
			t.Errorf("%s: incomplete entry", img.ID)
		}
		if img.Managed != "" {
			if _, ok := Engines[img.Managed]; !ok {
				t.Errorf("%s: managed engine %q does not exist", img.ID, img.Managed)
			}
		}
		for _, e := range img.Env {
			if e.Key == "" {
				t.Errorf("%s: env without key", img.ID)
			}
		}
	}
}

func TestLegacyPlaceholders(t *testing.T) {
	out := Render("a: ${SHIPYARD_PASSWORD_DB}\nb: ${PORTDOCK_PASSWORD_DB}\nc: ${SHIPYARD_FQDN}", "https://x.example.com")
	if strings.Contains(out, "${") || !strings.Contains(out, "c: x.example.com") {
		t.Fatal(out)
	}
}
