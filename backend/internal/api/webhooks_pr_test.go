package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// Pull requests from forks are not deployed (their code would run with the
// application's variables), and refs that git could read as options are refused.
func TestWebhookForksAndRefs(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	c := e.login("owner@corp.com")
	p := e.expect(c, "POST", "/api/projects", map[string]any{"name": "shop"}, 200)
	env := p["environments"].([]any)[0].(map[string]any)["id"]
	app := e.expect(c, "POST", "/api/applications", map[string]any{
		"name": "web", "environment_id": env, "server_id": 1, "source_type": "git", "git_repository": "https://github.com/acme/web.git",
		"build_pack": "dockerfile", "preview_enabled": true, "auto_deploy": true,
	}, 200)
	uuid, secret := app["uuid"].(string), app["webhook_secret"].(string)

	send := func(event string, payload any) map[string]any {
		body, _ := json.Marshal(payload)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		req, _ := http.NewRequest("POST", e.srv.URL+"/api/webhooks/github/"+uuid, bytes.NewReader(body))
		req.Header.Set("X-GitHub-Event", event)
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		if resp.StatusCode != 202 {
			t.Fatalf("%s: status %d %v", event, resp.StatusCode, out)
		}
		return out
	}
	pr := func(head string) map[string]any {
		return map[string]any{"action": "opened", "number": 7, "pull_request": map[string]any{
			"title": "change", "head": map[string]any{"ref": "feature", "sha": "0123456789abcdef0123456789abcdef01234567", "repo": map[string]any{"full_name": head}},
			"base": map[string]any{"ref": "main", "repo": map[string]any{"full_name": "acme/web"}},
		}}
	}
	if r := send("pull_request", pr("mallory/web")); r["status"] != "ignored" {
		t.Fatalf("fork PR deployed: %v", r)
	}
	if r := send("pull_request", pr("acme/web")); r["status"] != "queued" {
		t.Fatalf("same-repository PR not deployed: %v", r)
	}

	for _, q := range []string{"commit=--upload-pack=touch%20/tmp/x", "branch=--upload-pack=id", "branch=a..b", "commit=main"} {
		e.expect(e.client(), "POST", fmt.Sprintf("/api/webhooks/deploy/%s?token=%s&%s", uuid, secret, q), nil, 400)
	}
	e.expect(e.client(), "POST", fmt.Sprintf("/api/webhooks/deploy/%s?token=%s&branch=release/1.2&commit=0123abc", uuid, secret), nil, 202)
}
