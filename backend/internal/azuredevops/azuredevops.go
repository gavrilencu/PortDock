// Package azuredevops talks to Azure DevOps (Services or Server) with a
// personal access token: repositories, branches and files for creating
// applications, and service hooks that notify PortDock of every push.
package azuredevops

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/github"
)

// apiVersion is supported by Azure DevOps Services and Azure DevOps Server 2020 and later.
const apiVersion = "6.0"

// BaseURL normalizes an organization name or URL: "contoso" becomes
// https://dev.azure.com/contoso; https://dev.azure.com/contoso,
// https://contoso.visualstudio.com and on-premises collection URLs are kept.
func BaseURL(org string) (string, error) {
	org = strings.TrimSpace(org)
	if org == "" {
		return "", errors.New("enter the organization, e.g. contoso or https://dev.azure.com/contoso")
	}
	if !strings.Contains(org, "://") {
		if strings.ContainsAny(org, "/ ") {
			return "", errors.New("invalid organization name")
		}
		return "https://dev.azure.com/" + url.PathEscape(org), nil
	}
	u, err := url.Parse(org)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", errors.New("invalid organization URL")
	}
	u.RawQuery, u.Fragment, u.User = "", "", nil
	return strings.TrimRight(u.String(), "/"), nil
}

// Client calls one organization (or collection) with a personal access token.
type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

func New(base, token string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) do(ctx context.Context, method, p string, q url.Values, body, out any) error {
	if q == nil {
		q = url.Values{}
	}
	q.Set("api-version", apiVersion)
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+p+"?"+q.Encode(), r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(":"+c.Token)))
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("Azure DevOps is unreachable: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	// An invalid token gets a sign-in page (203 or a redirect) instead of JSON.
	if res.StatusCode == http.StatusNonAuthoritativeInfo || res.StatusCode == http.StatusUnauthorized ||
		strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
		return errors.New("Azure DevOps rejected the personal access token (check that it is valid, not expired and has the needed scopes)")
	}
	if res.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &e) == nil && e.Message != "" {
			msg = e.Message
		}
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return fmt.Errorf("Azure DevOps: %s (HTTP %d)", msg, res.StatusCode)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("unexpected Azure DevOps response: %w", err)
	}
	return nil
}

// Project is an Azure DevOps project.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Projects lists the projects the token can see (also validates the token).
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	var res struct {
		Value []Project `json:"value"`
	}
	err := c.do(ctx, http.MethodGet, "/_apis/projects", url.Values{"$top": {"500"}}, nil, &res)
	return res.Value, err
}

// Repository is a Git repository of a project.
type Repository struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Project       Project `json:"project"`
	DefaultBranch string  `json:"default_branch"`
	RemoteURL     string  `json:"remote_url"`
	WebURL        string  `json:"web_url"`
}

// Repositories lists every Git repository of the organization, by project and name.
func (c *Client) Repositories(ctx context.Context) ([]Repository, error) {
	var res struct {
		Value []struct {
			ID            string  `json:"id"`
			Name          string  `json:"name"`
			Project       Project `json:"project"`
			DefaultBranch string  `json:"defaultBranch"`
			RemoteURL     string  `json:"remoteUrl"`
			WebURL        string  `json:"webUrl"`
			IsDisabled    bool    `json:"isDisabled"`
		} `json:"value"`
	}
	if err := c.do(ctx, http.MethodGet, "/_apis/git/repositories", nil, nil, &res); err != nil {
		return nil, err
	}
	out := make([]Repository, 0, len(res.Value))
	for _, r := range res.Value {
		if r.IsDisabled {
			continue
		}
		out = append(out, Repository{ID: r.ID, Name: r.Name, Project: r.Project, DefaultBranch: strings.TrimPrefix(r.DefaultBranch, "refs/heads/"),
			RemoteURL: CleanRemoteURL(r.RemoteURL), WebURL: r.WebURL})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project.Name != out[j].Project.Name {
			return strings.ToLower(out[i].Project.Name) < strings.ToLower(out[j].Project.Name)
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// CleanRemoteURL drops the user name Azure DevOps puts in clone URLs (https://org@dev.azure.com/…).
func CleanRemoteURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User = nil
	return u.String()
}

// CloneURL returns the clone URL with the token, for git over HTTPS.
func CloneURL(remote, token string) string {
	u, err := url.Parse(remote)
	if err != nil || token == "" {
		return remote
	}
	u.User = url.UserPassword("pat", token)
	return u.String()
}

func repoPath(project, repo string) string {
	return "/" + url.PathEscape(project) + "/_apis/git/repositories/" + url.PathEscape(repo)
}

// Branches lists the branches of a repository.
func (c *Client) Branches(ctx context.Context, project, repo string) ([]string, error) {
	var res struct {
		Value []struct {
			Name string `json:"name"`
		} `json:"value"`
	}
	if err := c.do(ctx, http.MethodGet, repoPath(project, repo)+"/refs", url.Values{"filter": {"heads/"}}, nil, &res); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(res.Value))
	for _, r := range res.Value {
		out = append(out, strings.TrimPrefix(r.Name, "refs/heads/"))
	}
	sort.Strings(out)
	return out, nil
}

func versionQuery(ref string) url.Values {
	q := url.Values{}
	if ref != "" {
		q.Set("versionDescriptor.version", ref)
		q.Set("versionDescriptor.versionType", "branch")
	}
	return q
}

// Contents lists a directory of the repository at ref.
func (c *Client) Contents(ctx context.Context, project, repo, dir, ref string) ([]github.Entry, error) {
	dir = "/" + strings.Trim(dir, "/")
	q := versionQuery(ref)
	q.Set("scopePath", dir)
	q.Set("recursionLevel", "OneLevel")
	var res struct {
		Value []struct {
			Path     string `json:"path"`
			IsFolder bool   `json:"isFolder"`
		} `json:"value"`
	}
	if err := c.do(ctx, http.MethodGet, repoPath(project, repo)+"/items", q, nil, &res); err != nil {
		return nil, err
	}
	var out []github.Entry
	for _, it := range res.Value {
		if strings.TrimRight(it.Path, "/") == strings.TrimRight(dir, "/") || it.Path == dir {
			continue // the directory itself
		}
		e := github.Entry{Name: path.Base(it.Path), Type: "file"}
		if it.IsFolder {
			e.Type = "dir"
		}
		out = append(out, e)
	}
	return out, nil
}

// File returns the content of a file at ref.
func (c *Client) File(ctx context.Context, project, repo, file, ref string) (string, error) {
	q := versionQuery(ref)
	q.Set("path", "/"+strings.TrimLeft(file, "/"))
	q.Set("includeContent", "true")
	var res struct {
		Content string `json:"content"`
	}
	err := c.do(ctx, http.MethodGet, repoPath(project, repo)+"/items", q, nil, &res)
	return res.Content, err
}

// Detect suggests a build pack for a repository directory at ref.
func (c *Client) Detect(ctx context.Context, project, repo, ref, baseDir string) (github.Suggestion, error) {
	entries, err := c.Contents(ctx, project, repo, baseDir, ref)
	if err != nil {
		return github.Suggestion{}, err
	}
	return github.DetectFrom(entries, func(name string) (string, error) {
		return c.File(ctx, project, repo, path.Join("/", baseDir, name), ref)
	}), nil
}

// HookHeader carries the application's webhook secret in service hook requests.
const HookHeader = "X-PortDock-Token"

// Service hook events PortDock subscribes to for every application.
const (
	EventPush      = "git.push"
	EventPRCreated = "git.pullrequest.created"
	EventPRUpdated = "git.pullrequest.updated"
)

// HookEvents are the events of an application's service hooks: pushes deploy,
// pull requests get preview deployments.
var HookEvents = []string{EventPush, EventPRCreated, EventPRUpdated}

// CreateHook subscribes target to an event of the repository.
func (c *Client) CreateHook(ctx context.Context, event, project, repo, target, secret string) (string, error) {
	body := map[string]any{
		"publisherId":      "tfs",
		"eventType":        event,
		"resourceVersion":  "1.0",
		"consumerId":       "webHooks",
		"consumerActionId": "httpRequest",
		"publisherInputs":  map[string]string{"projectId": project, "repository": repo},
		"consumerInputs":   map[string]string{"url": target, "httpHeaders": HookHeader + ":" + secret},
	}
	var res struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/_apis/hooks/subscriptions", nil, body, &res); err != nil {
		return "", err
	}
	return res.ID, nil
}

// CreatePushHook subscribes target to every push to the repository.
func (c *Client) CreatePushHook(ctx context.Context, project, repo, target, secret string) (string, error) {
	return c.CreateHook(ctx, EventPush, project, repo, target, secret)
}

// Status is a commit or pull request status, shown as genre/name ("portdock/deploy").
type Status struct {
	State       string // pending | succeeded | failed | error
	Description string
	TargetURL   string
	Name        string
}

func statusBody(st Status) map[string]any {
	return map[string]any{
		"state":       st.State,
		"description": st.Description,
		"targetUrl":   st.TargetURL,
		"context":     map[string]string{"genre": "portdock", "name": st.Name},
	}
}

// SetCommitStatus reports a status on a commit (needs the Code: Status scope).
func (c *Client) SetCommitStatus(ctx context.Context, project, repo, sha string, st Status) error {
	return c.do(ctx, http.MethodPost, repoPath(project, repo)+"/commits/"+url.PathEscape(sha)+"/statuses", nil, statusBody(st), nil)
}

// SetPullRequestStatus reports a status on a pull request.
func (c *Client) SetPullRequestStatus(ctx context.Context, project, repo string, pr int, st Status) error {
	return c.do(ctx, http.MethodPost, repoPath(project, repo)+fmt.Sprintf("/pullRequests/%d/statuses", pr), nil, statusBody(st), nil)
}

// DeleteHook removes a service hook subscription.
func (c *Client) DeleteHook(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/_apis/hooks/subscriptions/"+url.PathEscape(id), nil, nil, nil)
}

// Event is the part of a service hook event PortDock uses: a push
// (commits, refUpdates) or a pull request (the other fields).
type Event struct {
	EventType string `json:"eventType"`
	Resource  struct {
		Commits []struct {
			CommitID string `json:"commitId"`
			Comment  string `json:"comment"`
		} `json:"commits"`
		RefUpdates []struct {
			Name        string `json:"name"`
			OldObjectID string `json:"oldObjectId"`
			NewObjectID string `json:"newObjectId"`
		} `json:"refUpdates"`
		PullRequestID         int    `json:"pullRequestId"`
		Status                string `json:"status"` // active | completed | abandoned
		Title                 string `json:"title"`
		SourceRefName         string `json:"sourceRefName"`
		TargetRefName         string `json:"targetRefName"`
		LastMergeSourceCommit struct {
			CommitID string `json:"commitId"`
		} `json:"lastMergeSourceCommit"`
	} `json:"resource"`
}

// ChangedFiles lists the files a commit changed, relative to the repository root.
func (c *Client) ChangedFiles(ctx context.Context, project, repo, commit string) ([]string, error) {
	var res struct {
		Changes []struct {
			Item struct {
				Path     string `json:"path"`
				IsFolder bool   `json:"isFolder"`
			} `json:"item"`
		} `json:"changes"`
	}
	if err := c.do(ctx, http.MethodGet, repoPath(project, repo)+"/commits/"+url.PathEscape(commit)+"/changes", nil, nil, &res); err != nil {
		return nil, err
	}
	var out []string
	for _, ch := range res.Changes {
		if !ch.Item.IsFolder && ch.Item.Path != "" {
			out = append(out, strings.TrimPrefix(ch.Item.Path, "/"))
		}
	}
	return out, nil
}
