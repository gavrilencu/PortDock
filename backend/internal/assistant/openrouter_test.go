package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeOpenRouter(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			fmt.Fprint(w, `{"data":[{"id":"openai/gpt-4o","name":"OpenAI: GPT-4o","context_length":128000,"description":"x","pricing":{"prompt":"0.0000025","completion":"0.00001"}},
				{"id":"meta/llama:free","name":"Llama (free)","context_length":8192,"pricing":{"prompt":"0","completion":"0"}}]}`)
		case "/key":
			if r.Header.Get("Authorization") != "Bearer good" {
				w.WriteHeader(401)
				fmt.Fprint(w, `{"error":{"message":"No auth credentials found","code":401}}`)
				return
			}
			fmt.Fprint(w, `{"data":{"label":"sk-or-v1-abc...","usage":1.25,"limit":null}}`)
		case "/chat/completions":
			var in struct {
				Model    string    `json:"model"`
				Stream   bool      `json:"stream"`
				Messages []Message `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			if r.Header.Get("X-Title") != "PortDock" || r.Header.Get("HTTP-Referer") != "https://portdock.example.com" {
				t.Errorf("missing attribution headers: %v", r.Header)
			}
			if in.Model == "missing/model" {
				w.WriteHeader(404)
				fmt.Fprint(w, `{"error":{"message":"missing/model is not a valid model ID"}}`)
				return
			}
			if !in.Stream {
				fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":" OK "}}]}`)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, ": OPENROUTER PROCESSING\n\n")
			for _, part := range []string{"Hel", "lo", " world"} {
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", part)
			}
			if in.Model == "broken/model" {
				fmt.Fprint(w, "data: {\"error\":{\"message\":\"provider overloaded\"}}\n\n")
				return
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	old := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = old })
	return srv
}

func TestClient(t *testing.T) {
	fakeOpenRouter(t)
	c := New()
	c.Referer = func() string { return "https://portdock.example.com" }
	ctx := context.Background()

	models, err := c.Models(ctx)
	if err != nil || len(models) != 2 || models[0].ID != "openai/gpt-4o" || models[0].PromptPrice != "0.0000025" || models[1].CompletionPrice != "0" {
		t.Fatalf("models: %+v %v", models, err)
	}
	if info, err := c.Key(ctx, "good"); err != nil || info.Label == "" || info.Usage != 1.25 || info.Limit != nil {
		t.Fatalf("key: %+v %v", info, err)
	}
	if _, err := c.Key(ctx, "bad"); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("a bad key must be rejected clearly, got %v", err)
	}
	if reply, err := c.Complete(ctx, "good", "openai/gpt-4o", []Message{{Role: "user", Content: "hi"}}, 5); err != nil || reply != "OK" {
		t.Fatalf("complete: %q %v", reply, err)
	}

	var deltas []string
	full, err := c.Stream(ctx, "good", "openai/gpt-4o", []Message{{Role: "user", Content: "hi"}}, func(s string) error {
		deltas = append(deltas, s)
		return nil
	})
	if err != nil || full != "Hello world" || len(deltas) != 3 {
		t.Fatalf("stream: %q %v %v", full, deltas, err)
	}
	full, err = c.Stream(ctx, "good", "broken/model", nil, func(string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "provider overloaded") || full != "Hello world" {
		t.Fatalf("mid-stream errors keep the partial reply: %q %v", full, err)
	}
	if _, err := c.Stream(ctx, "good", "missing/model", nil, func(string) error { return nil }); err == nil || !strings.Contains(err.Error(), "not a valid model") {
		t.Fatalf("unknown model: %v", err)
	}
}
