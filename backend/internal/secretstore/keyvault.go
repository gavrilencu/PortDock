package secretstore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// imdsURL is the Azure Instance Metadata Service endpoint of managed identities.
var imdsURL = "http://169.254.169.254/metadata/identity/oauth2/token"

const keyVaultAPI = "7.4"

// keyVault reads the secrets of an Azure Key Vault.
type keyVault struct {
	base    string
	c       Config
	http    *http.Client
	imds    *http.Client // the metadata service is never proxied
	imdsURL string

	mu      sync.Mutex
	token   string
	expires time.Time
}

// resource is the audience of the vault's tokens: the vault's DNS suffix,
// so sovereign clouds (vault.usgovcloudapi.net, vault.azure.cn) work too.
func (k *keyVault) resource() string {
	u, err := url.Parse(k.base)
	if err == nil {
		if _, suffix, ok := strings.Cut(u.Hostname(), "."); ok && strings.HasPrefix(suffix, "vault.") {
			return "https://" + suffix
		}
	}
	return "https://vault.azure.net"
}

func (k *keyVault) signIn(ctx context.Context) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.token != "" && time.Now().Before(k.expires) {
		return k.token, nil
	}
	var (
		req *http.Request
		err error
		hc  = k.http
	)
	if k.c.Auth == "managed_identity" {
		q := url.Values{"api-version": {"2018-02-01"}, "resource": {k.resource()}}
		if k.c.ClientID != "" {
			q.Set("client_id", k.c.ClientID)
		}
		req, err = http.NewRequestWithContext(ctx, "GET", k.imdsURL+"?"+q.Encode(), nil)
		if err == nil {
			req.Header.Set("Metadata", "true")
		}
		hc = k.imds
	} else {
		form := url.Values{"grant_type": {"client_credentials"}, "client_id": {k.c.ClientID}, "client_secret": {k.c.ClientSecret}, "scope": {k.resource() + "/.default"}}
		req, err = http.NewRequestWithContext(ctx, "POST", strings.TrimRight(k.c.AuthorityHost, "/")+"/"+url.PathEscape(k.c.TenantID)+"/oauth2/v2.0/token", strings.NewReader(form.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if err != nil {
		return "", err
	}
	res, err := hc.Do(req)
	if err != nil {
		if k.c.Auth == "managed_identity" {
			return "", fmt.Errorf("no managed identity: the metadata service of an Azure VM is unreachable (%v)", err)
		}
		return "", fmt.Errorf("Microsoft Entra ID is unreachable: %w", err)
	}
	defer res.Body.Close()
	var out struct {
		AccessToken string          `json:"access_token"`
		ExpiresIn   json.RawMessage `json:"expires_in"` // a number (Entra ID) or a string (IMDS)
		Error       string          `json:"error"`
		Description string          `json:"error_description"`
	}
	_ = json.Unmarshal(readBody(res), &out)
	if res.StatusCode >= 300 || out.AccessToken == "" {
		msg := out.Description
		if msg == "" {
			msg = out.Error
		}
		if msg == "" {
			msg = http.StatusText(res.StatusCode)
		}
		if i := strings.Index(msg, "\r\n"); i > 0 {
			msg = msg[:i] // Entra ID appends trace and correlation IDs
		}
		return "", fmt.Errorf("sign-in failed: %s (HTTP %d)", msg, res.StatusCode)
	}
	secs := 300
	_, _ = fmt.Sscan(strings.Trim(string(out.ExpiresIn), `"`), &secs)
	k.token, k.expires = out.AccessToken, time.Now().Add(time.Duration(max(secs-60, 30))*time.Second)
	return k.token, nil
}

func (k *keyVault) get(ctx context.Context, path string) (map[string]any, int, error) {
	tok, err := k.signIn(ctx)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", k.base+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := k.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("Key Vault is unreachable: %w", err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.Unmarshal(readBody(res), &out)
	if res.StatusCode >= 300 {
		msg := http.StatusText(res.StatusCode)
		if e, ok := out["error"].(map[string]any); ok {
			if m, _ := e["message"].(string); m != "" {
				msg = m
			}
		}
		return out, res.StatusCode, fmt.Errorf("%s (HTTP %d)", msg, res.StatusCode)
	}
	return out, res.StatusCode, nil
}

func (k *keyVault) Check(ctx context.Context) error {
	_, code, err := k.get(ctx, "/secrets?maxresults=1&api-version="+keyVaultAPI)
	// Reading secrets by name is enough: an identity without the List
	// permission is fine.
	if err != nil && code != http.StatusForbidden {
		return err
	}
	return nil
}

func (k *keyVault) Read(ctx context.Context, path string) (map[string]string, error) {
	if err := ValidPath(AzureKeyVault, path); err != nil {
		return nil, err
	}
	out, code, err := k.get(ctx, "/secrets/"+path+"?api-version="+keyVaultAPI)
	name, _, _ := strings.Cut(path, "/")
	switch {
	case code == http.StatusNotFound:
		return nil, fmt.Errorf("no secret named %s", name)
	case code == http.StatusForbidden:
		return nil, fmt.Errorf("access to %s denied: give PortDock's identity the Key Vault Secrets User role (or a Get secret access policy)", name)
	case err != nil:
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if attrs, ok := out["attributes"].(map[string]any); ok && attrs["enabled"] == false {
		return nil, fmt.Errorf("the secret %s is disabled", name)
	}
	value := stringify(out["value"])
	values := map[string]string{"": value}
	// A secret holding a JSON object can be read key by key.
	var obj map[string]any
	if strings.HasPrefix(strings.TrimSpace(value), "{") && json.Unmarshal([]byte(value), &obj) == nil {
		for key, v := range obj {
			if key != "" {
				values[key] = stringify(v)
			}
		}
	}
	return values, nil
}
