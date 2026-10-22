// Package assistant talks to the model of PortDock's built-in AI assistant:
// OpenRouter, an OpenAI-compatible gateway to hundreds of models, or any
// OpenAI-compatible server the organization runs itself (Ollama, vLLM,
// LiteLLM, LocalAI, llama.cpp), so the assistant also works offline.
package assistant

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// BaseURL is the OpenRouter API; tests point it at a fake server.
var BaseURL = "https://openrouter.ai/api/v1"

const modelsTTL = time.Hour

// Model is an entry of the OpenRouter catalog. Prices are USD per token.
type Model struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ContextLength   int    `json:"context_length"`
	PromptPrice     string `json:"prompt_price"`
	CompletionPrice string `json:"completion_price"`
	Description     string `json:"description,omitempty"`
}

// Message is a chat message sent to the model. An assistant message may carry
// the tool calls the model asked for; a tool message answers one of them.
type Message struct {
	Role       string     `json:"role"` // system | user | assistant | tool
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is a function call requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall names the function and carries its arguments as JSON text.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool is a function the model may call, in the OpenAI format that OpenRouter,
// Ollama, vLLM and the other compatible servers understand.
type Tool struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction describes one function: its name, what it does and a JSON
// schema of its arguments.
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ToolsUnsupportedError reports a model (or server) that refuses requests with
// tools, so the caller can ask again without them.
type ToolsUnsupportedError struct{ Err error }

func (e *ToolsUnsupportedError) Error() string { return e.Err.Error() }
func (e *ToolsUnsupportedError) Unwrap() error { return e.Err }

func toolsUnsupported(err error) bool {
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "tool") {
		return false
	}
	return strings.Contains(msg, "http 400") || strings.Contains(msg, "not support") || strings.Contains(msg, "unsupported") || strings.Contains(msg, "unknown field") || strings.Contains(msg, "invalid")
}

// KeyInfo describes an OpenRouter API key.
type KeyInfo struct {
	Label string   `json:"label"`
	Usage float64  `json:"usage"` // credits used, USD
	Limit *float64 `json:"limit"` // nil = no limit
}

// Provider is where the model runs: OpenRouter (no BaseURL) or an
// OpenAI-compatible server at BaseURL (e.g. http://ollama:11434/v1), for
// which the key is optional.
type Provider struct {
	BaseURL string
	Key     string
}

func (p Provider) base() string {
	if p.BaseURL != "" {
		return strings.TrimRight(p.BaseURL, "/")
	}
	return BaseURL
}

// OpenRouter reports whether the provider is OpenRouter.
func (p Provider) OpenRouter() bool { return p.BaseURL == "" }

func (p Provider) name() string {
	if p.OpenRouter() {
		return "OpenRouter"
	}
	return "the AI server"
}

// Name is how messages refer to the provider, capitalized.
func (p Provider) Name() string {
	n := p.name()
	return strings.ToUpper(n[:1]) + n[1:]
}

// Client calls the provider. Referer returns the URL of the PortDock instance
// (OpenRouter shows it as the app in usage statistics).
type Client struct {
	HTTP    *http.Client
	Referer func() string

	mu     sync.Mutex
	models map[string]cachedModels // by base URL
}

type cachedModels struct {
	list []Model
	at   time.Time
}

func New() *Client { return &Client{HTTP: &http.Client{}, models: map[string]cachedModels{}} }

func (c *Client) do(ctx context.Context, method, path string, p Provider, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base()+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.Key != "" {
		req.Header.Set("Authorization", "Bearer "+p.Key)
	}
	if c.Referer != nil && p.OpenRouter() {
		if ref := c.Referer(); ref != "" {
			req.Header.Set("HTTP-Referer", ref)
		}
	}
	req.Header.Set("X-Title", "PortDock")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s is unreachable: %w", p.Name(), err)
	}
	if res.StatusCode >= 300 {
		defer res.Body.Close()
		return nil, apiError(res, p)
	}
	return res, nil
}

// apiError turns an error response of the provider into a readable error.
func apiError(res *http.Response, p Provider) error {
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &body) == nil && body.Error.Message != "" {
		msg = body.Error.Message
	}
	switch res.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("the %s API key was rejected: %s", strings.TrimPrefix(p.name(), "the "), msg)
	case http.StatusPaymentRequired:
		return fmt.Errorf("the OpenRouter account has no credits left: %s", msg)
	}
	if msg == "" {
		msg = res.Status
	}
	return fmt.Errorf("%s: %s (HTTP %d)", p.Name(), msg, res.StatusCode)
}

// Models returns the provider's models (cached for an hour). OpenRouter's
// catalog needs no key; a local server lists what it serves.
func (c *Client) Models(ctx context.Context, p Provider) ([]Model, error) {
	c.mu.Lock()
	if m, ok := c.models[p.base()]; ok && time.Since(m.at) < modelsTTL {
		defer c.mu.Unlock()
		return m.list, nil
	}
	c.mu.Unlock()
	if p.OpenRouter() {
		p.Key = "" // the catalog is public
	}
	res, err := c.do(ctx, http.MethodGet, "/models", p, nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var body struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			ContextLength int    `json:"context_length"`
			Description   string `json:"description"`
			Pricing       struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("unexpected model list from %s: %w", p.name(), err)
	}
	out := make([]Model, 0, len(body.Data))
	for _, m := range body.Data {
		desc := m.Description
		if r := []rune(desc); len(r) > 280 {
			desc = string(r[:280]) + "…"
		}
		out = append(out, Model{ID: m.ID, Name: m.Name, ContextLength: m.ContextLength, PromptPrice: m.Pricing.Prompt, CompletionPrice: m.Pricing.Completion, Description: desc})
	}
	c.mu.Lock()
	c.models[p.base()] = cachedModels{list: out, at: time.Now()}
	c.mu.Unlock()
	return out, nil
}

// Key checks an OpenRouter API key and returns its label and usage.
func (c *Client) Key(ctx context.Context, p Provider) (KeyInfo, error) {
	res, err := c.do(ctx, http.MethodGet, "/key", p, nil)
	if err != nil {
		return KeyInfo{}, err
	}
	defer res.Body.Close()
	var body struct {
		Data KeyInfo `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return KeyInfo{}, fmt.Errorf("unexpected OpenRouter response: %w", err)
	}
	return body.Data, nil
}

// Complete returns a whole (short) reply; used to test a model.
func (c *Client) Complete(ctx context.Context, p Provider, model string, msgs []Message, maxTokens int) (string, error) {
	res, err := c.do(ctx, http.MethodPost, "/chat/completions", p, map[string]any{"model": model, "messages": msgs, "max_tokens": maxTokens})
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var body struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("unexpected response from %s: %w", p.name(), err)
	}
	if body.Error != nil {
		return "", fmt.Errorf("%s: %s", p.Name(), body.Error.Message)
	}
	if len(body.Choices) == 0 {
		return "", errors.New(p.Name() + " returned no reply")
	}
	return strings.TrimSpace(body.Choices[0].Message.Content), nil
}

// Stream sends the conversation and calls onDelta with each piece of the reply
// as it is generated. It returns the whole reply; on error (or cancellation)
// the part received so far is returned with the error.
func (c *Client) Stream(ctx context.Context, p Provider, model string, msgs []Message, onDelta func(string) error) (string, error) {
	text, _, err := c.StreamTools(ctx, p, model, msgs, nil, onDelta)
	return text, err
}

// StreamTools is Stream with tools the model may call. The text of the reply is
// streamed through onDelta; the tool calls the model asked for are returned
// once the stream ends (their arguments arrive in pieces and are assembled
// here). A model that refuses tools returns a *ToolsUnsupportedError.
func (c *Client) StreamTools(ctx context.Context, p Provider, model string, msgs []Message, tools []Tool, onDelta func(string) error) (string, []ToolCall, error) {
	body := map[string]any{"model": model, "messages": msgs, "stream": true}
	if len(tools) > 0 {
		body["tools"] = tools
		body["tool_choice"] = "auto"
	}
	res, err := c.do(ctx, http.MethodPost, "/chat/completions", p, body)
	if err != nil {
		if len(tools) > 0 && toolsUnsupported(err) {
			return "", nil, &ToolsUnsupportedError{Err: err}
		}
		return "", nil, err
	}
	defer res.Body.Close()
	var full strings.Builder
	calls := map[int]*ToolCall{} // by index, as the deltas refer to them
	var order []int
	finish := func() []ToolCall {
		out := make([]ToolCall, 0, len(order))
		for i, idx := range order {
			tc := *calls[idx]
			if strings.TrimSpace(tc.Function.Name) == "" {
				continue
			}
			if tc.ID == "" {
				tc.ID = fmt.Sprintf("call_%d", i+1)
			}
			if tc.Type == "" {
				tc.Type = "function"
			}
			out = append(out, tc)
		}
		return out
	}
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, ":") { // keep-alive comments ("OPENROUTER PROCESSING")
			continue
		}
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			return full.String(), finish(), nil
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    *int   `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if chunk.Error != nil {
			return full.String(), finish(), fmt.Errorf("%s: %s", p.Name(), chunk.Error.Message)
		}
		for _, ch := range chunk.Choices {
			for _, tc := range ch.Delta.ToolCalls {
				var idx int
				switch {
				case tc.Index != nil:
					idx = *tc.Index
				case tc.ID != "" || len(order) == 0:
					idx = len(order) // a new call from a server that sends no index
				default:
					idx = order[len(order)-1] // more arguments of the last call
				}
				cur := calls[idx]
				if cur == nil {
					cur = &ToolCall{Type: "function"}
					calls[idx] = cur
					order = append(order, idx)
				}
				if tc.ID != "" {
					cur.ID = tc.ID
				}
				if tc.Type != "" {
					cur.Type = tc.Type
				}
				if cur.Function.Name == "" {
					cur.Function.Name = tc.Function.Name
				}
				cur.Function.Arguments += tc.Function.Arguments
			}
			if ch.Delta.Content == "" {
				continue
			}
			full.WriteString(ch.Delta.Content)
			if err := onDelta(ch.Delta.Content); err != nil {
				return full.String(), finish(), err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return full.String(), finish(), err
	}
	if ctx.Err() != nil {
		return full.String(), finish(), ctx.Err()
	}
	return full.String(), finish(), nil
}
