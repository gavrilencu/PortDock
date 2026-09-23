package api

import (
	"fmt"
	"testing"
)

func TestServiceScopedEnv(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	c := e.login("owner@corp.com")
	p := e.expect(c, "POST", "/api/projects", map[string]any{"name": "shop"}, 200)
	env := p["environments"].([]any)[0].(map[string]any)["id"]
	mk := func(name, pack string) string {
		body := map[string]any{"name": name, "environment_id": env, "server_id": 1, "source_type": "git", "git_repository": "https://github.com/acme/" + name + ".git", "build_pack": pack}
		return fmt.Sprintf("/api/applications/%v", e.expect(c, "POST", "/api/applications", body, 200)["id"])
	}
	shop, web := mk("shop", "compose"), mk("web", "node")

	// The same key per service, plus an application-wide one.
	e.expect(c, "POST", shop+"/envs", map[string]any{"key": "PORT", "value": "80", "service": "frontend"}, 200)
	e.expect(c, "POST", shop+"/envs", map[string]any{"key": "PORT", "value": "4000", "service": "backend"}, 200)
	e.expect(c, "POST", shop+"/envs", map[string]any{"key": "PORT", "value": "1"}, 200)
	e.expect(c, "POST", shop+"/envs", map[string]any{"key": "PORT", "value": "2", "service": "backend"}, 400) // duplicate within a service

	// Bulk-editing one service leaves the others alone.
	e.expect(c, "PUT", shop+"/envs", map[string]any{"service": "backend", "content": "PORT=4001\nDATABASE_URL=postgres://db/app\n"}, 200)
	code, _ := e.do(c, "GET", shop+"/envs", nil)
	if code != 200 {
		t.Fatalf("list: %d", code)
	}
	rows, err := e.db.Query("SELECT service, key FROM env_vars ORDER BY service, key")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var s, k string
		_ = rows.Scan(&s, &k)
		got = append(got, s+":"+k)
	}
	rows.Close()
	if fmt.Sprint(got) != "[:PORT backend:DATABASE_URL backend:PORT frontend:PORT]" {
		t.Fatalf("variables: %v", got)
	}

	// Services only exist for compose applications.
	e.expect(c, "POST", web+"/envs", map[string]any{"key": "A", "value": "1", "service": "frontend"}, 400)
	e.expect(c, "POST", shop+"/envs", map[string]any{"key": "A", "value": "1", "service": "bad name"}, 400)

	// Applying variables needs a deployed version.
	e.expect(c, "POST", shop+"/apply-env", nil, 400)
	if _, err := e.db.Exec("INSERT INTO deployments(uuid, application_id, status, trigger, image, created_at) VALUES ('d1', ?, 'success', 'manual', 'compose@abc', '2026-01-01')", e.expect(c, "GET", shop, nil, 200)["id"]); err != nil {
		t.Fatal(err)
	}
	d := e.expect(c, "POST", shop+"/apply-env", nil, 200)
	if d["trigger"] != "config" || d["image"] != "compose@abc" {
		t.Fatalf("apply-env deployment: %v", d)
	}
}
