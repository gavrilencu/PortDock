package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMCPServer(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	owner := e.login("owner@corp.com")
	p := e.expect(owner, "POST", "/api/projects", map[string]any{"name": "shop"}, 200)
	env := p["environments"].([]any)[0].(map[string]any)["id"]
	e.expect(owner, "POST", "/api/applications", map[string]any{"name": "web", "environment_id": env, "server_id": 1, "source_type": "image", "docker_image": "nginx"}, 200)
	readTok := e.expect(owner, "POST", "/api/tokens", map[string]any{"name": "agent-read", "scope": "read"}, 200)["token"].(string)
	deployTok := e.expect(owner, "POST", "/api/tokens", map[string]any{"name": "agent-deploy", "scope": "deploy"}, 200)["token"].(string)

	post := func(token, body string) (int, []byte) {
		req, _ := http.NewRequest("POST", e.srv.URL+"/api/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	rpc := func(token, method string, params any) map[string]any {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		code, out := post(token, string(b))
		if code != 200 {
			t.Fatalf("%s: HTTP %d %s", method, code, out)
		}
		var res map[string]any
		_ = json.Unmarshal(out, &res)
		return res
	}
	call := func(token, tool string, args map[string]any) (string, bool) {
		t.Helper()
		res := rpc(token, "tools/call", map[string]any{"name": tool, "arguments": args})["result"].(map[string]any)
		text := res["content"].([]any)[0].(map[string]any)["text"].(string)
		return text, res["isError"] == true
	}

	// A browser session is refused: only API tokens.
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", e.srv.URL)
	if res, err := owner.Do(req); err != nil || res.StatusCode != 401 {
		t.Fatalf("session: %v %v", res.StatusCode, err)
	}
	if code, _ := post("", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}

	init := rpc(readTok, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}})
	r := init["result"].(map[string]any)
	if r["protocolVersion"] != "2025-06-18" || r["serverInfo"].(map[string]any)["name"] != "portdock" {
		t.Fatalf("initialize: %v", init)
	}
	if code, body := post(readTok, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != 202 || len(body) != 0 {
		t.Fatalf("notification: %d %s", code, body)
	}
	tools := rpc(readTok, "tools/list", map[string]any{})["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		m := tl.(map[string]any)
		names[m["name"].(string)] = true
		if m["inputSchema"].(map[string]any)["type"] != "object" {
			t.Errorf("%s: schema %v", m["name"], m["inputSchema"])
		}
	}
	for _, n := range []string{"list_applications", "deploy_application", "application_logs", "rollback_deployment", "get_deployment"} {
		if !names[n] {
			t.Errorf("missing tool %s", n)
		}
	}

	text, isErr := call(readTok, "list_applications", nil)
	if isErr || !strings.Contains(text, `"name": "web"`) || !strings.Contains(text, `"project": "shop"`) {
		t.Errorf("list_applications: %s", text)
	}
	if text, isErr = call(readTok, "get_application", map[string]any{"app": "shop/web"}); isErr || !strings.Contains(text, `"docker_image": "nginx"`) {
		t.Errorf("get_application: %s", text)
	}
	if text, isErr = call(readTok, "get_application", map[string]any{"app": "nope"}); !isErr || !strings.Contains(text, "no application") {
		t.Errorf("unknown app: %s", text)
	}
	// The token's scope applies to each tool.
	if text, isErr = call(readTok, "deploy_application", map[string]any{"app": "web"}); !isErr || !strings.Contains(strings.ToLower(text), "scope") {
		t.Errorf("deploy with a read token: %v %s", isErr, text)
	}
	if text, isErr = call(deployTok, "deploy_application", map[string]any{"app": "web"}); isErr || !strings.Contains(text, "deployment_id") {
		t.Errorf("deploy: %v %s", isErr, text)
	}

	if res := rpc(readTok, "resources/list", nil); res["error"].(map[string]any)["code"] != float64(-32601) {
		t.Errorf("unknown method: %v", res)
	}
	code, out := post(readTok, `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":2,"method":"ping"}]`)
	var batch []map[string]any
	if code != 200 || json.Unmarshal(out, &batch) != nil || len(batch) != 2 {
		t.Errorf("batch: %d %s", code, out)
	}
	if code, out := post(readTok, `{not json`); code != 200 || !bytes.Contains(out, []byte("-32700")) {
		t.Errorf("parse error: %d %s", code, out)
	}
}
