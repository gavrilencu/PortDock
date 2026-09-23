// Package github integrates PortDock with GitHub Apps (github.com and GitHub
// Enterprise Server): app creation via the manifest flow, installation tokens,
// repository/branch discovery, and commit statuses.
package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
)

const (
	DefaultHTMLURL = "https://github.com"
	DefaultAPIURL  = "https://api.github.com"
)

type Client struct {
	db   *store.DB
	box  *secrets.Box
	http *http.Client

	mu     sync.Mutex
	tokens map[string]cachedToken // "<appRowID>/<installationID>"
	repos  map[string]cachedRepos
}

type cachedToken struct {
	token   string
	expires time.Time
}

type cachedRepos struct {
	repos []Repo
	at    time.Time
}

func New(db *store.DB, box *secrets.Box) *Client {
	return &Client{db: db, box: box, http: &http.Client{Timeout: 30 * time.Second}, tokens: map[string]cachedToken{}, repos: map[string]cachedRepos{}}
}

// APIURLFor derives the REST API base from a GitHub web URL (GHES uses /api/v3).
func APIURLFor(htmlURL string) string {
	htmlURL = strings.TrimRight(htmlURL, "/")
	if htmlURL == "" || htmlURL == DefaultHTMLURL {
		return DefaultAPIURL
	}
	return htmlURL + "/api/v3"
}

// ---- auth ----

func parseKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("invalid private key: not PEM encoded")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key must be RSA")
	}
	return rk, nil
}

// appJWT builds the short-lived RS256 JWT that authenticates as the app itself.
func appJWT(appID int64, pemKey string) (string, error) {
	key, err := parseKey(pemKey)
	if err != nil {
		return "", err
	}
	now := time.Now()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(), // tolerate clock drift
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": strconv.FormatInt(appID, 10),
	})
	signing := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("GitHub API %d: %s", e.Status, e.Message) }

func (c *Client) do(ctx context.Context, method, rawURL, auth string, body, out any) (http.Header, error) {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "PortDock")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub request failed: %w", err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Message == "" {
			e.Message = http.StatusText(res.StatusCode)
		}
		return res.Header, &APIError{Status: res.StatusCode, Message: e.Message}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return res.Header, fmt.Errorf("decode GitHub response: %w", err)
		}
	}
	return res.Header, nil
}

func (c *Client) asApp(ctx context.Context, app store.GitHubApp, method, path string, body, out any) error {
	key, err := c.box.Decrypt(app.PrivateKey)
	if err != nil {
		return err
	}
	jwt, err := appJWT(app.AppID, key)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, method, strings.TrimRight(app.APIURL, "/")+path, "Bearer "+jwt, body, out)
	return err
}

// InstallationToken returns a cached (or fresh) installation access token.
func (c *Client) InstallationToken(ctx context.Context, app store.GitHubApp, installationID int64) (string, error) {
	key := fmt.Sprintf("%d/%d", app.ID, installationID)
	c.mu.Lock()
	if t, ok := c.tokens[key]; ok && time.Until(t.expires) > 5*time.Minute {
		c.mu.Unlock()
		return t.token, nil
	}
	c.mu.Unlock()
	var res struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := c.asApp(ctx, app, "POST", fmt.Sprintf("/app/installations/%d/access_tokens", installationID), nil, &res); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.tokens[key] = cachedToken{token: res.Token, expires: res.ExpiresAt}
	c.mu.Unlock()
	return res.Token, nil
}

func (c *Client) asInstallation(ctx context.Context, app store.GitHubApp, installationID int64, method, path string, body, out any) (http.Header, error) {
	tok, err := c.InstallationToken(ctx, app, installationID)
	if err != nil {
		return nil, err
	}
	u := path
	if !strings.HasPrefix(path, "http") {
		u = strings.TrimRight(app.APIURL, "/") + path
	}
	return c.do(ctx, method, u, "Bearer "+tok, body, out)
}

// App loads a GitHub app row.
func (c *Client) App(ctx context.Context, id int64) (store.GitHubApp, error) {
	return store.ByID[store.GitHubApp](ctx, c.db, "github_apps", id)
}

// ---- manifest flow ----

// Manifest describes the app GitHub creates on our behalf.
func Manifest(name, publicURL string) map[string]any {
	base := strings.TrimRight(publicURL, "/")
	m := map[string]any{
		"name":            name,
		"url":             base,
		"redirect_url":    base + "/api/github/callback",
		"setup_url":       base + "/api/github/setup",
		"setup_on_update": true,
		"public":          false,
		"default_permissions": map[string]string{
			"contents":      "read",
			"metadata":      "read",
			"pull_requests": "read",
			"statuses":      "write",
		},
	}
	// GitHub rejects manifests whose webhook URL isn't publicly reachable, even
	// when inactive. Private instances get an app without a webhook; it can be
	// enabled later from Settings once a public URL exists.
	if IsPublicURL(base) {
		m["hook_attributes"] = map[string]any{"url": base + "/api/webhooks/github-app", "active": true}
		m["default_events"] = []string{"push", "pull_request"}
	}
	return m
}

// IsPublicURL reports whether GitHub could plausibly reach rawURL.
func IsPublicURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") || !strings.Contains(h, ".") {
		return false
	}
	if ip := net.ParseIP(h); ip != nil {
		return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified())
	}
	return true
}

type Conversion struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	WebhookSecret string `json:"webhook_secret"`
	PEM           string `json:"pem"`
	HTMLURL       string `json:"html_url"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// ConvertManifest exchanges the temporary code for the app's credentials.
func (c *Client) ConvertManifest(ctx context.Context, apiURL, code string) (Conversion, error) {
	var conv Conversion
	_, err := c.do(ctx, "POST", strings.TrimRight(apiURL, "/")+"/app-manifests/"+url.PathEscape(code)+"/conversions", "", nil, &conv)
	return conv, err
}

// Describe fetches the app's own metadata (validates manually registered credentials).
func (c *Client) Describe(ctx context.Context, app store.GitHubApp) (slug, owner, name string, err error) {
	var res struct {
		Slug  string `json:"slug"`
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	if err = c.asApp(ctx, app, "GET", "/app", nil, &res); err != nil {
		return
	}
	return res.Slug, res.Owner.Login, res.Name, nil
}

// ---- installations ----

type installation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login     string `json:"login"`
		Type      string `json:"type"`
		AvatarURL string `json:"avatar_url"`
	} `json:"account"`
	RepositorySelection string `json:"repository_selection"`
}

// SyncInstallations mirrors the app's installations into the database.
func (c *Client) SyncInstallations(ctx context.Context, app store.GitHubApp) ([]store.GitHubInstallation, error) {
	var all []installation
	for page := 1; page <= 10; page++ {
		var batch []installation
		if err := c.asApp(ctx, app, "GET", fmt.Sprintf("/app/installations?per_page=100&page=%d", page), nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	keep := map[int64]bool{}
	for _, in := range all {
		keep[in.ID] = true
		_, err := c.db.ExecContext(ctx, `INSERT INTO github_installations(github_app_id, installation_id, account_login, account_type, account_avatar, repository_selection, created_at)
			VALUES(?,?,?,?,?,?,?) ON CONFLICT(github_app_id, installation_id) DO UPDATE SET account_login = excluded.account_login,
			account_type = excluded.account_type, account_avatar = excluded.account_avatar, repository_selection = excluded.repository_selection`,
			app.ID, in.ID, in.Account.Login, in.Account.Type, in.Account.AvatarURL, in.RepositorySelection, store.Now())
		if err != nil {
			return nil, err
		}
	}
	existing, err := store.Select[store.GitHubInstallation](ctx, c.db, "SELECT * FROM github_installations WHERE github_app_id = ?", app.ID)
	if err != nil {
		return nil, err
	}
	out := existing[:0]
	for _, e := range existing {
		if !keep[e.InstallationID] {
			_ = store.Delete(ctx, c.db, "github_installations", e.ID)
			continue
		}
		out = append(out, e)
	}
	c.mu.Lock()
	for k := range c.repos {
		if strings.HasPrefix(k, fmt.Sprintf("%d/", app.ID)) {
			delete(c.repos, k)
		}
	}
	c.mu.Unlock()
	return out, nil
}

// ---- repositories ----

type Repo struct {
	GitHubAppID    int64  `json:"github_app_id"`
	InstallationID int64  `json:"installation_id"`
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	FullName       string `json:"full_name"`
	Owner          string `json:"owner"`
	OwnerAvatar    string `json:"owner_avatar"`
	Private        bool   `json:"private"`
	DefaultBranch  string `json:"default_branch"`
	Description    string `json:"description"`
	Language       string `json:"language"`
	HTMLURL        string `json:"html_url"`
	CloneURL       string `json:"clone_url"`
	PushedAt       string `json:"pushed_at"`
	Archived       bool   `json:"archived"`
}

type ghRepo struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	Description   string `json:"description"`
	Language      string `json:"language"`
	HTMLURL       string `json:"html_url"`
	CloneURL      string `json:"clone_url"`
	PushedAt      string `json:"pushed_at"`
	Archived      bool   `json:"archived"`
	Owner         struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	} `json:"owner"`
}

// Repositories lists repos accessible to an installation (cached for a minute).
func (c *Client) Repositories(ctx context.Context, app store.GitHubApp, installationID int64, refresh bool) ([]Repo, error) {
	key := fmt.Sprintf("%d/%d", app.ID, installationID)
	c.mu.Lock()
	if cr, ok := c.repos[key]; ok && !refresh && time.Since(cr.at) < time.Minute {
		c.mu.Unlock()
		return cr.repos, nil
	}
	c.mu.Unlock()
	var out []Repo
	for page := 1; page <= 50; page++ {
		var res struct {
			TotalCount   int      `json:"total_count"`
			Repositories []ghRepo `json:"repositories"`
		}
		if _, err := c.asInstallation(ctx, app, installationID, "GET", fmt.Sprintf("/installation/repositories?per_page=100&page=%d", page), nil, &res); err != nil {
			return nil, err
		}
		for _, r := range res.Repositories {
			out = append(out, Repo{
				GitHubAppID: app.ID, InstallationID: installationID, ID: r.ID, Name: r.Name, FullName: r.FullName,
				Owner: r.Owner.Login, OwnerAvatar: r.Owner.AvatarURL, Private: r.Private, DefaultBranch: r.DefaultBranch,
				Description: r.Description, Language: r.Language, HTMLURL: r.HTMLURL, CloneURL: r.CloneURL,
				PushedAt: r.PushedAt, Archived: r.Archived,
			})
		}
		if len(res.Repositories) < 100 || len(out) >= res.TotalCount {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PushedAt > out[j].PushedAt })
	c.mu.Lock()
	c.repos[key] = cachedRepos{repos: out, at: time.Now()}
	c.mu.Unlock()
	return out, nil
}

type Branch struct {
	Name      string `json:"name"`
	Protected bool   `json:"protected"`
	SHA       string `json:"sha"`
}

func (c *Client) Branches(ctx context.Context, app store.GitHubApp, installationID int64, fullName string) ([]Branch, error) {
	var out []Branch
	for page := 1; page <= 20; page++ {
		var batch []struct {
			Name      string `json:"name"`
			Protected bool   `json:"protected"`
			Commit    struct {
				SHA string `json:"sha"`
			} `json:"commit"`
		}
		if _, err := c.asInstallation(ctx, app, installationID, "GET", fmt.Sprintf("/repos/%s/branches?per_page=100&page=%d", fullName, page), nil, &batch); err != nil {
			return nil, err
		}
		for _, b := range batch {
			out = append(out, Branch{Name: b.Name, Protected: b.Protected, SHA: b.Commit.SHA})
		}
		if len(batch) < 100 {
			break
		}
	}
	return out, nil
}

type Entry struct {
	Name string `json:"name"`
	Type string `json:"type"` // file | dir
}

// Contents lists a directory of the repository at ref.
func (c *Client) Contents(ctx context.Context, app store.GitHubApp, installationID int64, fullName, dir, ref string) ([]Entry, error) {
	p := fmt.Sprintf("/repos/%s/contents/%s", fullName, strings.Trim(dir, "/"))
	if ref != "" {
		p += "?ref=" + url.QueryEscape(ref)
	}
	var entries []Entry
	_, err := c.asInstallation(ctx, app, installationID, "GET", p, nil, &entries)
	return entries, err
}

// File returns the decoded content of a file (up to 1 MB).
func (c *Client) File(ctx context.Context, app store.GitHubApp, installationID int64, fullName, path, ref string) (string, error) {
	p := fmt.Sprintf("/repos/%s/contents/%s", fullName, strings.Trim(path, "/"))
	if ref != "" {
		p += "?ref=" + url.QueryEscape(ref)
	}
	var f struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if _, err := c.asInstallation(ctx, app, installationID, "GET", p, nil, &f); err != nil {
		return "", err
	}
	if f.Encoding != "base64" {
		return f.Content, nil
	}
	b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
	return string(b), err
}

// SetStatus reports a commit status (pending | success | failure | error).
func (c *Client) SetStatus(ctx context.Context, app store.GitHubApp, installationID int64, fullName, sha, state, description, targetURL string) error {
	if len(description) > 140 {
		description = description[:137] + "..."
	}
	_, err := c.asInstallation(ctx, app, installationID, "POST", fmt.Sprintf("/repos/%s/statuses/%s", fullName, sha), map[string]string{
		"state": state, "description": description, "target_url": targetURL, "context": "portdock/deploy",
	}, nil)
	return err
}

// CloneURL returns an authenticated HTTPS clone URL for the repository.
func (c *Client) CloneURL(ctx context.Context, appRowID, installationID int64, fullName string) (string, error) {
	app, err := c.App(ctx, appRowID)
	if err != nil {
		return "", fmt.Errorf("GitHub app not found: %w", err)
	}
	tok, err := c.InstallationToken(ctx, app, installationID)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(strings.TrimRight(app.HTMLURL, "/") + "/" + fullName + ".git")
	if err != nil {
		return "", err
	}
	u.User = url.UserPassword("x-access-token", tok)
	return u.String(), nil
}

// Forget drops cached tokens/repos for an app (after deletion or key rotation).
func (c *Client) Forget(appRowID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := fmt.Sprintf("%d/", appRowID)
	for k := range c.tokens {
		if strings.HasPrefix(k, prefix) {
			delete(c.tokens, k)
		}
	}
	for k := range c.repos {
		if strings.HasPrefix(k, prefix) {
			delete(c.repos, k)
		}
	}
}
