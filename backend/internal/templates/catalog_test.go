package templates

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestBuiltinTemplatesLint(t *testing.T) {
	if len(Services) < 13 {
		t.Fatalf("%d built-in templates", len(Services))
	}
	for slug, tpl := range Services {
		for _, is := range Lint(tpl) {
			if is.Error {
				t.Errorf("%s: %s", slug, is.Text)
			}
		}
		if tpl.Category == "Other" || tpl.Website == "" {
			t.Errorf("%s: category %q website %q", slug, tpl.Category, tpl.Website)
		}
	}
}

const coolifyN8N = `# documentation: https://docs.n8n.io/hosting/
# slogan: n8n is an extendable workflow automation tool.
# category: automation
# tags: n8n,workflow,automation
# logo: svgs/n8n.svg
# port: 5678

services:
  n8n:
    image: docker.n8n.io/n8nio/n8n:1.110.1
    container_name: n8n
    environment:
      - SERVICE_URL_N8N_5678
      - 'N8N_EDITOR_BASE_URL=${SERVICE_URL_N8N}'
      - 'WEBHOOK_URL=${SERVICE_FQDN_N8N}'
      - 'N8N_HOST=${SERVICE_FQDN_N8N}'
      - WS=wss://${SERVICE_FQDN_N8N}/cable
      - 'APP_DOMAIN=${APP_DOMAIN:-$SERVICE_FQDN_N8N}'
      - DB_POSTGRESDB_USER=$SERVICE_USER_POSTGRES
      - 'DB_POSTGRESDB_PASSWORD=${SERVICE_PASSWORD_POSTGRES}'
      - 'N8N_ENCRYPTION_KEY=${SERVICE_PASSWORD_64_ENCRYPTION}'
      - 'JWT=${SERVICE_REALBASE64_64_JWT}'
      - 'TOKEN=${SERVICE_BASE64_128_TOKEN}'
      - 'HEXY=${SERVICE_HEX_32_HEXY}'
      - 'GENERIC_TIMEZONE=${GENERIC_TIMEZONE:-Europe/Berlin}'
      - 'MQ_USER=${SERVICE_USER_RABBITMQ:-plane}'
      - 'BEACON=${DISABLE_BEACON:?false}'
      - 'SMTP=${SMTP_HOST:?}'
      - 'DSN=$SERVICE_USER_PG:$SERVICE_PASSWORD_PG@pg/db'
    volumes:
      - 'n8n-data:/home/node/.n8n'
      - './local-files:/files'
      - type: bind
        source: ./init.sh
        target: /docker-entrypoint-initdb.d/init.sh
        content: |
          #!/bin/sh
          echo ready
    ports:
      - '5678:5678'
  postgresql:
    image: 'postgres:16-alpine'
    exclude_from_hc: true
    healthcheck:
      test: ['CMD', 'node', '-e', 'fetch("http://127.0.0.1:${process.env.PORT||4788}/h")']
    environment:
      POSTGRES_USER: ${SERVICE_USER_POSTGRES}
      POSTGRES_PASSWORD: ${SERVICE_PASSWORD_POSTGRES}
      SERVICE_FQDN_ADMIN_8080:
    volumes:
      - 'postgresql-data:/var/lib/postgresql/data'
`

func TestConvertCoolify(t *testing.T) {
	tpl, err := ConvertCoolify("n8n", coolifyN8N)
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Name != "N8n" || tpl.Service != "n8n" || tpl.Port != 5678 || tpl.Category != "Low-code & Automation" || tpl.Website != "https://docs.n8n.io/hosting/" ||
		strings.Join(tpl.Tags, ",") != "n8n,workflow,automation" || tpl.Logo != "" || !strings.Contains(tpl.Description, "workflow automation") {
		t.Errorf("metadata: %+v", tpl)
	}
	c := tpl.Compose
	for _, want := range []string{"N8N_EDITOR_BASE_URL=${PORTDOCK_URL}", "WEBHOOK_URL=${PORTDOCK_URL}", "N8N_HOST=${PORTDOCK_FQDN}", "WS=wss://${PORTDOCK_FQDN}/cable",
		"APP_DOMAIN=${APP_DOMAIN:-${PORTDOCK_FQDN}}", "DB_POSTGRESDB_USER=${PORTDOCK_USER_POSTGRES}", "MQ_USER=${PORTDOCK_USER_RABBITMQ}'",
		"DSN=${PORTDOCK_USER_PG}:${PORTDOCK_PASSWORD_PG}@pg/db", "BEACON=${DISABLE_BEACON:-false}", "SMTP=${SMTP_HOST:?}", "127.0.0.1:$${process.env.PORT||4788}", "${PORTDOCK_PASSWORD_POSTGRES}", "${PORTDOCK_PASSWORD64_ENCRYPTION}",
		"${PORTDOCK_BASE64x64_JWT}", "${PORTDOCK_RANDOM128_TOKEN}", "${PORTDOCK_HEX32_HEXY}", "POSTGRES_USER: ${PORTDOCK_USER_POSTGRES}", "${GENERIC_TIMEZONE:-Europe/Berlin}",
		"n8n-local-files:/files", "restart: unless-stopped", "content: |"} {
		if !strings.Contains(c, want) {
			t.Errorf("missing %q in\n%s", want, c)
		}
	}
	for _, gone := range []string{"SERVICE_URL_N8N_5678", "container_name", "exclude_from_hc", "./init.sh", "SERVICE_FQDN_ADMIN"} {
		if strings.Contains(c, gone) {
			t.Errorf("%q left in\n%s", gone, c)
		}
	}
	if strings.Join(tpl.Required, ",") != "SMTP_HOST" {
		t.Errorf("required: %v", tpl.Required)
	}
	if !strings.Contains(strings.Join(tpl.Notes, "|"), "postgresql also had a public address") {
		t.Errorf("notes: %v", tpl.Notes)
	}
	var spec struct {
		Configs  map[string]map[string]string `yaml:"configs"`
		Volumes  map[string]any               `yaml:"volumes"`
		Services map[string]struct {
			Configs []map[string]string `yaml:"configs"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(c), &spec); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(spec.Configs["n8n-init-sh"]["content"], "echo ready") || spec.Services["n8n"].Configs[0]["target"] != "/docker-entrypoint-initdb.d/init.sh" {
		t.Errorf("configs: %+v %+v", spec.Configs, spec.Services["n8n"].Configs)
	}
	if _, ok := spec.Volumes["n8n-local-files"]; !ok || spec.Volumes["n8n-data"] == nil && !hasKey(spec.Volumes, "n8n-data") || !hasKey(spec.Volumes, "postgresql-data") {
		t.Errorf("volumes: %v", spec.Volumes)
	}
	for _, is := range Lint(tpl) {
		if is.Error {
			t.Errorf("lint: %s", is.Text)
		}
	}
	out := Render(c, "flows.example.com")
	if strings.Contains(out, "${PORTDOCK") || !strings.Contains(out, "N8N_HOST=flows.example.com") {
		t.Errorf("render:\n%s", out)
	}
	if bg, err := ConvertCoolify("runner", "services:\n  runner:\n    image: gitea/act_runner:0.2\n"); err != nil || bg.Service != "" || bg.Port != 0 || !strings.Contains(strings.Join(bg.Notes, ""), "without a domain") {
		t.Errorf("background service: %+v %v", bg, err)
	}
}

func TestParseCatalog(t *testing.T) {
	coolify, _ := json.Marshal(map[string]CoolifyEntry{
		"n8n":    {Documentation: "https://n8n.io?utm_source=coolify.io", Slogan: "Workflows.", Compose: base64.StdEncoding.EncodeToString([]byte(coolifyN8N)), Tags: []string{"automation"}, Category: "automation", Logo: "svgs/n8n.svg", Port: "5678"},
		"broken": {Compose: "!!!"},
		"nopub":  {Compose: base64.StdEncoding.EncodeToString([]byte("services:\n  a:\n    image: x\n"))},
	})
	tpls, skipped, err := ParseCatalog(FormatAuto, coolify, "https://raw.githubusercontent.com/coollabsio/coolify/v4.x/templates/service-templates.json")
	if err != nil || len(tpls) != 2 || len(skipped) != 1 {
		t.Fatalf("coolify: %v %v %v", tpls, skipped, err)
	}
	if tpls[0].Slug != "n8n" || tpls[1].Slug != "nopub" || tpls[1].Service != "" {
		t.Errorf("coolify: %+v", tpls)
	}
	if tpls[0].Website != "https://n8n.io" || tpls[0].Logo != "https://raw.githubusercontent.com/coollabsio/coolify/v4.x/public/svgs/n8n.svg" || tpls[0].Description != "Workflows." {
		t.Errorf("coolify entry: %+v", tpls[0])
	}

	items := []CatalogItem{{Slug: "uptime-kuma", Compose: Services["uptime-kuma"].Compose}, {Slug: "Bad Slug", Compose: "x"}}
	doc, _ := Bundle("acme", items)
	tpls, skipped, err = ParseCatalog(FormatAuto, doc, "https://catalog.acme.internal/portdock.json")
	if err != nil || len(tpls) != 1 || tpls[0].Name != "Uptime Kuma" || len(skipped) != 1 {
		t.Fatalf("portdock: %v %v %v", tpls, skipped, err)
	}
	yamlDoc := "version: 1\ntemplates:\n  - slug: kuma\n    compose: |\n" + indent(Services["uptime-kuma"].Compose, "      ")
	if tpls, _, err := ParseCatalog(FormatPortDock, []byte(yamlDoc), ""); err != nil || len(tpls) != 1 || tpls[0].Slug != "kuma" {
		t.Errorf("yaml catalog: %v %v", tpls, err)
	}
	if tpls, _, err := ParseCatalog(FormatAuto, []byte(Services["grafana"].Compose), "https://git.acme.internal/raw/templates/grafana.yml?ref=main"); err != nil || len(tpls) != 1 || tpls[0].Slug != "grafana" {
		t.Errorf("single template: %v %v", tpls, err)
	}
	if _, _, err := ParseCatalog(FormatAuto, []byte(`{"version": 2, "templates": [{"slug": "x", "compose": "y"}]}`), ""); err == nil {
		t.Error("future version accepted")
	}
}

func hasKey(m map[string]any, k string) bool {
	_, ok := m[k]
	return ok
}

func indent(s, pre string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = pre + lines[i]
	}
	return strings.Join(lines, "\n") + "\n"
}

// TestCoolifyCatalogFile converts a downloaded copy of Coolify's catalog:
// PORTDOCK_COOLIFY_JSON=service-templates.json go test ./internal/templates -run CoolifyCatalogFile -v
func TestCoolifyCatalogFile(t *testing.T) {
	file := os.Getenv("PORTDOCK_COOLIFY_JSON")
	if file == "" {
		t.Skip("set PORTDOCK_COOLIFY_JSON")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	tpls, skipped, err := ParseCatalog(FormatAuto, data, "https://raw.githubusercontent.com/coollabsio/coolify/v4.x/templates/service-templates.json")
	if err != nil {
		t.Fatal(err)
	}
	leftover := map[string]int{}
	for _, tpl := range tpls {
		for _, m := range regexp.MustCompile(`\$\{?SERVICE_[A-Z]+`).FindAllString(tpl.Compose, -1) {
			leftover[strings.TrimLeft(m, "${")]++
		}
	}
	t.Logf("Coolify variables left: %v", leftover)
	clean, hostAccess := 0, 0
	reasons := map[string]int{}
	for _, tpl := range tpls {
		ok := true
		for _, is := range Lint(tpl) {
			if is.Error {
				ok = false
				key := is.Text
				if i := strings.IndexAny(key, ":("); i > 0 {
					key = key[:i]
				}
				reasons[key]++
				if strings.HasPrefix(is.Text, "needs host access") {
					hostAccess++
				}
			}
		}
		if ok {
			clean++
		}
	}
	t.Logf("%d converted (%d without lint errors, %d need host access), %d skipped", len(tpls), clean, hostAccess, len(skipped))
	for r, n := range reasons {
		t.Logf("  lint %4d × %s", n, r)
	}
	for i, s := range skipped {
		if i < 15 {
			t.Logf("  skipped %s: %s", s.Slug, s.Reason)
		}
	}
}

// TestCommunityCatalog keeps services/catalog.json, the catalog instances
// download from the repository, in step with the template files:
// PORTDOCK_UPDATE_CATALOG=1 go test ./internal/templates -run CommunityCatalog
func TestCommunityCatalog(t *testing.T) {
	files, _ := builtin.ReadDir("services")
	var items []CatalogItem
	for _, f := range files {
		b, _ := builtin.ReadFile("services/" + f.Name())
		items = append(items, CatalogItem{Slug: strings.TrimSuffix(f.Name(), ".yml"), Compose: string(b)})
	}
	want, err := Bundle("portdock", items)
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	if os.Getenv("PORTDOCK_UPDATE_CATALOG") == "1" {
		if err := os.WriteFile("services/catalog.json", want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile("services/catalog.json")
	if err != nil || strings.ReplaceAll(string(got), "\r\n", "\n") != string(want) {
		t.Fatalf("services/catalog.json is not up to date (%v): run PORTDOCK_UPDATE_CATALOG=1 go test ./internal/templates -run CommunityCatalog", err)
	}
	tpls, skipped, err := ParseCatalog(FormatAuto, got, "")
	if err != nil || len(tpls) != len(Services) || len(skipped) != 0 {
		t.Errorf("catalog.json: %d templates, %v %v", len(tpls), skipped, err)
	}
}
