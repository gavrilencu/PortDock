package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/portdock/portdock/internal/store"
)

func TestImportFromCoolify(t *testing.T) {
	routes := map[string]any{
		"/api/v1/projects":                 []map[string]any{{"uuid": "p1", "name": "Shop"}},
		"/api/v1/projects/p1/environments": []map[string]any{{"id": 7, "name": "production"}, {"id": 8, "name": "staging"}},
		"/api/v1/applications": []map[string]any{{"uuid": "a1", "name": "web", "environment_id": 7, "git_repository": "acme/web", "git_branch": "main",
			"build_pack": "nixpacks", "ports_exposes": "3000", "fqdn": "https://shop.example.com", "health_check_enabled": true, "health_check_path": "/up"}},
		"/api/v1/applications/a1/envs": []map[string]any{{"key": "NODE_ENV", "real_value": "production"}, {"key": "STRIPE_SECRET", "real_value": "sk_live_123"}},
		"/api/v1/applications/a1/storages":        map[string]any{"persistent_storages": []map[string]any{{"name": "a1-uploads", "mount_path": "/app/uploads"}}},
		"/api/v1/applications/a1/scheduled-tasks": []map[string]any{{"name": "nightly", "command": "npm run report", "frequency": "daily", "enabled": true}},
		"/api/v1/databases":                       []map[string]any{{"uuid": "d1", "name": "shop-db", "environment_id": 7, "database_type": "standalone-postgresql", "image": "postgres:16-alpine"}},
		"/api/v1/services": []map[string]any{{"uuid": "s1", "name": "automation", "service_type": "n8n", "environment_id": 8,
			"docker_compose_raw": "services:\n  n8n:\n    image: n8nio/n8n:1.110.1\n    environment:\n      - SERVICE_URL_N8N_5678\n      - N8N_HOST=${SERVICE_FQDN_N8N}\n      - SMTP_HOST=${SMTP_HOST}\n"}},
		"/api/v1/services/s1/applications": []map[string]any{{"name": "n8n", "fqdn": "https://flows.example.com"}},
		"/api/v1/services/s1/envs":         []map[string]any{{"key": "SMTP_HOST", "real_value": "smtp.example.com"}},
	}
	coolify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer coolify-token" {
			w.WriteHeader(401)
			return
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(body)
	}))
	defer coolify.Close()

	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("dev@corp.com", "correct-horse", "member")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status, is_local) VALUES (1, 'local', 'localhost', 'reachable', 1)"); err != nil {
		t.Fatal(err)
	}
	owner, dev := e.login("owner@corp.com"), e.login("dev@corp.com")
	src := map[string]any{"source": "coolify", "url": coolify.URL, "token": "coolify-token"}

	e.expect(dev, "POST", "/api/migrations/plan", src, 403)
	e.expect(owner, "POST", "/api/migrations/plan", map[string]any{"source": "coolify", "url": coolify.URL, "token": "wrong"}, 400)
	plan := e.expect(owner, "POST", "/api/migrations/plan", src, 200)
	apps := plan["apps"].([]any)
	if len(apps) != 1 || len(plan["databases"].([]any)) != 1 || len(plan["services"].([]any)) != 1 {
		t.Fatalf("plan: %v", plan)
	}
	if b, _ := json.Marshal(plan); strings.Contains(string(b), "sk_live_123") {
		t.Error("a secret value is in the plan")
	}

	body := map[string]any{"source": "coolify", "url": coolify.URL, "token": "coolify-token", "target_server_id": 1, "deploy": false,
		"items": []map[string]any{{"kind": "app", "ref": "a1"}, {"kind": "database", "ref": "d1"}, {"kind": "service", "ref": "s1"}, {"kind": "app", "ref": "gone"}}}
	res := e.expect(owner, "POST", "/api/migrations/apply", body, 200)["results"].([]any)
	if len(res) != 4 {
		t.Fatalf("results: %v", res)
	}
	for i, want := range []string{"", "", "", "not found in the source"} {
		if got, _ := res[i].(map[string]any)["error"].(string); got != want {
			t.Errorf("result %d: %v", i, res[i])
		}
	}
	appID := int64(res[0].(map[string]any)["id"].(float64))
	app, err := store.ByID[store.Application](t.Context(), e.db, "applications", appID)
	if err != nil {
		t.Fatal(err)
	}
	if app.GitRepository != "https://github.com/acme/web" || app.BuildPack != "nixpacks" || app.Port != 3000 || app.HealthCheckPath != "/up" {
		t.Errorf("app: %+v", app)
	}
	env, _ := store.ByID[store.Environment](t.Context(), e.db, "environments", app.EnvironmentID)
	project, _ := store.ByID[store.Project](t.Context(), e.db, "projects", env.ProjectID)
	if project.Name != "Shop" || env.Name != "production" {
		t.Errorf("placed in %s/%s", project.Name, env.Name)
	}
	vars := e.expectList(owner, "GET", fmt.Sprintf("/api/applications/%d/envs", appID))
	secret := false
	for _, v := range vars {
		if v["key"] == "STRIPE_SECRET" && v["value"] == "sk_live_123" && v["is_secret"] == true {
			secret = true
		}
	}
	if len(vars) != 2 || !secret {
		t.Errorf("vars: %v", vars)
	}
	var n int
	_ = e.db.QueryRow("SELECT COUNT(*) FROM app_domains WHERE application_id = ? AND host = 'shop.example.com'", appID).Scan(&n)
	var vols, jobs int
	_ = e.db.QueryRow("SELECT COUNT(*) FROM app_volumes WHERE application_id = ? AND mount_path = '/app/uploads'", appID).Scan(&vols)
	_ = e.db.QueryRow("SELECT COUNT(*) FROM app_jobs WHERE application_id = ? AND schedule = '0 0 * * *'", appID).Scan(&jobs)
	if n != 1 || vols != 1 || jobs != 1 {
		t.Errorf("domains %d volumes %d jobs %d", n, vols, jobs)
	}
	var engine, image string
	_ = e.db.QueryRow("SELECT engine, image FROM databases WHERE id = ?", int64(res[1].(map[string]any)["id"].(float64))).Scan(&engine, &image)
	if engine != "postgres" || image != "postgres:16-alpine" {
		t.Errorf("database: %s %s", engine, image)
	}
	svcID := int64(res[2].(map[string]any)["id"].(float64))
	svc, _ := store.ByID[store.Service](t.Context(), e.db, "services", svcID)
	senv, _ := store.ByID[store.Environment](t.Context(), e.db, "environments", svc.EnvironmentID)
	if !strings.Contains(svc.Compose, "N8N_HOST=flows.example.com") || svc.Domain != "https://flows.example.com" || senv.Name != "staging" || senv.ProjectID != project.ID {
		t.Errorf("service: %+v in %s", svc, senv.Name)
	}
	if v := e.expectList(owner, "GET", fmt.Sprintf("/api/services/%d/envs", svcID)); len(v) != 1 || v[0]["value"] != "smtp.example.com" {
		t.Errorf("service vars: %v", v)
	}

	// Again, into another project: names are free there.
	body["items"] = []map[string]any{{"kind": "app", "ref": "a1", "project": "Shop copy", "environment": "test"}}
	res = e.expect(owner, "POST", "/api/migrations/apply", body, 200)["results"].([]any)
	r0 := res[0].(map[string]any)
	if r0["error"] != nil || !strings.Contains(fmt.Sprint(r0["notes"]), "domains not added") {
		t.Errorf("second import: %v", r0)
	}
}
