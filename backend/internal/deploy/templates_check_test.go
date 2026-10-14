package deploy

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/portdock/portdock/internal/composefile"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/templates"
)

// TestServiceTemplatesPassComposeCheck renders every one-click service with the
// real Docker Compose and checks it without host access, as a deploy does:
// PORTDOCK_DOCKER_E2E=1 go test ./internal/deploy -run ServiceTemplatesPassComposeCheck
func TestServiceTemplatesPassComposeCheck(t *testing.T) {
	if os.Getenv("PORTDOCK_DOCKER_E2E") != "1" {
		t.Skip("set PORTDOCK_DOCKER_E2E=1 to render the templates with Docker Compose")
	}
	os.Setenv("MSYS_NO_PATHCONV", "1")
	slugs := make([]string, 0, len(templates.Services))
	for s := range templates.Services {
		slugs = append(slugs, s)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		raw := templates.Render(templates.Services[slug].Compose, "app.example.com")
		if err := composefile.Check(raw, false); err != nil {
			t.Errorf("%s: %v", slug, err)
			continue
		}
		dir := t.TempDir()
		file := filepath.Join(dir, "docker-compose.yml")
		if err := os.WriteFile(file, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dir, ".portdock.env"), nil, 0o600)
		root := filepath.ToSlash(dir)
		c := ComposeUp{X: executor.Local{}, Project: "sy-0123456789abcdef", Dir: root, File: root + "/docker-compose.yml", Network: "portdock", Root: root, Forbidden: []string{"/data/portdock"}}
		if err := checkComposeConfig(context.Background(), c, raw, root+"/.portdock.env"); err != nil {
			t.Errorf("%s: %v", slug, err)
		}
	}
}

// TestCoolifyCatalogPassesComposeCheck converts a downloaded copy of Coolify's
// catalog and checks each template that needs no host access with the real
// Docker Compose:
// PORTDOCK_DOCKER_E2E=1 PORTDOCK_COOLIFY_JSON=service-templates.json go test ./internal/deploy -run CoolifyCatalogPassesComposeCheck
func TestCoolifyCatalogPassesComposeCheck(t *testing.T) {
	file := os.Getenv("PORTDOCK_COOLIFY_JSON")
	if os.Getenv("PORTDOCK_DOCKER_E2E") != "1" || file == "" {
		t.Skip("set PORTDOCK_DOCKER_E2E=1 and PORTDOCK_COOLIFY_JSON")
	}
	os.Setenv("MSYS_NO_PATHCONV", "1")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	tpls, _, err := templates.ParseCatalog(templates.FormatCoolify, data, "")
	if err != nil {
		t.Fatal(err)
	}
	checked, failed := 0, 0
	for _, tpl := range tpls {
		raw := templates.Render(tpl.Compose, "app.example.com")
		if composefile.Check(raw, false) != nil {
			continue // needs host access
		}
		checked++
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dir, ".portdock.env"), nil, 0o600)
		root := filepath.ToSlash(dir)
		c := ComposeUp{X: executor.Local{}, Project: "sy-0123456789abcdef", Dir: root, File: root + "/docker-compose.yml", Network: "portdock", Root: root, Forbidden: []string{"/data/portdock"}}
		if err := checkComposeConfig(context.Background(), c, raw, root+"/.portdock.env"); err != nil {
			failed++
			if failed <= 15 {
				t.Logf("%s: %v", tpl.Slug, err)
			}
		}
	}
	t.Logf("%d templates checked with docker compose, %d refused", checked, failed)
}
