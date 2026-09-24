package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/portdock/portdock/internal/azuredevops"
)

// fakeAzure imitates the Azure DevOps REST API for one organization ("contoso")
// with a project "Shop" and a repository "web".
type fakeAzure struct {
	srv *httptest.Server
	mu  sync.Mutex
	// hooks holds the live service hook subscriptions by id.
	hooks   map[string]map[string]any
	created int
	deleted []string
}

func newFakeAzure(t *testing.T) *fakeAzure {
	f := &fakeAzure{hooks: map[string]map[string]any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAzure) org() string { return f.srv.URL + "/contoso" }

func (f *fakeAzure) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("api-version") == "" {
		http.Error(w, "api-version missing", 400)
		return
	}
	// A rejected token gets the sign-in page, like the real service.
	if r.Header.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(":good-pat")) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNonAuthoritativeInfo)
		_, _ = io.WriteString(w, "<html>Sign in</html>")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	send := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	p, q := strings.TrimPrefix(r.URL.Path, "/contoso"), r.URL.Query()
	repo := "/Shop/_apis/git/repositories/web"
	// Projects and repositories are addressed by name or id.
	p = strings.Replace(p, "/p1/_apis/git/repositories/r1", repo, 1)
	switch {
	case r.Method == "GET" && p == "/_apis/projects":
		send(map[string]any{"value": []map[string]string{{"id": "p1", "name": "Shop"}}})
	case r.Method == "GET" && p == "/_apis/git/repositories":
		send(map[string]any{"value": []map[string]any{
			{"id": "r1", "name": "web", "project": map[string]string{"id": "p1", "name": "Shop"}, "defaultBranch": "refs/heads/main",
				"remoteUrl": "https://contoso@dev.azure.com/contoso/Shop/_git/web", "webUrl": "https://dev.azure.com/contoso/Shop/_git/web"},
			{"id": "r2", "name": "old", "project": map[string]string{"id": "p1", "name": "Shop"}, "isDisabled": true},
		}})
	case r.Method == "GET" && p == repo+"/refs":
		send(map[string]any{"value": []map[string]string{{"name": "refs/heads/main"}, {"name": "refs/heads/dev"}}})
	case r.Method == "GET" && p == repo+"/items" && q.Get("scopePath") != "":
		send(map[string]any{"value": []map[string]any{
			{"path": "/", "isFolder": true}, {"path": "/Dockerfile"}, {"path": "/src", "isFolder": true},
		}})
	case r.Method == "GET" && p == repo+"/items":
		send(map[string]string{"content": "FROM nginx\nEXPOSE 8080\n"})
	case r.Method == "GET" && strings.HasPrefix(p, repo+"/commits/"):
		send(map[string]any{"changes": []map[string]any{
			{"item": map[string]any{"path": "/web/index.html"}}, {"item": map[string]any{"path": "/web", "isFolder": true}},
		}})
	case r.Method == "POST" && p == "/_apis/hooks/subscriptions":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.created++
		id := fmt.Sprintf("hook-%d", f.created)
		f.hooks[id] = body
		f.mu.Unlock()
		send(map[string]string{"id": id})
	case r.Method == "DELETE" && strings.HasPrefix(p, "/_apis/hooks/subscriptions/"):
		id := strings.TrimPrefix(p, "/_apis/hooks/subscriptions/")
		f.mu.Lock()
		delete(f.hooks, id)
		f.deleted = append(f.deleted, id)
		f.mu.Unlock()
		w.WriteHeader(204)
	default:
		w.WriteHeader(404)
		send(map[string]string{"message": "not found: " + r.Method + " " + p})
	}
}

func (f *fakeAzure) hook(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hooks[id]
}

func azurePush(branch, sha, message string) map[string]any {
	return map[string]any{
		"eventType": "git.push",
		"resource": map[string]any{
			"commits":    []map[string]string{{"commitId": sha, "comment": message}},
			"refUpdates": []map[string]string{{"name": "refs/heads/" + branch, "oldObjectId": strings.Repeat("1", 40), "newObjectId": sha}},
		},
	}
}

func TestAzureDevOpsPushToDeploy(t *testing.T) {
	az := newFakeAzure(t)
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("dev@corp.com", "correct-horse", "member")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status, is_local) VALUES (1, 'local', 'localhost', 'reachable', 1)"); err != nil {
		t.Fatal(err)
	}
	c := e.login("owner@corp.com")

	// Connecting checks the token; only admins can connect.
	e.expect(c, "POST", "/api/azure/connections", map[string]any{"url": az.org(), "token": "wrong"}, 400)
	e.expect(c, "POST", "/api/azure/connections", map[string]any{"url": az.org()}, 400)
	e.expect(e.login("dev@corp.com"), "POST", "/api/azure/connections", map[string]any{"url": az.org(), "token": "good-pat"}, 403)
	res := e.expect(c, "POST", "/api/azure/connections", map[string]any{"url": az.org() + "/", "token": " good-pat "}, 200)
	conn := res["connection"].(map[string]any)
	if conn["name"] != "contoso" || conn["url"] != az.org() || conn["token"] != nil || res["repositories"].(float64) != 1 {
		t.Fatalf("connection: %v", res)
	}
	cid := int64(conn["id"].(float64))

	_, list := e.do(c, "GET", "/api/azure/connections", nil)
	if raw := list["_raw"].(string); !strings.Contains(raw, `"name":"contoso"`) || strings.Contains(raw, "good-pat") {
		t.Fatalf("connections: %s", raw)
	}
	_, repos := e.do(c, "GET", fmt.Sprintf("/api/azure/repositories?connection=%d", cid), nil)
	var rs []azuredevops.Repository
	_ = json.Unmarshal([]byte(repos["_raw"].(string)), &rs)
	if len(rs) != 1 || rs[0].DefaultBranch != "main" || rs[0].RemoteURL != "https://dev.azure.com/contoso/Shop/_git/web" {
		t.Fatalf("repositories: %v", repos)
	}
	_, br := e.do(c, "GET", fmt.Sprintf("/api/azure/branches?connection=%d&project=Shop&repo=web", cid), nil)
	if br["_raw"] != "[\"dev\",\"main\"]\n" {
		t.Fatalf("branches: %v", br)
	}
	sug := e.expect(c, "GET", fmt.Sprintf("/api/azure/detect?connection=%d&project=Shop&repo=web&ref=main", cid), nil, 200)
	if sug["build_pack"] != "dockerfile" || fmt.Sprint(sug["files"]) != "[Dockerfile src/]" {
		t.Fatalf("detect: %v", sug)
	}

	// Creating the application subscribes a service hook to its pushes.
	p := e.expect(c, "POST", "/api/projects", map[string]any{"name": "shop"}, 200)
	env := p["environments"].([]any)[0].(map[string]any)["id"]
	appIn := map[string]any{
		"name": "web", "environment_id": env, "server_id": 1, "source_type": "git", "build_pack": "dockerfile",
		"git_repository": rs[0].RemoteURL, "git_branch": "main",
		"azure_connection_id": cid, "azure_project_id": "p1",
	}
	e.expect(c, "POST", "/api/applications", appIn, 400) // no repository selected
	appIn["azure_repo_id"] = "r1"
	app := e.expect(c, "POST", "/api/applications", appIn, 200)
	id, uuid, secret := int64(app["id"].(float64)), app["uuid"].(string), app["webhook_secret"].(string)
	if app["azure_hook_id"] != "hook-1,hook-2,hook-3" || app["azure_hook_error"] != "" {
		t.Fatalf("hooks not created: %v", app)
	}
	for id, ev := range map[string]string{"hook-2": "git.pullrequest.created", "hook-3": "git.pullrequest.updated"} {
		if az.hook(id)["eventType"] != ev {
			t.Fatalf("%s: %v", id, az.hook(id))
		}
	}
	h := az.hook("hook-1")
	ci, pi := h["consumerInputs"].(map[string]any), h["publisherInputs"].(map[string]any)
	if h["eventType"] != "git.push" || ci["url"] != e.srv.URL+"/api/webhooks/azure/"+uuid || ci["httpHeaders"] != "X-PortDock-Token:"+secret ||
		pi["projectId"] != "p1" || pi["repository"] != "r1" {
		t.Fatalf("hook: %v", h)
	}

	push := func(token string, body any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", e.srv.URL+"/api/webhooks/azure/"+uuid, strings.NewReader(string(b)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set(azuredevops.HookHeader, token)
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}
	sha := strings.Repeat("a", 40)
	if code, _ := push("", azurePush("main", sha, "x")); code != 401 {
		t.Fatalf("push without token: %d", code)
	}
	if code, _ := push("wrong", azurePush("main", sha, "x")); code != 401 {
		t.Fatalf("push with a wrong token: %d", code)
	}
	if _, out := push(secret, azurePush("dev", sha, "x")); out["status"] != "ignored" {
		t.Fatalf("push to another branch: %v", out)
	}
	if _, out := push(secret, azurePush("main", strings.Repeat("0", 40), "x")); out["status"] != "ignored" {
		t.Fatalf("deleted branch: %v", out)
	}
	if _, out := push(secret, map[string]any{"eventType": "git.pullrequest.created"}); out["status"] != "ignored" {
		t.Fatalf("other event: %v", out)
	}
	code, out := push(secret, azurePush("main", sha, "Fix the header\n\nlong text"))
	if code != 202 || out["deployment_id"] == nil {
		t.Fatalf("push to main: %d %v", code, out)
	}
	d := e.expect(c, "GET", fmt.Sprintf("/api/deployments/%v", out["deployment_id"]), nil, 200)
	if dep, _ := d["deployment"].(map[string]any); dep != nil {
		d = dep
	}
	if d["commit_sha"] != sha || d["commit_message"] != "Fix the header" || d["trigger"] != "webhook" {
		t.Fatalf("deployment: %v", d)
	}

	// Watch paths are checked against the files of the pushed commits.
	app["watch_paths"] = "api/**"
	e.expect(c, "PATCH", fmt.Sprintf("/api/applications/%d", id), app, 200)
	if _, out := push(secret, azurePush("main", strings.Repeat("b", 40), "docs")); out["message"] != "no changes in watch paths" {
		t.Fatalf("watch paths: %v", out)
	}
	if got := e.expect(c, "GET", fmt.Sprintf("/api/applications/%d", id), nil, 200); got["azure_hook_id"] != "hook-1,hook-2,hook-3" {
		t.Fatalf("an update must keep the hook: %v", got)
	}

	// Pull requests targeting the branch get preview deployments.
	pr := func(event, status, target, sha string) map[string]any {
		return map[string]any{"eventType": event, "resource": map[string]any{
			"pullRequestId": 7, "status": status, "title": "Login page", "sourceRefName": "refs/heads/feature/login",
			"targetRefName": "refs/heads/" + target, "lastMergeSourceCommit": map[string]string{"commitId": sha},
		}}
	}
	prSHA := strings.Repeat("d", 40)
	if _, out := push(secret, pr("git.pullrequest.created", "active", "main", prSHA)); out["status"] != "ignored" {
		t.Fatalf("previews are off: %v", out)
	}
	app["preview_enabled"] = true
	e.expect(c, "PATCH", fmt.Sprintf("/api/applications/%d", id), app, 200)
	if _, out := push(secret, pr("git.pullrequest.created", "active", "dev", prSHA)); out["status"] != "ignored" {
		t.Fatalf("pull request to another branch: %v", out)
	}
	_, out = push(secret, pr("git.pullrequest.created", "active", "main", prSHA))
	if out["status"] != "queued" {
		t.Fatalf("pull request: %v", out)
	}
	d = e.expect(c, "GET", fmt.Sprintf("/api/deployments/%v", out["deployment_id"]), nil, 200)
	if dep, _ := d["deployment"].(map[string]any); dep != nil {
		d = dep
	}
	if d["pr_number"].(float64) != 7 || d["branch"] != "feature/login" || d["commit_sha"] != prSHA {
		t.Fatalf("preview deployment: %v", d)
	}
	if _, out := push(secret, pr("git.pullrequest.updated", "active", "main", prSHA)); out["status"] != "ignored" {
		t.Fatalf("a vote or a new title must not redeploy: %v", out)
	}
	if _, out := push(secret, pr("git.pullrequest.updated", "active", "main", strings.Repeat("e", 40))); out["status"] != "queued" {
		t.Fatalf("a new commit on the pull request: %v", out)
	}
	if _, out := push(secret, pr("git.pullrequest.updated", "completed", "main", prSHA)); out["status"] != "cleaned" {
		t.Fatalf("completed pull request: %v", out)
	}

	// A new webhook secret replaces the hooks, which carry it.
	e.expect(c, "POST", fmt.Sprintf("/api/applications/%d/webhook-secret", id), nil, 200)
	info := e.expect(c, "GET", fmt.Sprintf("/api/applications/%d/azure-hook", id), nil, 200)
	if info["hook_id"] != "hook-4,hook-5,hook-6" || info["hooks"].(float64) != 3 || az.hook("hook-1") != nil || az.hook("hook-3") != nil || info["url"] != e.srv.URL+"/api/webhooks/azure/"+uuid {
		t.Fatalf("hook after new secret: %v (deleted %v)", info, az.deleted)
	}
	if info = e.expect(c, "POST", fmt.Sprintf("/api/applications/%d/azure-hook", id), nil, 200); info["hook_id"] != "hook-7,hook-8,hook-9" || az.hook("hook-6") != nil {
		t.Fatalf("recreated hook: %v", info)
	}

	// A connection in use can't be removed; deleting the application removes its hook.
	e.expect(c, "DELETE", fmt.Sprintf("/api/azure/connections/%d", cid), nil, 409)
	e.expect(c, "DELETE", fmt.Sprintf("/api/applications/%d", id), nil, 200)
	if az.hook("hook-7") != nil || az.hook("hook-9") != nil {
		t.Fatalf("hook left behind after deleting the application: %v", az.deleted)
	}
	e.expect(c, "DELETE", fmt.Sprintf("/api/azure/connections/%d", cid), nil, 200)
}

func TestAzureBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"contoso":                           "https://dev.azure.com/contoso",
		"https://dev.azure.com/contoso/":    "https://dev.azure.com/contoso",
		"https://contoso.visualstudio.com":  "https://contoso.visualstudio.com",
		"https://tfs.corp.local/tfs/Main?x": "https://tfs.corp.local/tfs/Main",
		"https://me@dev.azure.com/contoso":  "https://dev.azure.com/contoso",
	} {
		if got, err := azuredevops.BaseURL(in); err != nil || got != want {
			t.Errorf("BaseURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "a b", "ftp://x", "contoso/Shop"} {
		if _, err := azuredevops.BaseURL(bad); err == nil {
			t.Errorf("BaseURL(%q) accepted", bad)
		}
	}
	if got := azuredevops.CloneURL("https://dev.azure.com/contoso/Shop/_git/web", "t0k"); got != "https://pat:t0k@dev.azure.com/contoso/Shop/_git/web" {
		t.Errorf("CloneURL = %s", got)
	}
}
