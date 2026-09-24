package api

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/portdock/portdock/internal/store"
)

func (e *testEnv) login(email string) *http.Client {
	c := e.client()
	if code, res := e.do(c, "POST", "/api/auth/login", map[string]string{"email": email, "password": "correct-horse"}); code != 200 {
		e.t.Fatalf("login %s: %d %v", email, code, res)
	}
	return c
}

func (e *testEnv) expect(c *http.Client, method, path string, body any, want int) map[string]any {
	e.t.Helper()
	code, res := e.do(c, method, path, body)
	if code != want {
		e.t.Fatalf("%s %s: got %d want %d (%v)", method, path, code, want, res)
	}
	return res
}

func TestProjectRolesAndApprovals(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	users := map[string]store.User{}
	for _, n := range []string{"maint", "maint2", "dev", "viewer", "outsider"} {
		users[n] = e.user(n+"@corp.com", "correct-horse", "member")
	}
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	owner := e.login("owner@corp.com")

	// Private project with explicit members; production protected, staging open.
	p := e.expect(owner, "POST", "/api/projects", map[string]any{"name": "payments", "default_role": "none"}, 200)
	pid := int64(p["id"].(float64))
	prod := int64(p["environments"].([]any)[0].(map[string]any)["id"].(float64))
	stg := int64(e.expect(owner, "POST", fmt.Sprintf("/api/projects/%d/environments", pid), map[string]any{"name": "staging"}, 200)["id"].(float64))
	e.expect(owner, "PATCH", fmt.Sprintf("/api/environments/%d", prod), map[string]any{"protected": true}, 200)
	e.expect(owner, "PUT", fmt.Sprintf("/api/projects/%d/members", pid), map[string]any{"members": []map[string]any{
		{"user_id": users["maint"].ID, "role": "maintainer"}, {"user_id": users["maint2"].ID, "role": "maintainer"},
		{"user_id": users["dev"].ID, "role": "developer"}, {"user_id": users["viewer"].ID, "role": "viewer"},
	}}, 200)
	mkApp := func(env int64, name string) int64 {
		res := e.expect(owner, "POST", "/api/applications", map[string]any{"name": name, "environment_id": env, "server_id": 1,
			"source_type": "image", "docker_image": "nginx:alpine", "domains": ""}, 200)
		return int64(res["id"].(float64))
	}
	prodApp, stgApp := mkApp(prod, "api-prod"), mkApp(stg, "api-staging")
	app := func(id int64) string { return fmt.Sprintf("/api/applications/%d", id) }

	outsider, viewer, dev, maint, maint2 := e.login("outsider@corp.com"), e.login("viewer@corp.com"), e.login("dev@corp.com"), e.login("maint@corp.com"), e.login("maint2@corp.com")

	// Outsider: the project doesn't exist for them.
	if _, list := e.do(outsider, "GET", "/api/projects", nil); list["_raw"] != "[]\n" {
		t.Fatalf("outsider sees projects: %v", list)
	}
	e.expect(outsider, "GET", app(prodApp), nil, 404)
	e.expect(outsider, "POST", app(stgApp)+"/deploy", nil, 404)
	e.expect(outsider, "POST", "/api/applications", map[string]any{"name": "x", "environment_id": stg, "server_id": 1, "source_type": "image", "docker_image": "nginx"}, 400)

	// Viewer: read only, no secrets.
	res := e.expect(viewer, "GET", app(prodApp), nil, 200)
	if acc := res["access"].(map[string]any); acc["role"] != "viewer" || acc["can_write"] != false || acc["can_deploy"] != false {
		t.Fatalf("viewer access %v", acc)
	}
	e.expect(viewer, "PATCH", app(stgApp), map[string]any{"description": "x"}, 403)
	e.expect(viewer, "GET", app(stgApp)+"/envs", nil, 403)
	e.expect(viewer, "POST", app(stgApp)+"/deploy", nil, 403)

	// Developer: free on staging, approval needed on protected production.
	e.expect(dev, "PATCH", app(stgApp), map[string]any{"description": "tuned"}, 200)
	e.expect(dev, "POST", app(stgApp)+"/envs", map[string]any{"key": "A", "value": "1"}, 200)
	if d := e.expect(dev, "POST", app(stgApp)+"/deploy", nil, 200); d["status"] != "queued" {
		t.Fatalf("staging deploy %v", d)
	}
	e.expect(dev, "PATCH", app(prodApp), map[string]any{"description": "x"}, 403)
	e.expect(dev, "GET", app(prodApp)+"/envs", nil, 403)
	e.expect(dev, "DELETE", app(stgApp), nil, 403)
	req := e.expect(dev, "POST", app(prodApp)+"/deploy", nil, 200)
	if req["status"] != "pending_approval" {
		t.Fatalf("protected deploy by developer: %v", req)
	}
	depPath := fmt.Sprintf("/api/deployments/%d", int64(req["id"].(float64)))
	e.expect(dev, "POST", depPath+"/approve", nil, 403)
	if d := e.expect(maint, "GET", depPath, nil, 200); d["can_review"] != true || d["requested_by_name"] != "dev" {
		t.Fatalf("maintainer view of request %v", d)
	}
	if d := e.expect(maint, "POST", depPath+"/approve", map[string]string{"comment": "LGTM"}, 200); d["status"] != "queued" || d["review_comment"] != "LGTM" {
		t.Fatalf("approve %v", d)
	}
	e.expect(maint, "POST", depPath+"/approve", nil, 400) // already reviewed

	// Maintainers deploy protected envs directly…
	if d := e.expect(maint, "POST", app(prodApp)+"/deploy", nil, 200); d["status"] != "queued" {
		t.Fatalf("maintainer protected deploy %v", d)
	}
	// …until every deployment requires approval: four-eyes, even for maintainers.
	e.expect(maint, "PATCH", fmt.Sprintf("/api/environments/%d", prod), map[string]any{"require_approval": true}, 200)
	req = e.expect(maint, "POST", app(prodApp)+"/deploy", nil, 200)
	depPath = fmt.Sprintf("/api/deployments/%d", int64(req["id"].(float64)))
	if req["status"] != "pending_approval" {
		t.Fatalf("require_approval deploy %v", req)
	}
	e.expect(maint, "POST", depPath+"/approve", nil, 403)
	e.expect(maint2, "POST", depPath+"/reject", map[string]string{"comment": "not today"}, 200)
	if d := e.expect(maint, "GET", depPath, nil, 200); d["status"] != "rejected" || d["reviewed_by_name"] != "maint2" {
		t.Fatalf("reject %v", d)
	}

	// Git pushes / deploy hooks also wait in require_approval environments.
	appRow := e.expect(maint, "GET", fmt.Sprintf("/api/applications/%d", prodApp), nil, 200)
	uuid, secret := appRow["uuid"].(string), appRow["webhook_secret"].(string)
	hook := e.expect(e.client(), "POST", "/api/webhooks/deploy/"+uuid+"?token="+secret, nil, 202)
	if hook["status"] != "pending_approval" {
		t.Fatalf("deploy hook %v", hook)
	}

	// Lists are filtered.
	var n int
	code, _ := e.do(outsider, "GET", "/api/deployments", nil)
	if code != 200 {
		t.Fatalf("deployments list %d", code)
	}
	resp, _ := outsider.Get(e.srv.URL + "/api/deployments")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("outsider sees deployments: %s", body)
	}
	_ = e.db.QueryRow("SELECT COUNT(*) FROM deployments").Scan(&n)
	if n < 5 {
		t.Fatalf("expected deployments, got %d", n)
	}

	// Members can't lock themselves out of their own project.
	e.expect(maint, "PUT", fmt.Sprintf("/api/projects/%d/members", pid), map[string]any{"members": []map[string]any{{"user_id": users["dev"].ID, "role": "developer"}}}, 400)

	// Audit log: members see only their own entries; admins can export CSV.
	resp, _ = dev.Get(e.srv.URL + "/api/activity")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "project.create") || !strings.Contains(string(body), "deployment.request_approval") {
		t.Fatalf("member audit view: %s", body)
	}
	e.expect(dev, "GET", "/api/activity/export", nil, 403)
	resp, _ = owner.Get(e.srv.URL + "/api/activity/export?action=deployment.")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.HasPrefix(string(body), "id,time,user_id,user_email") || !strings.Contains(string(body), "deployment.approve") || strings.Contains(string(body), "project.create") ||
		resp.Header.Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("csv export: %s", body)
	}
	if !strings.Contains(string(body), "127.0.0.1") {
		t.Fatalf("audit entries should carry the client IP: %s", body)
	}
}

func TestCSVSafe(t *testing.T) {
	if csvSafe("=HYPERLINK(1)") != "'=HYPERLINK(1)" || csvSafe("api") != "api" {
		t.Fatal("formula injection not neutralised")
	}
}

func TestPhase(t *testing.T) {
	now := time.Now()
	mk := func(status string, restarting bool, restarts, exit int, health string, started time.Time) inspectJSON {
		var c inspectJSON
		c.State.Status, c.State.Restarting, c.RestartCount, c.State.ExitCode = status, restarting, restarts, exit
		c.State.StartedAt = started.Format(time.RFC3339Nano)
		if health != "" {
			c.State.Health = &struct {
				Status string `json:"Status"`
			}{health}
		}
		return c
	}
	cases := map[string]inspectJSON{
		"healthy":    mk("running", false, 0, 0, "healthy", now.Add(-time.Hour)),
		"running":    mk("running", false, 0, 0, "", now.Add(-time.Hour)),
		"unhealthy":  mk("running", false, 0, 0, "unhealthy", now.Add(-time.Hour)),
		"crashloop":  mk("restarting", true, 7, 1, "", now),
		"restarting": mk("restarting", true, 1, 1, "", now),
		"failed":     mk("exited", false, 0, 137, "", now),
		"exited":     mk("exited", false, 0, 0, "", now),
	}
	for want, c := range cases {
		if got := phase(c, now); got != want {
			t.Errorf("want %s got %s", want, got)
		}
	}
	if got := phase(mk("running", false, 5, 0, "", now.Add(-time.Minute)), now); got != "crashloop" {
		t.Errorf("recently restarted many times: %s", got)
	}
}
