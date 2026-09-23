// Package assistant talks to OpenRouter, an OpenAI-compatible gateway to
// hundreds of models, for PortDock's built-in AI assistant.
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

// Message is a chat message sent to the model.
type Message struct {
	Role    string `json:"role"` // system | user | assistant
	Content string `json:"content"`
}

// KeyInfo describes an OpenRouter API key.
type KeyInfo struct {
	Label string   `json:"label"`
	Usage float64  `json:"usage"` // credits used, USD
	Limit *float64 `json:"limit"` // nil = no limit
}

// Client calls OpenRouter. Referer returns the URL of the PortDock instance
// (OpenRouter shows it as the app in usage statistics).
type Client struct {
	HTTP    *http.Client
	Referer func() string

	mu       sync.Mutex
	models   []Model
	modelsAt time.Time
}

func New() *Client { return &Client{HTTP: &http.Client{}} }

func (c *Client) do(ctx context.Context, method, path, key string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, BaseURL+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if c.Referer != nil {
		if ref := c.Referer(); ref != "" {
			req.Header.Set("HTTP-Referer", ref)
		}
	}
	req.Header.Set("X-Title", "PortDock")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OpenRouter is unreachable: %w", err)
	}
	if res.StatusCode >= 300 {
		defer res.Body.Close()
		return nil, apiError(res)
	}
	return res, nil
}

// apiError turns an OpenRouter error response into a readable error.
func apiError(res *http.Response) error {
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
		return fmt.Errorf("the OpenRouter API key was rejected: %s", msg)
	case http.StatusPaymentRequired:
		return fmt.Errorf("the OpenRouter account has no credits left: %s", msg)
	}
	if msg == "" {
		msg = res.Status
	}
	return fmt.Errorf("OpenRouter: %s (HTTP %d)", msg, res.StatusCode)
}

// Models returns the OpenRouter catalog (cached for an hour). It needs no key.
func (c *Client) Models(ctx context.Context) ([]Model, error) {
	c.mu.Lock()
	if c.models != nil && time.Since(c.modelsAt) < modelsTTL {
		defer c.mu.Unlock()
		return c.models, nil
	}
	c.mu.Unlock()
	res, err := c.do(ctx, http.MethodGet, "/models", "", nil)
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
		return nil, fmt.Errorf("unexpected OpenRouter model list: %w", err)
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
	c.models, c.modelsAt = out, time.Now()
	c.mu.Unlock()
	return out, nil
}

// Key checks an API key and returns its label and usage.
func (c *Client) Key(ctx context.Context, key string) (KeyInfo, error) {
	res, err := c.do(ctx, http.MethodGet, "/key", key, nil)
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
func (c *Client) Complete(ctx context.Context, key, model string, msgs []Message, maxTokens int) (string, error) {
	res, err := c.do(ctx, http.MethodPost, "/chat/completions", key, map[string]any{"model": model, "messages": msgs, "max_tokens": maxTokens})
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
		return "", fmt.Errorf("unexpected OpenRouter response: %w", err)
	}
	if body.Error != nil {
		return "", fmt.Errorf("OpenRouter: %s", body.Error.Message)
	}
	if len(body.Choices) == 0 {
		return "", errors.New("OpenRouter returned no reply")
	}
	return strings.TrimSpace(body.Choices[0].Message.Content), nil
}

// Stream sends the conversation and calls onDelta with each piece of the reply
// as it is generated. It returns the whole reply; on error (or cancellation)
// the part received so far is returned with the error.
func (c *Client) Stream(ctx context.Context, key, model string, msgs []Message, onDelta func(string) error) (string, error) {
	res, err := c.do(ctx, http.MethodPost, "/chat/completions", key, map[string]any{"model": model, "messages": msgs, "stream": true})
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var full strings.Builder
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
			return full.String(), nil
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
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
			return full.String(), fmt.Errorf("OpenRouter: %s", chunk.Error.Message)
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content == "" {
				continue
			}
			full.WriteString(ch.Delta.Content)
			if err := onDelta(ch.Delta.Content); err != nil {
				return full.String(), err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return full.String(), err
	}
	if ctx.Err() != nil {
		return full.String(), ctx.Err()
	}
	return full.String(), nil
}
