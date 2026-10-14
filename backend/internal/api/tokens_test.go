package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestScopedAPITokens(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("dev@corp.com", "correct-horse", "member")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	owner, dev := e.login("owner@corp.com"), e.login("dev@corp.com")
	mkProject := func(name string) (int64, any) {
		p := e.expect(owner, "POST", "/api/projects", map[string]any{"name": name}, 200)
		return int64(p["id"].(float64)), p["environments"].([]any)[0].(map[string]any)["id"]
	}
	pa, envA := mkProject("alpha")
	pb, envB := mkProject("beta")
	mkApp := func(env any, name string) string {
		a := e.expect(owner, "POST", "/api/applications", map[string]any{"name": name, "environment_id": env, "server_id": 1, "source_type": "image", "docker_image": "nginx"}, 200)
		return fmt.Sprintf("/api/applications/%v", a["id"])
	}
	appA, appB := mkApp(envA, "web-a"), mkApp(envB, "web-b")

	call := func(token, method, path string, body any) (int, map[string]any) {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		req, _ := http.NewRequest(method, e.srv.URL+path, &buf)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	want := func(token, method, path string, body any, code int) {
		t.Helper()
		if got, out := call(token, method, path, body); got != code {
			t.Errorf("%s %s: got %d want %d (%v)", method, path, got, code, out)
		}
	}
	token := func(c *http.Client, path string, in map[string]any, code int) string {
		t.Helper()
		res := e.expect(c, "POST", path, in, code)
		tok, _ := res["token"].(string)
		return tok
	}

	// Validation.
	token(owner, "/api/tokens", map[string]any{"name": "x", "scope": "root"}, 400)
	token(owner, "/api/tokens", map[string]any{"name": "x", "scope": "read", "expires_in_days": 5000}, 400)
	token(owner, "/api/tokens", map[string]any{"name": "x", "scope": "read", "project_ids": []int64{999}}, 400)

	// read: GET only.
	read := token(owner, "/api/tokens", map[string]any{"name": "dashboard", "scope": "read"}, 200)
	want(read, "GET", appA, nil, 200)
	want(read, "GET", appB, nil, 200)
	want(read, "POST", appA+"/deploy", map[string]any{}, 403)
	want(read, "POST", "/api/projects", map[string]any{"name": "gamma"}, 403)

	// deploy, limited to project alpha: other projects disappear.
	ci := token(owner, "/api/tokens", map[string]any{"name": "ci", "scope": "deploy", "project_ids": []int64{pa}, "expires_in_days": 90}, 200)
	want(ci, "GET", appA, nil, 200)
	want(ci, "GET", appB, nil, 404)
	want(ci, "POST", appA+"/deploy", map[string]any{}, 200)
	want(ci, "POST", appB+"/deploy", map[string]any{}, 404)
	want(ci, "PATCH", appA, map[string]any{"description": "changed by CI"}, 403)
	want(ci, "POST", "/api/projects", map[string]any{"name": "gamma"}, 403)
	_, app := call(ci, "GET", appA, nil)
	if acc := app["access"].(map[string]any); acc["can_deploy"] != true || acc["can_write"] != false {
		t.Errorf("access block of a deploy token: %v", acc)
	}
	// The owner's limited token is no administrator.
	want(ci, "GET", "/api/users", nil, 403)
	want(ci, "GET", "/api/settings", nil, 200)

	// write: configuration, but no deletions.
	wr := token(owner, "/api/tokens", map[string]any{"name": "terraform", "scope": "write", "project_ids": []int64{pa, pb}}, 200)
	want(wr, "PATCH", appA, map[string]any{"name": "web-a", "environment_id": envA, "server_id": 1, "source_type": "image", "docker_image": "nginx", "description": "by terraform"}, 200)
	want(wr, "DELETE", appA, nil, 403)
	want(wr, "GET", "/api/users", nil, 403)

	// full and unrestricted: everything the owner may do.
	full := token(owner, "/api/tokens", map[string]any{"name": "admin-script", "scope": "full"}, 200)
	want(full, "GET", "/api/users", nil, 200)
	want(full, "POST", "/api/projects", map[string]any{"name": "gamma"}, 200)

	// No token manages accounts or tokens.
	for _, tok := range []string{read, ci, wr, full} {
		want(tok, "POST", "/api/tokens", map[string]any{"name": "more", "scope": "full"}, 403)
		want(tok, "PATCH", "/api/auth/me", map[string]any{"name": "x"}, 403)
		want(tok, "POST", "/api/auth/2fa/disable", map[string]any{}, 403)
	}
	want(full, "GET", "/api/auth/me", nil, 200)

	// Expired tokens stop working.
	if _, err := e.db.Exec("UPDATE api_tokens SET expires_at = '2020-01-01T00:00:00Z' WHERE name = 'ci'"); err != nil {
		t.Fatal(err)
	}
	if code, out := call(ci, "GET", appA, nil); code != 401 || out["error"] != "this API token has expired" {
		t.Errorf("expired token: %d %v", code, out)
	}

	// Members limit tokens to the projects they can see.
	if _, err := e.db.Exec("UPDATE projects SET default_role = 'none' WHERE id = ?", pb); err != nil {
		t.Fatal(err)
	}
	token(dev, "/api/tokens", map[string]any{"name": "mine", "scope": "deploy", "project_ids": []int64{pb}}, 400)
	devTok := token(dev, "/api/tokens", map[string]any{"name": "mine", "scope": "deploy"}, 200)
	want(devTok, "GET", appB, nil, 404)
	want(devTok, "POST", appA+"/deploy", map[string]any{}, 200)
	if list := e.expectList(dev, "GET", "/api/tokens"); len(list) != 1 || list[0]["scope"] != "deploy" {
		t.Errorf("dev tokens: %v", list)
	}
}

func TestServiceAccounts(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("dev@corp.com", "correct-horse", "member")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	owner, dev := e.login("owner@corp.com"), e.login("dev@corp.com")
	p := e.expect(owner, "POST", "/api/projects", map[string]any{"name": "alpha"}, 200)
	pid, env := int64(p["id"].(float64)), p["environments"].([]any)[0].(map[string]any)["id"]
	a := e.expect(owner, "POST", "/api/applications", map[string]any{"name": "web", "environment_id": env, "server_id": 1, "source_type": "image", "docker_image": "nginx"}, 200)
	appPath := fmt.Sprintf("/api/applications/%v", a["id"])

	e.expect(dev, "POST", "/api/service-accounts", map[string]any{"name": "github-actions"}, 403)
	e.expect(owner, "POST", "/api/service-accounts", map[string]any{"name": "x", "role": "owner"}, 400)
	sa := e.expect(owner, "POST", "/api/service-accounts", map[string]any{"name": "github-actions"}, 200)
	if sa["service_account"] != true || sa["role"] != "member" {
		t.Fatalf("service account: %v", sa)
	}
	base := fmt.Sprintf("/api/service-accounts/%v", sa["id"])

	// It cannot sign in, even with a password set by an administrator.
	e.expect(e.client(), "POST", "/api/auth/login", map[string]any{"email": sa["email"], "password": "whatever-password"}, 401)

	res := e.expect(owner, "POST", base+"/tokens", map[string]any{"name": "deploy", "scope": "deploy", "project_ids": []int64{pid}}, 200)
	tok := res["token"].(string)
	req, _ := http.NewRequest("POST", e.srv.URL+appPath+"/deploy", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("service account deploy: %d", resp.StatusCode)
	}

	list := e.expectList(owner, "GET", "/api/service-accounts")
	if len(list) != 1 || len(list[0]["tokens"].([]any)) != 1 {
		t.Fatalf("list: %v", list)
	}
	// Disabling it stops its tokens.
	e.expect(owner, "PATCH", base, map[string]any{"disabled": true}, 200)
	req, _ = http.NewRequest("GET", e.srv.URL+appPath, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("disabled service account: %d", resp.StatusCode)
	}
	e.expect(owner, "DELETE", base, nil, 200)
	e.expect(owner, "DELETE", fmt.Sprintf("/api/service-accounts/%d", 1), nil, 404) // a person, not a service account
}
