package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/portdock/portdock/internal/assistant"
)

// fakeToolModel is an OpenAI-compatible model that uses tools: the last user
// message says which tool it calls; once a tool result is in the conversation
// it answers in text with that result. toolsSeen counts the requests that
// offered tools.
func fakeToolModel(t *testing.T) (toolsSeen *int) {
	t.Helper()
	seen := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var in struct {
			Stream   bool                `json:"stream"`
			Messages []assistant.Message `json:"messages"`
			Tools    []assistant.Tool    `json:"tools"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if !in.Stream {
			fmt.Fprint(w, `{"choices":[{"message":{"content":"OK"}}]}`)
			return
		}
		if len(in.Tools) > 0 {
			seen++
		}
		w.Header().Set("Content-Type", "text/event-stream")
		var lastUser, lastTool string
		for _, m := range in.Messages {
			switch m.Role {
			case "user":
				lastUser = m.Content
			case "tool":
				lastTool = m.Content
			}
		}
		text := func(s string) {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", s)
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
		call := func(id, name, args string) {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%q}}]}}]}\n\n", id, name, args)
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
		switch {
		case lastTool != "":
			text("Result: " + lastTool)
		case len(in.Tools) == 0:
			text("No tools here.")
		case strings.Contains(lastUser, "projects"):
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Looking.\"}}]}\n\n")
			call("c1", "list_projects", "{}")
		case strings.Contains(lastUser, "restart"):
			call("c2", "restart_application", `{"app":"nope"}`)
		case strings.Contains(lastUser, "forbidden"):
			call("c3", "delete_container", `{"container":"x"}`)
		case strings.Contains(lastUser, "docs"):
			call("c4", "search_docs", `{"query":"canary rollback"}`)
		default:
			text("Hello.")
		}
	}))
	t.Cleanup(srv.Close)
	old := assistant.BaseURL
	assistant.BaseURL = srv.URL
	t.Cleanup(func() { assistant.BaseURL = old })
	return &seen
}

func eventsNamed(evs []sseEvent, name string) []sseEvent {
	var out []sseEvent
	for _, e := range evs {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

func TestAssistantTools(t *testing.T) {
	seen := fakeToolModel(t)
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("bob@corp.com", "correct-horse", "member")
	owner, bob := e.login("owner@corp.com"), e.login("bob@corp.com")
	e.expect(owner, "PUT", "/api/chat/settings", map[string]any{"enabled": true, "api_key": "sk-or-good", "model": "openai/gpt-4o", "access": "all"}, 200)
	if code, _ := e.do(owner, "POST", "/api/projects", map[string]any{"name": "shop"}); code >= 300 {
		t.Fatalf("create project: %d", code)
	}
	if st := e.expect(owner, "GET", "/api/chat/status", nil, 200); st["tools"] != true || st["actions"] != true {
		t.Fatalf("status: %v", st)
	}

	// A read tool runs at once; its result reaches the model and the steps are kept with the reply.
	code, evs := e.chatSend(owner, 0, "list my projects")
	if code != 200 {
		t.Fatalf("send: %d", code)
	}
	tools := eventsNamed(evs, "tool")
	if len(tools) != 2 || tools[0].Data["tool"] != "list_projects" || tools[0].Data["status"] != "running" || tools[1].Data["status"] != "done" {
		t.Fatalf("tool events: %+v", tools)
	}
	done := evs[len(evs)-1]
	content, _ := done.Data["content"].(string)
	if done.Name != "done" || !strings.HasPrefix(content, "Looking.") || !strings.Contains(content, "Result:") || !strings.Contains(content, "shop") {
		t.Fatalf("reply: %s %q", done.Name, content)
	}
	meta, _ := done.Data["meta"].(map[string]any)
	steps, _ := meta["steps"].([]any)
	if len(steps) != 1 || steps[0].(map[string]any)["status"] != "done" {
		t.Fatalf("meta: %v", meta)
	}
	if *seen == 0 {
		t.Fatal("the model was not offered any tool")
	}
	convID := int64(evs[0].Data["id"].(float64))

	// The documentation is searched locally.
	_, evs = e.chatSend(owner, convID, "search the docs")
	if content, _ = evs[len(evs)-1].Data["content"].(string); !strings.Contains(strings.ToLower(content), "canary") {
		t.Fatalf("docs reply: %q", content)
	}

	// A tool the assistant does not have is refused without executing anything.
	_, evs = e.chatSend(owner, convID, "do the forbidden thing")
	if tools = eventsNamed(evs, "tool"); len(tools) != 1 || tools[0].Data["status"] != "error" || tools[0].Data["detail"] != "unknown tool" {
		t.Fatalf("unknown tool: %+v", tools)
	}
	if content, _ = evs[len(evs)-1].Data["content"].(string); !strings.Contains(content, "there is no tool") {
		t.Fatalf("unknown tool reply: %q", content)
	}

	// An action only becomes a pending confirmation.
	_, evs = e.chatSend(owner, convID, "please restart web")
	actions := eventsNamed(evs, "action")
	if len(actions) != 1 || actions[0].Data["status"] != "pending" || actions[0].Data["tool"] != "restart_application" {
		t.Fatalf("action event: %+v", actions)
	}
	actionID, _ := actions[0].Data["action_id"].(string)
	if actionID == "" {
		t.Fatal("no action id")
	}
	if content, _ = evs[len(evs)-1].Data["content"].(string); !strings.Contains(content, "awaiting_confirmation") {
		t.Fatalf("action reply: %q", content)
	}
	// Someone else cannot confirm it; the owner can, and it runs with their rights (here: no such application).
	e.expect(bob, "POST", "/api/chat/actions/"+actionID+"/confirm", map[string]any{}, 410)
	res := e.expect(owner, "POST", "/api/chat/actions/"+actionID+"/confirm", map[string]any{}, 200)
	if res["status"] != "failed" || !strings.Contains(fmt.Sprint(res["detail"]), "no application") {
		t.Fatalf("confirm: %v", res)
	}
	e.expect(owner, "POST", "/api/chat/actions/"+actionID+"/confirm", map[string]any{}, 410) // one shot
	full := e.expect(owner, "GET", fmt.Sprintf("/api/chat/conversations/%d", convID), nil, 200)
	msgs := full["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	lastSteps := last["meta"].(map[string]any)["steps"].([]any)
	if lastSteps[0].(map[string]any)["status"] != "failed" {
		t.Fatalf("stored step: %v", last["meta"])
	}
	var audited int
	_ = e.db.QueryRow("SELECT COUNT(*) FROM activity WHERE action = 'assistant.action'").Scan(&audited)
	if audited != 1 {
		t.Errorf("audit entries: %d", audited)
	}

	// Cancelling records it too.
	_, evs = e.chatSend(owner, convID, "please restart web")
	actionID, _ = eventsNamed(evs, "action")[0].Data["action_id"].(string)
	if res := e.expect(owner, "POST", "/api/chat/actions/"+actionID+"/cancel", map[string]any{}, 200); res["status"] != "cancelled" {
		t.Fatalf("cancel: %v", res)
	}

	// Administrators can turn actions, then tools, off.
	e.expect(owner, "PUT", "/api/chat/settings", map[string]any{"actions_enabled": false}, 200)
	if st := e.expect(owner, "GET", "/api/chat/status", nil, 200); st["actions"] != false || st["tools"] != true {
		t.Fatalf("status without actions: %v", st)
	}
	_, evs = e.chatSend(owner, convID, "please restart web")
	if tools = eventsNamed(evs, "tool"); len(eventsNamed(evs, "action")) != 0 || len(tools) != 1 || tools[0].Data["status"] != "error" {
		t.Fatalf("actions off: %+v", evs)
	}
	e.expect(owner, "PUT", "/api/chat/settings", map[string]any{"tools_enabled": false}, 200)
	if st := e.expect(owner, "GET", "/api/chat/status", nil, 200); st["tools"] != false || st["actions"] != false {
		t.Fatalf("status without tools: %v", st)
	}
	before := *seen
	_, evs = e.chatSend(owner, convID, "list my projects")
	if content, _ = evs[len(evs)-1].Data["content"].(string); content != "No tools here." || *seen != before {
		t.Fatalf("tools off: %q seen=%d", content, *seen-before)
	}
}

func TestChatGuard(t *testing.T) {
	cases := []struct {
		method, path string
		allowed      bool
	}{
		{"GET", "/api/projects", true},
		{"GET", "/api/servers/1/containers/abc/logs?tail=10", true},
		{"DELETE", "/api/projects/1", false},
		{"DELETE", "/api/volumes/1", false},
		{"PATCH", "/api/applications/3", false},
		{"PUT", "/api/settings", false},
		{"POST", "/api/applications/3/deploy", true},
		{"POST", "/api/applications/3/restart", true},
		{"POST", "/api/applications/3/stop", false},
		{"POST", "/api/deployments/7/rollback", true},
		{"POST", "/api/databases/2/backup-now?now=1", true},
		{"POST", "/api/databases/2/recreate", false},
		{"POST", "/api/servers/1/containers/abc123/restart", true},
		{"POST", "/api/servers/1/containers/abc123/stop", false},
		{"POST", "/api/servers/1/containers/abc123/remove", false},
		{"POST", "/api/servers/1/prune", false},
		{"POST", "/api/jobs/4/run", true},
		{"POST", "/api/users", false},
		{"POST", "/api/tokens", false},
	}
	for _, c := range cases {
		if err := chatCallAllowed(c.method, c.path); (err == nil) != c.allowed {
			t.Errorf("%s %s: allowed=%v, err=%v", c.method, c.path, err == nil, err)
		}
	}
	// No tool deletes, removes, stops, kills, pauses or prunes anything, and
	// the read-only tool list carries no action.
	for _, tl := range chatToolList {
		for _, bad := range []string{"delete", "remove", "stop", "kill", "prune", "pause", "exec", "terminal"} {
			if strings.Contains(tl.Name, bad) {
				t.Errorf("tool %q must not exist", tl.Name)
			}
		}
		if tl.Run == nil || tl.Title == nil {
			t.Errorf("tool %q incomplete", tl.Name)
		}
	}
	for _, d := range chatToolDefs(false) {
		if chatToolByName(d.Function.Name).Action {
			t.Errorf("%s offered while actions are off", d.Function.Name)
		}
	}
	if len(chatToolDefs(true)) <= len(chatToolDefs(false)) {
		t.Error("actions missing from the full tool list")
	}
	// The guard applies inside s.call, before any handler runs.
	e := newTestEnv(t)
	r := httptest.NewRequest("GET", "/", nil).WithContext(withChatGuard(context.Background()))
	if err := e.api.call(r, "DELETE", "/api/projects/1", nil, nil); !errors.Is(err, errChatForbidden) {
		t.Fatalf("DELETE through the guard: %v", err)
	}
	if err := e.api.call(r, "POST", "/api/servers/1/prune", map[string]any{}, nil); !errors.Is(err, errChatForbidden) {
		t.Fatalf("prune through the guard: %v", err)
	}
}

func TestChatToolHelpers(t *testing.T) {
	masked := maskSecrets("db=postgres://app:s3cret@db:5432/x password=abc123 Authorization: Bearer tok_456 api_key = \"k-789\" user=app")
	for _, leak := range []string{"s3cret", "abc123", "tok_456", "k-789"} {
		if strings.Contains(masked, leak) {
			t.Errorf("%q leaked in %q", leak, masked)
		}
	}
	if !strings.Contains(masked, "user=app") || !strings.Contains(masked, "postgres://app:***@db") {
		t.Errorf("over-masked: %q", masked)
	}
	freq := frequentLines([]string{
		"2026-10-22T10:00:01Z connection refused to 10.0.0.5:5432",
		"2026-10-22T10:00:03Z connection refused to 10.0.0.5:5432",
		"2026-10-22T10:00:09Z connection refused to 10.0.0.7:5432",
		"2026-10-22T10:00:10Z started",
	}, 3)
	if len(freq) != 1 || !strings.HasPrefix(freq[0], "3× connection refused") {
		t.Errorf("frequent lines: %v", freq)
	}
	var inspect map[string]any
	_ = json.Unmarshal([]byte(`{"Name":"/web-1","Id":"abcdef0123456789","Created":"2026-10-22T09:00:00Z","RestartCount":3,
		"State":{"Status":"exited","Running":false,"OOMKilled":true,"ExitCode":137,"Health":{"Status":"unhealthy","FailingStreak":4,"Log":[{"Start":"t1","ExitCode":1,"Output":"curl: (7) password=zzz"}]}},
		"Config":{"Image":"shop/web:1","Env":["DATABASE_URL=postgres://u:p@h/db","PORT=3000"],"Cmd":["node","server.js","--token=abc"],"Labels":{"portdock.kind":"application","com.example.other":"x"}},
		"HostConfig":{"Memory":268435456,"RestartPolicy":{"Name":"unless-stopped"},"LogConfig":{"Type":"json-file"}},
		"Mounts":[{"Type":"volume","Name":"pd-x_data","Destination":"/data","RW":true}],
		"NetworkSettings":{"Networks":{"portdock":{"IPAddress":"172.18.0.9","Aliases":["web"]}}}}`), &inspect)
	sum := summarizeInspect(inspect)
	b, _ := json.Marshal(sum)
	out := string(b)
	for _, leak := range []string{"postgres://u:p@h", "zzz", "--token=abc"} {
		if strings.Contains(out, leak) {
			t.Errorf("inspect leaked %q: %s", leak, out)
		}
	}
	for _, want := range []string{`"env_keys":["DATABASE_URL","PORT"]`, `"OOMKilled":true`, `"ExitCode":137`, `"restart_count":3`, `"portdock.kind"`, `"pd-x_data"`, `"172.18.0.9"`} {
		if !strings.Contains(out, want) {
			t.Errorf("inspect summary missing %s: %s", want, out)
		}
	}
	if strings.Contains(out, "com.example.other") {
		t.Errorf("unrelated labels kept: %s", out)
	}
	if step := chatStepNote(&chatMeta{Steps: []chatStep{{Tool: "restart_application", Title: "Restart application web", Status: "confirmed", Detail: `{"restarted":true}`}, {Title: "Read the logs of container web-1", Status: "done"}}}); !strings.Contains(step, "confirmed by the user and run") || !strings.Contains(step, "web-1 — done") {
		t.Errorf("step note: %s", step)
	}
}
