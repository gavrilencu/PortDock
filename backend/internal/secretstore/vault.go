package secretstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// vault reads a KV secrets engine of HashiCorp Vault (or OpenBao).
type vault struct {
	base string
	c    Config
	http *http.Client

	mu    sync.Mutex
	token string // signed-in token (AppRole), reused by the client's reads
}

func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

func (v *vault) do(ctx context.Context, method, path string, body any, token string) (map[string]any, int, error) {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, v.base+"/v1/"+path, rd)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("X-Vault-Request", "true")
	if token != "" {
		req.Header.Set("X-Vault-Token", token)
	}
	if v.c.Namespace != "" {
		req.Header.Set("X-Vault-Namespace", v.c.Namespace)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := v.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("Vault is unreachable: %w", err)
	}
	defer res.Body.Close()
	raw := readBody(res)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if res.StatusCode >= 300 {
		msg := ""
		if errs, ok := out["errors"].([]any); ok && len(errs) > 0 {
			parts := make([]string, 0, len(errs))
			for _, e := range errs {
				if s := strings.TrimSpace(stringify(e)); s != "" {
					parts = append(parts, s)
				}
			}
			msg = strings.Join(parts, "; ")
		}
		if msg == "" {
			msg = http.StatusText(res.StatusCode)
		}
		return out, res.StatusCode, fmt.Errorf("%s (HTTP %d)", msg, res.StatusCode)
	}
	return out, res.StatusCode, nil
}

// signIn returns the token to read with: the configured one, or one from an
// AppRole login.
func (v *vault) signIn(ctx context.Context) (string, error) {
	if v.c.Auth != "approle" {
		return v.c.Token, nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.token != "" {
		return v.token, nil
	}
	out, _, err := v.do(ctx, "POST", "auth/"+escapePath(v.c.AuthMount)+"/login", map[string]string{"role_id": v.c.RoleID, "secret_id": v.c.SecretID}, "")
	if err != nil {
		return "", fmt.Errorf("AppRole sign-in failed: %w", err)
	}
	auth, _ := out["auth"].(map[string]any)
	tok, _ := auth["client_token"].(string)
	if tok == "" {
		return "", fmt.Errorf("AppRole sign-in returned no token")
	}
	v.token = tok
	return tok, nil
}

func (v *vault) Check(ctx context.Context) error {
	tok, err := v.signIn(ctx)
	if err != nil {
		return err
	}
	if _, _, err := v.do(ctx, "GET", "auth/token/lookup-self", nil, tok); err != nil {
		return fmt.Errorf("the token is not valid: %w", err)
	}
	return nil
}

func (v *vault) Read(ctx context.Context, path string) (map[string]string, error) {
	if err := ValidPath(Vault, path); err != nil {
		return nil, err
	}
	tok, err := v.signIn(ctx)
	if err != nil {
		return nil, err
	}
	api := escapePath(v.c.Mount) + "/" + escapePath(path)
	if v.c.KVVersion == 2 {
		api = escapePath(v.c.Mount) + "/data/" + escapePath(path)
	}
	out, code, err := v.do(ctx, "GET", api, nil, tok)
	shown := v.c.Mount + "/" + path
	switch {
	case code == http.StatusNotFound:
		return nil, fmt.Errorf("no secret at %s", shown)
	case code == http.StatusForbidden:
		return nil, fmt.Errorf("permission denied on %s: the Vault policy of PortDock's token must allow reading it", shown)
	case err != nil:
		return nil, fmt.Errorf("%s: %w", shown, err)
	}
	data, _ := out["data"].(map[string]any)
	if v.c.KVVersion == 2 {
		inner, ok := data["data"].(map[string]any)
		if !ok {
			// A deleted or destroyed latest version.
			return nil, fmt.Errorf("the latest version of %s is deleted", shown)
		}
		data = inner
	}
	values := make(map[string]string, len(data))
	for k, val := range data {
		values[k] = stringify(val)
	}
	return values, nil
}
