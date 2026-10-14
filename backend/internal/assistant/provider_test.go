package assistant

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A self-hosted OpenAI-compatible server (Ollama, vLLM, llama.cpp): no key,
// no OpenRouter headers, its own model list.
func TestLocalProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("HTTP-Referer") != "" {
			http.Error(w, "unexpected headers", 400)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"object":"list","data":[{"id":"qwen2.5:7b","object":"model"}]}`)
		case "/v1/chat/completions":
			if strings.Contains(r.URL.RawQuery, "fail") {
				http.Error(w, `{"error":{"message":"model not loaded"}}`, 404)
				return
			}
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New()
	c.Referer = func() string { return "https://portdock.example.com" }
	p := Provider{BaseURL: srv.URL + "/v1/"}
	models, err := c.Models(t.Context(), p)
	if err != nil || len(models) != 1 || models[0].ID != "qwen2.5:7b" {
		t.Fatalf("models: %v %v", models, err)
	}
	if reply, err := c.Complete(t.Context(), p, "qwen2.5:7b", []Message{{Role: "user", Content: "hi"}}, 5); err != nil || reply != "OK" {
		t.Fatalf("complete: %q %v", reply, err)
	}
	down := Provider{BaseURL: "http://127.0.0.1:1/v1"}
	if _, err := c.Complete(t.Context(), down, "m", nil, 5); err == nil || !strings.HasPrefix(err.Error(), "The AI server is unreachable") {
		t.Errorf("down: %v", err)
	}
	// OpenRouter keeps its name in messages.
	if (Provider{}).Name() != "OpenRouter" || p.OpenRouter() {
		t.Error("provider names")
	}
}
