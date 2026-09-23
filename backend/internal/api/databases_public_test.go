package api

import (
	"fmt"
	"strings"
	"testing"
)

func TestDatabasePublicAccess(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, is_local, status) VALUES (1, 'local', 'localhost', 1, 'reachable'), (2, 'edge', 'edge.example.net', 0, 'reachable')"); err != nil {
		t.Fatal(err)
	}
	c := e.login("owner@corp.com")
	p := e.expect(c, "POST", "/api/projects", map[string]any{"name": "shop"}, 200)
	env := p["environments"].([]any)[0].(map[string]any)["id"]

	create := func(name string, server, port int, host string, want int) map[string]any {
		return e.expect(c, "POST", "/api/databases", map[string]any{
			"name": name, "engine": "postgres", "environment_id": env, "server_id": server,
			"public_port": port, "public_host": host, "password": "s3cret", "start": false,
		}, want)
	}
	path := func(db map[string]any) string { return fmt.Sprintf("/api/databases/%v", db["id"]) }

	// Not exposed: no public URL.
	a := create("a", 1, 0, "", 200)
	if a["public_url"] != "" {
		t.Fatalf("unexposed public_url = %v", a["public_url"])
	}

	// Local server without a known public IP falls back to localhost ...
	a = e.expect(c, "PATCH", path(a), map[string]any{"public_port": 15432}, 200)
	if u := a["public_url"].(string); !strings.Contains(u, "@localhost:15432/") {
		t.Fatalf("local fallback url = %s", u)
	}
	// ... and uses the server's public IP once it is known (the instance URL is loopback here).
	if err := e.db.SetSetting(t.Context(), "public_ipv4", "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	a = e.expect(c, "GET", path(a), nil, 200)
	if u := a["public_url"].(string); !strings.Contains(u, "@203.0.113.7:15432/") || a["public_address"] != "203.0.113.7" {
		t.Fatalf("public IP url = %s (%v)", u, a["public_address"])
	}

	// A public host is normalized and wins.
	a = e.expect(c, "PATCH", path(a), map[string]any{"public_host": " Postgres://DB.Example.com:5432/app "}, 200)
	if a["public_host"] != "db.example.com" {
		t.Fatalf("public_host = %v", a["public_host"])
	}
	if u := a["public_url"].(string); !strings.Contains(u, "@db.example.com:15432/") {
		t.Fatalf("public host url = %s", u)
	}

	// Invalid hosts are rejected.
	for _, h := range []string{"not a host", "*.example.com", "exa_mple.com", "-bad.example.com"} {
		e.expect(c, "PATCH", path(a), map[string]any{"public_host": h}, 400)
	}
	e.expect(c, "PATCH", path(a), map[string]any{"public_port": 70000}, 400)

	// Port conflicts on the same server are refused with 409, other servers are fine.
	res := create("b", 1, 15432, "", 409)
	if msg, _ := res["error"].(string); !strings.Contains(msg, `port 15432 is already used by database "a"`) {
		t.Fatalf("conflict message = %v", res)
	}
	b := create("b", 1, 0, "", 200)
	e.expect(c, "PATCH", path(b), map[string]any{"public_port": 15432}, 409)
	e.expect(c, "PATCH", path(a), map[string]any{"public_port": 15432, "name": "a2"}, 200) // own port is not a conflict

	// A remote server's host is used as is.
	r := create("remote", 2, 15432, "", 200)
	if u := r["public_url"].(string); !strings.Contains(u, "@edge.example.net:15432/") {
		t.Fatalf("remote url = %s", u)
	}

	// Clearing the public host falls back again.
	a = e.expect(c, "PATCH", path(a), map[string]any{"public_host": ""}, 200)
	if a["public_address"] != "203.0.113.7" {
		t.Fatalf("cleared public host address = %v", a["public_address"])
	}
}
