package assistant

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The model asks for two tools, their arguments split across chunks (the
// OpenAI streaming format); a server without an index on the deltas is
// handled too, and a model that refuses tools is reported as such.
func TestStreamTools(t *testing.T) {
	var gotTools int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if tools, _ := body["tools"].([]any); len(tools) > 0 {
			gotTools = len(tools)
			if m, _ := body["model"].(string); m == "no-tools" {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":{"message":"model does not support tools"}}`)
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Let me check.\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_a\",\"type\":\"function\",\"function\":{\"name\":\"list_containers\",\"arguments\":\"\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"server\\\": \"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"prod\\\"}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"call_b\",\"type\":\"function\",\"function\":{\"name\":\"search_docs\",\"arguments\":\"{\\\"query\\\":\\\"canary\\\"}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	c := New()
	p := Provider{BaseURL: srv.URL}
	tools := []Tool{{Type: "function", Function: ToolFunction{Name: "list_containers", Parameters: map[string]any{"type": "object"}}}}
	var text strings.Builder
	reply, calls, err := c.StreamTools(t.Context(), p, "m", []Message{{Role: "user", Content: "hi"}}, tools, func(s string) error { text.WriteString(s); return nil })
	if err != nil || reply != "Let me check." || text.String() != reply || gotTools != 1 {
		t.Fatalf("stream: %q %v tools=%d", reply, err, gotTools)
	}
	if len(calls) != 2 || calls[0].ID != "call_a" || calls[0].Function.Name != "list_containers" || calls[0].Function.Arguments != `{"server": "prod"}` ||
		calls[1].ID != "call_b" || calls[1].Function.Arguments != `{"query":"canary"}` {
		t.Fatalf("calls: %+v", calls)
	}
	if _, _, err := c.StreamTools(t.Context(), p, "no-tools", nil, tools, func(string) error { return nil }); err == nil {
		t.Fatal("expected an error")
	} else if _, ok := err.(*ToolsUnsupportedError); !ok {
		t.Fatalf("expected ToolsUnsupportedError, got %T %v", err, err)
	}
	// Without tools the plain Stream keeps working and no tools are sent.
	gotTools = 0
	if _, err := c.Stream(t.Context(), p, "m", []Message{{Role: "user", Content: "hi"}}, func(string) error { return nil }); err != nil || gotTools != 0 {
		t.Fatalf("plain stream: %v tools=%d", err, gotTools)
	}
}

func TestStreamToolsWithoutIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"x\",\"function\":{\"name\":\"a\",\"arguments\":\"{\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"arguments\":\"}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"y\",\"function\":{\"name\":\"b\",\"arguments\":\"{}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	_, calls, err := New().StreamTools(t.Context(), Provider{BaseURL: srv.URL}, "m", nil, []Tool{{Type: "function"}}, func(string) error { return nil })
	if err != nil || len(calls) != 2 || calls[0].Function.Arguments != "{}" || calls[0].ID != "x" || calls[1].Function.Name != "b" || calls[1].ID != "y" {
		t.Fatalf("calls: %+v %v", calls, err)
	}
}
