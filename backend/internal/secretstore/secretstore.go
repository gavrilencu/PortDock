// Package secretstore reads secrets from external secret managers
// (HashiCorp Vault and Azure Key Vault) for the environment variables that
// reference them. Values are read when a deployment starts and never stored.
package secretstore

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Types of secret stores.
const (
	Vault         = "vault"
	AzureKeyVault = "azure_keyvault"
)

// Config is the encrypted part of a store: credentials and options.
type Config struct {
	// Auth is token or approle (Vault), client_secret or managed_identity (Key Vault).
	Auth string `json:"auth,omitempty"`

	// HashiCorp Vault
	Token     string `json:"token,omitempty"`
	RoleID    string `json:"role_id,omitempty"`
	SecretID  string `json:"secret_id,omitempty"`
	AuthMount string `json:"auth_mount,omitempty"` // where AppRole is mounted (default approle)
	Namespace string `json:"namespace,omitempty"`  // Vault Enterprise / HCP namespace
	Mount     string `json:"mount,omitempty"`      // KV secrets engine (default secret)
	KVVersion int    `json:"kv_version,omitempty"` // 1 or 2 (default)

	// Azure Key Vault
	TenantID      string `json:"tenant_id,omitempty"`
	ClientID      string `json:"client_id,omitempty"` // also picks a user-assigned managed identity
	ClientSecret  string `json:"client_secret,omitempty"`
	AuthorityHost string `json:"authority_host,omitempty"` // default https://login.microsoftonline.com

	// CACert is a PEM bundle trusted besides the system roots (an internal CA).
	CACert string `json:"ca_cert,omitempty"`
}

// Client reads the secrets of one store.
type Client interface {
	// Read returns the values at path: the keys of a Vault secret, or a Key
	// Vault secret's value under "" (plus its keys when it holds a JSON object).
	Read(ctx context.Context, path string) (map[string]string, error)
	// Check signs in and confirms the store answers.
	Check(ctx context.Context) error
}

// Defaults fills the options left empty.
func (c *Config) Defaults(typ string) {
	switch typ {
	case Vault:
		if c.Auth == "" {
			c.Auth = "token"
		}
		if c.Mount == "" {
			c.Mount = "secret"
		}
		if c.KVVersion == 0 {
			c.KVVersion = 2
		}
		if c.AuthMount == "" {
			c.AuthMount = "approle"
		}
	case AzureKeyVault:
		if c.Auth == "" {
			c.Auth = "client_secret"
		}
		if c.AuthorityHost == "" {
			c.AuthorityHost = "https://login.microsoftonline.com"
		}
	}
}

var (
	mountRe     = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*$`)
	guidRe      = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)
	kvNameRe    = regexp.MustCompile(`^[0-9A-Za-z-]{1,127}$`)
	kvVersionRe = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
)

// Validate checks the address and the configuration of a store.
func Validate(typ, address string, c Config) error {
	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("the address must be an http(s) URL, e.g. https://vault.example.com:8200")
	}
	if c.CACert != "" {
		if !x509.NewCertPool().AppendCertsFromPEM([]byte(c.CACert)) {
			return errors.New("the CA certificate is not a PEM certificate")
		}
	}
	switch typ {
	case Vault:
		if !mountRe.MatchString(c.Mount) || strings.Contains(c.Mount, "..") {
			return errors.New("invalid KV mount path")
		}
		if c.KVVersion != 1 && c.KVVersion != 2 {
			return errors.New("the KV version must be 1 or 2")
		}
		switch c.Auth {
		case "token":
			if c.Token == "" {
				return errors.New("the Vault token is required")
			}
		case "approle":
			if c.RoleID == "" || c.SecretID == "" {
				return errors.New("AppRole needs a role ID and a secret ID")
			}
			if !mountRe.MatchString(c.AuthMount) || strings.Contains(c.AuthMount, "..") {
				return errors.New("invalid AppRole mount path")
			}
		default:
			return errors.New("the Vault sign-in must be token or approle")
		}
	case AzureKeyVault:
		if u.Scheme != "https" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
			return errors.New("the Key Vault URL must use https, e.g. https://my-vault.vault.azure.net")
		}
		switch c.Auth {
		case "client_secret":
			if !guidRe.MatchString(c.TenantID) || !guidRe.MatchString(c.ClientID) {
				return errors.New("the tenant ID and client ID must be GUIDs")
			}
			if c.ClientSecret == "" {
				return errors.New("the client secret is required")
			}
			if a, err := url.Parse(c.AuthorityHost); err != nil || a.Scheme != "https" && a.Hostname() != "localhost" && a.Hostname() != "127.0.0.1" || a.Host == "" {
				return errors.New("the authority host must be an https URL")
			}
		case "managed_identity":
			if c.ClientID != "" && !guidRe.MatchString(c.ClientID) {
				return errors.New("the client ID of a user-assigned identity must be a GUID")
			}
		default:
			return errors.New("the Key Vault sign-in must be client_secret or managed_identity")
		}
	default:
		return fmt.Errorf("unknown secret store type %q", typ)
	}
	return nil
}

// ValidPath checks the path of a reference: a Vault secret path, or a Key
// Vault secret name with an optional /version.
func ValidPath(typ, path string) error {
	if path == "" {
		return errors.New("the path is required")
	}
	switch typ {
	case AzureKeyVault:
		name, version, _ := strings.Cut(path, "/")
		if !kvNameRe.MatchString(name) || version != "" && !kvVersionRe.MatchString(version) {
			return errors.New("a Key Vault secret is a name of letters, digits and dashes, optionally followed by /version")
		}
	default:
		if strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") || strings.Contains(path, "//") {
			return errors.New("the path must not start or end with /")
		}
		for _, seg := range strings.Split(path, "/") {
			if seg == "." || seg == ".." {
				return errors.New("the path must not contain . or .. segments")
			}
		}
		for _, r := range path {
			if r < 0x20 || r == 0x7f || r == '?' || r == '#' || r == '%' || r == '\\' {
				return errors.New("the path contains a character that is not allowed")
			}
		}
	}
	return nil
}

// Lookup picks key from the values of a path. An empty key is the whole
// value of a Key Vault secret.
func Lookup(values map[string]string, key string) (string, error) {
	if v, ok := values[key]; ok {
		return v, nil
	}
	keys := Keys(values)
	if key == "" {
		return "", fmt.Errorf("name a key: %s", strings.Join(keys, ", "))
	}
	if len(keys) == 0 {
		return "", fmt.Errorf("no key %s: the secret has no keys", key)
	}
	return "", fmt.Errorf("no key %s (keys: %s)", key, strings.Join(keys, ", "))
}

// Keys lists the key names of values, without the whole-value entry.
func Keys(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for k := range values {
		if k != "" {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// New returns the client of a store.
func New(typ, address string, c Config) (Client, error) {
	c.Defaults(typ)
	if err := Validate(typ, address, c); err != nil {
		return nil, err
	}
	hc, err := httpClient(c.CACert, true)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(address, "/")
	switch typ {
	case Vault:
		return &vault{base: base, c: c, http: hc}, nil
	default:
		imds, _ := httpClient("", false)
		return &keyVault{base: base, c: c, http: hc, imds: imds, imdsURL: imdsURL}, nil
	}
}

// httpClient trusts the system roots plus caPEM; proxied requests go through
// the proxy of http.DefaultTransport (the corporate proxy when it is set).
func httpClient(caPEM string, proxied bool) (*http.Client, error) {
	t := http.DefaultTransport.(*http.Transport).Clone()
	if !proxied {
		t.Proxy = nil
	}
	if caPEM != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(caPEM)) {
			return nil, errors.New("the CA certificate is not a PEM certificate")
		}
		t.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: t, Timeout: 20 * time.Second}, nil
}

// readBody returns at most 1 MB of a response.
func readBody(res *http.Response) []byte {
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return b
}

// stringify renders a JSON value as an environment variable value.
func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}
