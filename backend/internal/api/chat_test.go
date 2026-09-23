package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/portdock/portdock/internal/assistant"
)

// fakeOpenRouter answers the OpenRouter endpoints the assistant uses; streamed
// replies echo the number of messages received (system prompt + history).
func fakeOpenRouter(t *testing.T) *[]string {
	t.Helper()
	var systemPrompts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-or-good" && r.URL.Path != "/models" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":{"message":"invalid key"}}`)
			return
		}
		switch r.URL.Path {
		case "/models":
			fmt.Fprint(w, `{"data":[{"id":"openai/gpt-4o","name":"GPT-4o","context_length":128000,"pricing":{"prompt":"0.0000025","completion":"0.00001"}}]}`)
		case "/key":
			fmt.Fprint(w, `{"data":{"label":"sk-or-v1-goo...","usage":0.5,"limit":10}}`)
		case "/chat/completions":
			var in struct {
				Stream   bool                `json:"stream"`
				Messages []assistant.Message `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			if !in.Stream {
				fmt.Fprint(w, `{"choices":[{"message":{"content":"OK"}}]}`)
				return
			}
			systemPrompts = append(systemPrompts, in.Messages[0].Content)
			w.Header().Set("Content-Type", "text/event-stream")
			last := in.Messages[len(in.Messages)-1].Content
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"You said: \"}}]}\n\n")
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", fmt.Sprintf("%s (%d messages)", last, len(in.Messages)))
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}))
	t.Cleanup(srv.Close)
	old := assistant.BaseURL
	assistant.BaseURL = srv.URL
	t.Cleanup(func() { assistant.BaseURL = old })
	return &systemPrompts
}

type sseEvent struct {
	Name string
	Data map[string]any
}

func (e *testEnv) chatSend(c *http.Client, convID int64, content string) (int, []sseEvent) {
	e.t.Helper()
	b, _ := json.Marshal(map[string]any{"conversation_id": convID, "content": content})
	res, err := c.Post(e.srv.URL+"/api/chat/send", "application/json", strings.NewReader(string(b)))
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		return res.StatusCode, nil
	}
	var out []sseEvent
	for _, block := range strings.Split(string(raw), "\n\n") {
		var ev sseEvent
		for _, line := range strings.Split(block, "\n") {
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				ev.Name = v
			}
			if v, ok := strings.CutPrefix(line, "data: "); ok {
				_ = json.Unmarshal([]byte(v), &ev.Data)
			}
		}
		if ev.Name != "" {
			out = append(out, ev)
		}
	}
	return 200, out
}

func TestAssistant(t *testing.T) {
	prompts := fakeOpenRouter(t)
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("bob@corp.com", "correct-horse", "member")
	ann := e.user("ann@corp.com", "correct-horse", "member")
	_, _ = e.db.Exec("UPDATE users SET locale = 'ro' WHERE id = ?", ann.ID)
	owner, cb, ca := e.login("owner@corp.com"), e.login("bob@corp.com"), e.login("ann@corp.com")

	// Off until configured; settings are admin-only and never return the key.
	if st := e.expect(cb, "GET", "/api/chat/status", nil, 200); st["allowed"] != false || st["enabled"] != false {
		t.Fatalf("status before setup: %v", st)
	}
	e.expect(cb, "GET", "/api/chat/settings", nil, 403)
	e.expect(cb, "POST", "/api/chat/send", map[string]any{"content": "hi"}, 403)
	e.expect(owner, "PUT", "/api/chat/settings", map[string]any{"model": "not a model!"}, 400)
	e.expect(owner, "PUT", "/api/chat/settings", map[string]any{"access": "everyone"}, 400)
	st := e.expect(owner, "PUT", "/api/chat/settings", map[string]any{"enabled": true, "api_key": "sk-or-good", "model": "openai/gpt-4o", "access": "selected", "user_ids": []int64{ann.ID, 999}, "instructions": "Mention the on-call channel #ops."}, 200)
	if st["api_key_set"] != true || st["api_key"] != nil || fmt.Sprint(st["user_ids"]) != fmt.Sprintf("[%d]", ann.ID) {
		t.Fatalf("settings: %v", st)
	}
	var stored string
	_ = e.db.QueryRow("SELECT value FROM settings WHERE key = 'chat_api_key'").Scan(&stored)
	if stored == "" || strings.Contains(stored, "sk-or-good") {
		t.Fatalf("the key must be stored encrypted: %q", stored)
	}

	// Models and the connection test.
	if m := e.expect(owner, "GET", "/api/chat/models", nil, 200); len(m["models"].([]any)) != 1 {
		t.Fatalf("models: %v", m)
	}
	if r := e.expect(owner, "POST", "/api/chat/test", map[string]any{}, 200); r["reply"] != "OK" || r["label"] == "" {
		t.Fatalf("test: %v", r)
	}
	e.expect(owner, "POST", "/api/chat/test", map[string]any{"api_key": "sk-or-bad"}, 400)

	// Only selected users may chat; admins too only when selected.
	if st := e.expect(cb, "GET", "/api/chat/status", nil, 200); st["allowed"] != false || st["enabled"] != true {
		t.Fatalf("bob: %v", st)
	}
	if code, _ := e.chatSend(cb, 0, "hi"); code != 403 {
		t.Fatalf("bob must not chat: %d", code)
	}
	if code, _ := e.chatSend(owner, 0, "hi"); code != 403 {
		t.Fatalf("admins aren't selected: %d", code)
	}

	// A new conversation streams the reply and keeps the history.
	code, evs := e.chatSend(ca, 0, "How do I add a domain?\nThanks")
	if code != 200 || len(evs) < 4 || evs[0].Name != "conversation" || evs[1].Name != "user" || evs[len(evs)-1].Name != "done" {
		t.Fatalf("stream: %d %+v", code, evs)
	}
	conv := evs[0].Data
	if conv["title"] != "How do I add a domain?" {
		t.Errorf("title: %v", conv["title"])
	}
	if done := evs[len(evs)-1].Data; done["role"] != "assistant" || !strings.Contains(done["content"].(string), "(2 messages)") || done["model"] != "openai/gpt-4o" {
		t.Errorf("reply: %v", done)
	}
	id := int64(conv["id"].(float64))
	_, evs = e.chatSend(ca, id, "And HTTPS?")
	if done := evs[len(evs)-1].Data; !strings.Contains(done["content"].(string), "(4 messages)") {
		t.Errorf("the history must be sent: %v", done)
	}
	if p := (*prompts)[0]; !strings.Contains(p, "PortDock") || !strings.Contains(p, "Romanian") || !strings.Contains(p, "#ops") {
		t.Errorf("system prompt: %s", p)
	}
	full := e.expect(ca, "GET", fmt.Sprintf("/api/chat/conversations/%d", id), nil, 200)
	if n := len(full["messages"].([]any)); n != 4 {
		t.Errorf("stored messages: %d", n)
	}
	if list := anyList(e.expect(ca, "GET", "/api/chat/conversations", nil, 200)); len(list) != 1 {
		t.Errorf("conversations: %v", list)
	}

	// Conversations are private.
	_, _ = e.db.Exec("UPDATE settings SET value = 'all' WHERE key = 'chat_access'")
	e.expect(cb, "GET", fmt.Sprintf("/api/chat/conversations/%d", id), nil, 404)
	e.expect(cb, "DELETE", fmt.Sprintf("/api/chat/conversations/%d", id), nil, 404)
	if code, _ := e.chatSend(cb, id, "hijack"); code != 404 {
		t.Errorf("posting into someone else's conversation: %d", code)
	}
	e.expect(ca, "DELETE", fmt.Sprintf("/api/chat/conversations/%d", id), nil, 200)
	var left int
	_ = e.db.QueryRow("SELECT COUNT(*) FROM chat_messages").Scan(&left)
	if left != 0 {
		t.Errorf("messages must go with their conversation: %d left", left)
	}
	if code, _ := e.chatSend(ca, 0, strings.Repeat("x", 20_001)); code != 400 {
		t.Errorf("too long: %d", code)
	}
}

// anyList decodes a JSON array response (testEnv.do keeps non-objects in _raw).
func anyList(m map[string]any) []any {
	if v, ok := m["_raw"].(string); ok {
		var out []any
		_ = json.Unmarshal([]byte(v), &out)
		return out
	}
	return nil
}
