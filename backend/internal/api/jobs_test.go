package api

import (
	"fmt"
	"testing"
)

func TestApplicationJobs(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	viewer := e.user("viewer@corp.com", "correct-horse", "member")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	owner := e.login("owner@corp.com")
	p := e.expect(owner, "POST", "/api/projects", map[string]any{"name": "shop"}, 200)
	env := p["environments"].([]any)[0].(map[string]any)["id"]
	e.expect(owner, "PATCH", fmt.Sprintf("/api/projects/%v", p["id"]), map[string]any{"name": "shop", "default_role": "none"}, 200)
	e.expect(owner, "PUT", fmt.Sprintf("/api/projects/%v/members", p["id"]), map[string]any{"members": []map[string]any{{"user_id": viewer.ID, "role": "viewer"}}}, 200)
	v := e.login("viewer@corp.com")
	app := e.expect(owner, "POST", "/api/applications", map[string]any{"name": "web", "environment_id": env, "server_id": 1, "source_type": "image", "docker_image": "nginx"}, 200)
	base := fmt.Sprintf("/api/applications/%v/jobs", app["id"])

	for _, bad := range []map[string]any{
		{"name": "", "schedule": "0 3 * * *", "command": "echo"},
		{"name": "x", "schedule": "every night", "command": "echo"},
		{"name": "x", "schedule": "0 3 * * *", "command": ""},
		{"name": "x", "schedule": "0 3 * * *", "command": "echo", "timeout": 5},
		{"name": "x", "schedule": "0 3 * * *", "command": "echo", "service": "worker"}, // not a compose app
	} {
		e.expect(owner, "POST", base, bad, 400)
	}
	job := e.expect(owner, "POST", base, map[string]any{"name": "nightly report", "schedule": "30 2 * * *", "command": "php artisan report:send"}, 200)
	if job["enabled"] != true || job["timeout"] != float64(3600) || job["next_run_at"] == "" {
		t.Fatalf("job: %v", job)
	}
	e.expect(owner, "POST", base, map[string]any{"name": "every 10 minutes", "schedule": "@every 10m", "command": "true"}, 200)
	jobPath := fmt.Sprintf("/api/jobs/%v", job["id"])

	// Viewers see the jobs and their runs, but don't change or run them.
	if list := e.expectList(v, "GET", base); len(list) != 2 {
		t.Fatalf("viewer list: %v", list)
	}
	e.expect(v, "PATCH", jobPath, map[string]any{"enabled": false}, 403)
	e.expect(v, "POST", jobPath+"/run", nil, 403)
	e.expectList(v, "GET", jobPath+"/runs")

	// Jobs need an image: the application was never deployed.
	e.expect(owner, "POST", jobPath+"/run", nil, 400)

	paused := e.expect(owner, "PATCH", jobPath, map[string]any{"enabled": false}, 200)
	if paused["enabled"] != false || paused["next_run_at"] != "" {
		t.Fatalf("paused: %v", paused)
	}
	e.expect(owner, "DELETE", jobPath, nil, 200)
	if list := e.expectList(owner, "GET", base); len(list) != 1 {
		t.Fatalf("after delete: %v", list)
	}
}
