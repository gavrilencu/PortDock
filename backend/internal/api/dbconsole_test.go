package api

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/portdock/portdock/internal/store"
)

func TestSQLConsoleAccess(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	maint := e.user("maint@corp.com", "correct-horse", "member")
	e.user("outsider@corp.com", "correct-horse", "member")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status, is_local) VALUES (1, 'local', 'localhost', 'reachable', 1)"); err != nil {
		t.Fatal(err)
	}
	owner := e.login("owner@corp.com")
	p := e.expect(owner, "POST", "/api/projects", map[string]any{"name": "shop", "default_role": "none"}, 200)
	pid := int64(p["id"].(float64))
	env := int64(p["environments"].([]any)[0].(map[string]any)["id"].(float64))
	e.expect(owner, "PUT", fmt.Sprintf("/api/projects/%d/members", pid), map[string]any{"members": []map[string]any{{"user_id": maint.ID, "role": "maintainer"}}}, 200)
	mk := func(engine string) int64 {
		d := store.Database{UUID: "pdtest" + engine, Name: engine, Engine: engine, EnvironmentID: env, ServerID: 1, Username: "u", DatabaseName: "app", Status: "stopped", CreatedAt: store.Now()}
		if _, err := store.Insert(t.Context(), e.db, "databases", &d); err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	pg, redis := mk("postgres"), mk("redis")
	base := fmt.Sprintf("/api/databases/%d", pg)
	m := e.login("maint@corp.com")

	if c := e.expect(owner, "GET", base+"/console", nil, 200); c["supported"] != true || c["can_write"] != true {
		t.Fatalf("owner console: %v", c)
	}
	// A maintainer who isn't an organization admin only reads.
	if c := e.expect(m, "GET", base+"/console", nil, 200); c["can_write"] != false {
		t.Fatalf("member console: %v", c)
	}
	e.expect(m, "POST", base+"/query", map[string]any{"query": "DELETE FROM t", "write": true}, 403)
	res := e.expect(m, "POST", base+"/query", map[string]any{"query": "DELETE FROM t"}, 400)
	if !strings.Contains(fmt.Sprint(res["error"]), "read-only") {
		t.Fatalf("read-only error: %v", res)
	}
	e.expect(owner, "POST", base+"/query", map[string]any{"query": `\! id`, "write": true}, 400)
	e.expect(owner, "POST", base+"/query", map[string]any{"query": "SELECT 1"}, 400) // not running
	e.expect(owner, "POST", fmt.Sprintf("/api/databases/%d/query", redis), map[string]any{"query": "GET k"}, 400)
	if c := e.expect(owner, "GET", fmt.Sprintf("/api/databases/%d/console", redis), nil, 200); c["supported"] != false {
		t.Fatalf("redis console: %v", c)
	}
	e.expect(e.login("outsider@corp.com"), "POST", base+"/query", map[string]any{"query": "SELECT 1"}, 404)

	if os.Getenv("PORTDOCK_DB_E2E") != "1" {
		return
	}
	// Against a real PostgreSQL started by PortDock.
	d := e.expect(owner, "POST", "/api/databases", map[string]any{"name": "live", "engine": "postgres", "environment_id": env, "server_id": 1}, 200)
	live := fmt.Sprintf("/api/databases/%v", d["id"])
	t.Cleanup(func() {
		e.do(owner, "DELETE", live, nil)
		_ = exec.Command("docker", "rm", "-f", d["uuid"].(string)).Run()
		_ = exec.Command("docker", "volume", "rm", "-f", "sy-"+d["uuid"].(string)).Run()
	})
	deadline := time.Now().Add(3 * time.Minute)
	for {
		code, out := e.do(owner, "POST", live+"/query", map[string]any{"query": "CREATE TABLE t (id int, v text); INSERT INTO t VALUES (1, 'a'), (2, NULL); SELECT * FROM t ORDER BY id", "write": true})
		if code == 200 && out["error"] == "" {
			rs := out["results"].([]any)
			sel := rs[2].(map[string]any)
			if len(rs) != 3 || rs[1].(map[string]any)["affected"].(float64) != 2 || len(sel["rows"].([]any)) != 2 || sel["rows"].([]any)[1].([]any)[1] != nil {
				t.Fatalf("results: %v", out)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never ran: %d %v", code, out)
		}
		time.Sleep(3 * time.Second)
	}
	out := e.expect(m, "POST", live+"/query", map[string]any{"query": "SELECT count(*) AS n FROM t"}, 200)
	if out["read_only"] != true || out["error"] != "" {
		t.Fatalf("member read: %v", out)
	}
	out = e.expect(m, "POST", live+"/query", map[string]any{"query": "WITH d AS (DELETE FROM t RETURNING *) SELECT count(*) FROM d"}, 200)
	if out["error"] == "" {
		t.Fatalf("a writing CTE ran in read-only mode: %v", out)
	}
	tables := e.expect(m, "GET", live+"/tables", nil, 200)
	if !strings.Contains(fmt.Sprint(tables["rows"]), "t") {
		t.Fatalf("tables: %v", tables)
	}
	var n int
	_ = e.db.QueryRow("SELECT COUNT(*) FROM activity WHERE action = 'database.query'").Scan(&n)
	if n == 0 {
		t.Error("queries are not in the audit log")
	}
}
