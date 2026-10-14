package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/portdock/portdock/internal/templates"
)

func TestServiceCatalogs(t *testing.T) {
	var broken atomic.Bool
	bundle, _ := templates.Bundle("acme", []templates.CatalogItem{{Slug: "kuma", Compose: templates.Services["uptime-kuma"].Compose}, {Slug: "Nope!", Compose: "x"}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if broken.Load() {
			w.WriteHeader(502)
			return
		}
		w.Write(bundle)
	}))
	defer srv.Close()

	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("dev@corp.com", "correct-horse", "member")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status, is_local) VALUES (1, 'local', 'localhost', 'reachable', 1)"); err != nil {
		t.Fatal(err)
	}
	owner, dev := e.login("owner@corp.com"), e.login("dev@corp.com")
	p := e.expect(owner, "POST", "/api/projects", map[string]any{"name": "shop", "default_role": "developer"}, 200)
	env := p["environments"].([]any)[0].(map[string]any)["id"]

	// Built-in templates come first, without their compose files.
	list := e.expectList(dev, "GET", "/api/service-templates")
	if len(list) != len(templates.Services) || list[0]["key"] == "" || list[0]["compose"] != nil {
		t.Fatalf("built-in list: %d %v", len(list), list[0])
	}

	e.expect(dev, "POST", "/api/template-catalogs", map[string]any{"name": "acme", "url": srv.URL}, 403)
	e.expect(owner, "POST", "/api/template-catalogs", map[string]any{"name": "Acme Corp", "url": srv.URL}, 400)
	e.expect(owner, "POST", "/api/template-catalogs", map[string]any{"name": "acme"}, 400)
	acme := e.expect(owner, "POST", "/api/template-catalogs", map[string]any{"name": "acme", "url": srv.URL}, 200)
	if acme["count"] != float64(1) || len(acme["skipped"].([]any)) != 1 || acme["fetch_error"] != "" {
		t.Errorf("acme: %v", acme)
	}
	n8n := "# documentation: https://n8n.io\n# slogan: Workflows.\n# category: automation\n# port: 5678\nservices:\n  n8n:\n    image: n8nio/n8n:1.110.1\n    environment:\n      - SERVICE_URL_N8N_5678\n      - N8N_HOST=${SERVICE_FQDN_N8N}\n      - DB_PASSWORD=${SERVICE_PASSWORD_DB}\n"
	sock := "# port: 9000\nservices:\n  portainer:\n    image: portainer/portainer-ce:2.21.0\n    environment:\n      - SERVICE_URL_PORTAINER_9000\n    volumes:\n      - /var/run/docker.sock:/var/run/docker.sock\n"
	doc, _ := json.Marshal(map[string]templates.CoolifyEntry{
		"n8n":       {Compose: base64.StdEncoding.EncodeToString([]byte(n8n)), Slogan: "Workflows.", Category: "automation", Port: "5678"},
		"portainer": {Compose: base64.StdEncoding.EncodeToString([]byte(sock)), Slogan: "Docker UI.", Port: "9000"},
	})
	cool := e.expect(owner, "POST", "/api/template-catalogs", map[string]any{"name": "coolify", "content": string(doc)}, 200)
	if cool["count"] != float64(2) || cool["upload"] != true || cool["format"] != "auto" {
		t.Errorf("coolify: %v", cool)
	}

	list = e.expectList(dev, "GET", "/api/service-templates")
	keys := map[string]map[string]any{}
	for _, l := range list {
		keys[l["key"].(string)] = l
	}
	if keys["acme/kuma"]["source"] != "acme" || keys["coolify/n8n"]["name"] != "N8n" || keys["coolify/portainer"]["host_access"] != true || keys["coolify/n8n"]["host_access"] != nil {
		t.Errorf("merged list: %v %v %v", keys["acme/kuma"], keys["coolify/n8n"], keys["coolify/portainer"])
	}
	detail := e.expect(dev, "GET", "/api/service-templates/coolify/n8n", nil, 200)
	if c := detail["template"].(map[string]any)["compose"].(string); !strings.Contains(c, "N8N_HOST=${PORTDOCK_FQDN}") || !strings.Contains(c, "${PORTDOCK_PASSWORD_DB}") {
		t.Errorf("detail: %v", detail)
	}
	e.expect(dev, "GET", "/api/service-templates/coolify/nope", nil, 404)

	// A service from a catalog template: placeholders rendered for its domain.
	svc := e.expect(dev, "POST", "/api/services", map[string]any{"environment_id": env, "server_id": 1, "template": "coolify/n8n", "domain": "flows.example.com", "deploy": false}, 200)
	if c := svc["compose"].(string); svc["name"] != "N8n" || svc["template"] != "coolify/n8n" || !strings.Contains(c, "N8N_HOST=flows.example.com") || strings.Contains(c, "${PORTDOCK") {
		t.Errorf("service: %v", svc)
	}
	// One that needs the Docker socket is refused, even with host access.
	e.expect(dev, "POST", "/api/services", map[string]any{"environment_id": env, "server_id": 1, "template": "coolify/portainer", "deploy": false}, 400)
	e.expect(owner, "POST", "/api/services", map[string]any{"environment_id": env, "server_id": 1, "template": "coolify/portainer", "host_access": true, "deploy": false}, 400)

	// A failed refresh keeps the templates.
	broken.Store(true)
	id := int64(acme["id"].(float64))
	e.expect(owner, "POST", fmt.Sprintf("/api/template-catalogs/%d/refresh", id), nil, 400)
	cats := e.expectList(owner, "GET", "/api/template-catalogs")
	for _, c := range cats {
		if c["name"] == "acme" && (c["count"] != float64(1) || !strings.Contains(c["fetch_error"].(string), "HTTP 502")) {
			t.Errorf("after a failed refresh: %v", c)
		}
	}
	// Disabled catalogs leave the picker.
	e.expect(owner, "PATCH", fmt.Sprintf("/api/template-catalogs/%d", id), map[string]any{"enabled": false}, 200)
	if len(e.expectList(dev, "GET", "/api/service-templates")) != len(templates.Services)+2 {
		t.Error("a disabled catalog is listed")
	}
	e.expect(dev, "POST", "/api/services", map[string]any{"environment_id": env, "server_id": 1, "template": "acme/kuma", "deploy": false}, 400)
	e.expect(owner, "DELETE", fmt.Sprintf("/api/template-catalogs/%d", id), nil, 200)
}
