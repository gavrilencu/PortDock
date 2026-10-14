package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveJSON(t *testing.T, auth string, routes map[string]any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != auth {
			w.WriteHeader(401)
			w.Write([]byte(`{"message":"Unauthenticated."}`))
			return
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			w.Write([]byte(`{"message":"Not found."}`))
			return
		}
		json.NewEncoder(w).Encode(body)
	}))
}

const n8nCompose = "services:\n  n8n:\n    image: docker.n8n.io/n8nio/n8n\n    environment:\n      - SERVICE_URL_N8N_5678\n      - N8N_HOST=${SERVICE_FQDN_N8N}\n      - DB_PASSWORD=${SERVICE_PASSWORD_DB}\n      - SMTP_HOST=${SMTP_HOST}\n"

func TestCoolify(t *testing.T) {
	srv := serveJSON(t, "Bearer tok", map[string]any{
		"/api/v1/projects":                 []map[string]any{{"uuid": "p1", "name": "Shop"}},
		"/api/v1/projects/p1/environments": []map[string]any{{"id": 7, "name": "production"}, {"id": 8, "name": "staging"}},
		"/api/v1/applications": []map[string]any{
			{"uuid": "a1", "name": "Web Front", "environment_id": 7, "git_repository": "acme/web", "git_branch": "main", "git_commit_sha": "HEAD",
				"build_pack": "nixpacks", "ports_exposes": "3000,9000", "fqdn": "https://shop.example.com,https://www.shop.example.com", "base_directory": "/",
				"health_check_enabled": true, "health_check_path": "/healthz", "limits_memory": "512m", "limits_cpus": "0", "is_http_basic_auth_enabled": true,
				"pre_deployment_command": "npm run migrate"},
			{"uuid": "a2", "name": "api", "environment_id": 8, "git_repository": "git@gitlab.example.com:acme/api.git", "build_pack": "dockerfile",
				"dockerfile_location": "/docker/Dockerfile", "ports_exposes": "8080"},
			{"uuid": "a3", "name": "proxy", "environment_id": 7, "build_pack": "dockerimage", "docker_registry_image_name": "nginx", "docker_registry_image_tag": "1.27-alpine", "ports_exposes": "80"},
		},
		"/api/v1/applications/a1/envs": []map[string]any{
			{"key": "NODE_ENV", "value": "production", "real_value": "production", "is_buildtime": true},
			{"key": "STRIPE_SECRET", "value": "sk_live_x", "real_value": "sk_live_x"},
			{"key": "API_URL", "value": "{{project.API}}", "real_value": "https://api.example.com"},
			{"key": "NODE_ENV", "value": "development", "is_preview": true},
		},
		"/api/v1/applications/a1/storages": map[string]any{"persistent_storages": []map[string]any{{"name": "a1-uploads", "mount_path": "/app/uploads"}},
			"file_storages": []map[string]any{{"mount_path": "/app/config.json"}}},
		"/api/v1/applications/a1/scheduled-tasks": []map[string]any{{"name": "cleanup", "command": "npm run cleanup", "frequency": "daily", "enabled": true},
			{"name": "off", "command": "x", "frequency": "hourly", "enabled": false}},
		"/api/v1/databases": []map[string]any{{"uuid": "d1", "name": "shop-db", "environment_id": 7, "database_type": "standalone-postgresql", "image": "postgres:16-alpine"},
			{"uuid": "d2", "name": "cache", "environment_id": 7, "image": "eqalpha/keydb:latest"}, {"uuid": "d3", "name": "x", "image": "oracle:23"}},
		"/api/v1/services":                 []map[string]any{{"uuid": "s1", "name": "automation", "service_type": "n8n", "environment_id": 8, "docker_compose_raw": n8nCompose}},
		"/api/v1/services/s1/applications": []map[string]any{{"name": "n8n", "fqdn": "https://flows.example.com"}},
		"/api/v1/services/s1/envs":         []map[string]any{{"key": "SERVICE_PASSWORD_DB", "value": "old"}, {"key": "SMTP_HOST", "value": "smtp.example.com"}},
	})
	defer srv.Close()

	if _, err := Read(context.Background(), Coolify, Credentials{URL: srv.URL, Token: "nope"}); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("bad token: %v", err)
	}
	p, err := Read(context.Background(), Coolify, Credentials{URL: srv.URL + "/api/v1/", Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Apps) != 3 || len(p.Databases) != 2 || len(p.Services) != 1 || len(p.Warnings) != 1 {
		t.Fatalf("plan: %d apps %d dbs %d services, warnings %v", len(p.Apps), len(p.Databases), len(p.Services), p.Warnings)
	}
	web := p.Apps[0]
	if web.Name != "web-front" || web.Project != "Shop" || web.Environment != "production" || web.GitRepo != "https://github.com/acme/web" || web.GitCommit != "" ||
		web.BuildPack != "nixpacks" || web.Port != 3000 || len(web.Domains) != 2 || web.HealthPath != "/healthz" || web.Memory != "512m" || web.CPU != "" ||
		web.PreDeploy != "npm run migrate" || len(web.Volumes) != 1 || web.Volumes[0].Name != "uploads" || len(web.Jobs) != 1 || web.Jobs[0].Schedule != "0 0 * * *" {
		t.Errorf("web: %+v", web)
	}
	vars := map[string]Var{}
	for _, v := range web.Env {
		vars[v.Key+map[bool]string{true: "@preview"}[v.Preview]] = v
	}
	if !vars["NODE_ENV"].Build || vars["NODE_ENV@preview"].Value != "development" || !vars["STRIPE_SECRET"].Secret || vars["API_URL"].Value != "https://api.example.com" {
		t.Errorf("vars: %+v", web.Env)
	}
	notes := strings.Join(web.Notes, "|")
	if !strings.Contains(notes, "password") || !strings.Contains(notes, "config.json") {
		t.Errorf("notes: %v", web.Notes)
	}
	api := p.Apps[1]
	if api.Environment != "staging" || api.BuildPack != "dockerfile" || api.Dockerfile != "docker/Dockerfile" || !strings.Contains(strings.Join(api.Notes, ""), "deploy key") {
		t.Errorf("api: %+v", api)
	}
	if proxy := p.Apps[2]; proxy.SourceType != "image" || proxy.Image != "nginx:1.27-alpine" || proxy.BuildPack != "image" {
		t.Errorf("proxy: %+v", proxy)
	}
	if d := p.Databases[0]; d.Engine != "postgres" || d.Image != "postgres:16-alpine" || d.Name != "shop-db" {
		t.Errorf("db: %+v", d)
	}
	if d := p.Databases[1]; d.Engine != "redis" || d.Image != "" || !strings.Contains(strings.Join(d.Notes, ""), "Redis") {
		t.Errorf("keydb: %+v", d)
	}
	s := p.Services[0]
	if s.Domain != "https://flows.example.com" || !strings.Contains(s.Compose, "N8N_HOST=${PORTDOCK_FQDN}") || len(s.Env) != 1 || s.Env[0].Key != "SMTP_HOST" || s.Environment != "staging" {
		t.Errorf("service: %+v", s)
	}
}

func TestHeroku(t *testing.T) {
	srv := serveJSON(t, "Bearer hrku-key", map[string]any{
		"/apps": []map[string]any{{"id": "h1", "name": "acme-web", "git_url": "https://git.heroku.com/acme-web.git", "team": map[string]any{"name": "acme"}}},
		"/apps/h1/formation": []map[string]any{{"type": "web", "quantity": 2, "command": "npm start"}, {"type": "worker", "quantity": 1, "command": "node worker.js"},
			{"type": "release", "quantity": 0, "command": "x"}},
		"/apps/h1/config-vars": map[string]any{"DATABASE_URL": "postgres://u:p@ec2.amazonaws.com/db", "NODE_ENV": "production", "EMPTY": nil},
		"/apps/h1/addons": []map[string]any{{"name": "postgresql-curved-123", "addon_service": map[string]any{"name": "heroku-postgresql"}, "plan": map[string]any{"name": "heroku-postgresql:essential-0"},
			"config_vars": []string{"DATABASE_URL"}}, {"name": "papertrail-x", "addon_service": map[string]any{"name": "papertrail"}, "plan": map[string]any{"name": "papertrail:choklad"}}},
		"/apps/h1/domains": []map[string]any{{"hostname": "acme-web.herokuapp.com", "kind": "heroku"}, {"hostname": "www.acme.com", "kind": "custom"}},
	})
	defer srv.Close()
	old := herokuAPI
	herokuAPI = srv.URL
	defer func() { herokuAPI = old }()
	p, err := Read(context.Background(), Heroku, Credentials{Token: "hrku-key"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Apps) != 1 || len(p.Databases) != 1 {
		t.Fatalf("plan: %+v", p)
	}
	a := p.Apps[0]
	if a.Project != "acme" || a.GitRepo != "https://git.heroku.com/acme-web.git" || a.GitToken != "hrku-key" || a.Replicas != 2 || a.BuildPack != "nixpacks" ||
		len(a.Domains) != 1 || a.Domains[0] != "www.acme.com" || len(a.Env) != 3 {
		t.Errorf("app: %+v", a)
	}
	notes := strings.Join(a.Notes, "|")
	for _, want := range []string{"worker process", "papertrail-x", "DATABASE_URL still points", "Heroku Git"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes miss %q: %v", want, a.Notes)
		}
	}
	if d := p.Databases[0]; d.Engine != "postgres" || d.Name != "acme-web-postgres" || d.Project != "acme" {
		t.Errorf("db: %+v", d)
	}
	b, _ := json.Marshal(a)
	if strings.Contains(string(b), "hrku-key") {
		t.Error("the API key is in the plan's JSON")
	}
}

func TestDokku(t *testing.T) {
	outputs := map[string]string{
		"--quiet apps:list":                                  "shop\nworker-only\n",
		"config:export --format json shop":                   `{"DOKKU_APP_RESTORE":"1","DATABASE_URL":"postgres://postgres:x@dokku-postgres-shopdb:5432/shopdb","SECRET_KEY_BASE":"abc","GIT_REV":"d1"}`,
		"domains:report shop --domains-app-vhosts":           "shop.example.com www.shop.example.com\n",
		"ports:report shop --ports-map":                      "http:80:3000 https:443:3000\n",
		"ps:scale shop":                                      "-----> Scaling for shop\nproctype: qty\n--------: ---\nweb:  2\nworker: 1\n",
		"storage:report shop --storage-deploy-mounts":        "-v /var/lib/dokku/data/storage/shop:/app/storage\n",
		"config:export --format json worker-only":            `{}`,
		"domains:report worker-only --domains-app-vhosts":    "",
		"ports:report worker-only --ports-map":               "",
		"ps:scale worker-only":                               "web: 0\n",
		"storage:report worker-only --storage-deploy-mounts": "",
		"postgres:list":                                      "=====> Postgres services\nshopdb\n",
		"redis:list":                                         "NAME     VERSION    STATUS\ncache    redis:7.2  running\n",
	}
	var asked []string
	run := func(_ context.Context, cmd string) (string, error) {
		asked = append(asked, cmd)
		if cmd == "dokku --quiet apps:list" {
			return "", errors.New("dokku: command not found") // the SSH user is dokku
		}
		if out, ok := outputs[cmd]; ok {
			return out, nil
		}
		return "", errors.New("unknown command " + cmd)
	}
	p, err := Read(context.Background(), Dokku, Credentials{Run: run})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Apps) != 2 || len(p.Databases) != 2 {
		t.Fatalf("plan: %+v (asked %v)", p, asked)
	}
	a := p.Apps[0]
	if a.Image != "dokku/shop:latest" || a.SourceType != "image" || a.Port != 3000 || a.Replicas != 2 || len(a.Domains) != 2 || len(a.Env) != 2 ||
		len(a.Volumes) != 1 || a.Volumes[0].Path != "/app/storage" || a.Volumes[0].Name != "shop-storage" {
		t.Errorf("shop: %+v", a)
	}
	notes := strings.Join(a.Notes, "|")
	if !strings.Contains(notes, "worker process") || !strings.Contains(notes, "DATABASE_URL points") || !strings.Contains(notes, "empty volume") {
		t.Errorf("notes: %v", a.Notes)
	}
	if len(p.Apps[1].Domains) != 0 {
		t.Errorf("the default domain of Dokku is imported: %v", p.Apps[1].Domains)
	}
	if p.Databases[0].Engine != "postgres" || p.Databases[0].Name != "shopdb" || p.Databases[1].Engine != "redis" || p.Databases[1].Name != "cache" {
		t.Errorf("dbs: %+v", p.Databases)
	}
}

func TestEngineOf(t *testing.T) {
	for in, want := range map[string]string{"heroku-postgresql": "postgres", "heroku-redis": "redis", "jawsdb": "mysql", "jawsdb-maria": "mariadb",
		"cleardb": "mysql", "standalone-mongodb": "mongodb", "valkey/valkey:8": "valkey", "papertrail": ""} {
		if got, _ := engineOf(in); got != want {
			t.Errorf("%s: %q", in, got)
		}
	}
}

func TestResourceName(t *testing.T) {
	for in, want := range map[string]string{"Web Front": "web-front", "  my_app.v2 ": "my-app-v2", "!!!": "app"} {
		if got := resourceName(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
