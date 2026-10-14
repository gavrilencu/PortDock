package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/github"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
)

type githubAppDTO struct {
	store.GitHubApp
	Installations []store.GitHubInstallation `json:"installations"`
	InstallURL    string                     `json:"install_url"`
	SettingsURL   string                     `json:"settings_url"`
	Applications  int                        `json:"applications"`
	WebhookURL    string                     `json:"webhook_url"`
	// WebhookConfigured is false for apps created on a private instance
	// (GitHub refuses unreachable webhook URLs at creation time).
	WebhookConfigured bool `json:"webhook_configured"`
	PublicURL         bool `json:"public_url"`
}

func appPath(a store.GitHubApp) string {
	if strings.TrimRight(a.HTMLURL, "/") == github.DefaultHTMLURL {
		return "/apps/"
	}
	return "/github-apps/"
}

func (s *Server) githubAppOut(ctx context.Context, a store.GitHubApp) githubAppDTO {
	ins, _ := store.Select[store.GitHubInstallation](ctx, s.db, "SELECT * FROM github_installations WHERE github_app_id = ? ORDER BY account_login", a.ID)
	var n int
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM applications WHERE github_app_id = ?", a.ID).Scan(&n)
	base := strings.TrimRight(a.HTMLURL, "/")
	settings := base + appPath(a) + a.Slug // public page; owners see a "Configure/Edit" entry point there
	return githubAppDTO{
		GitHubApp: a, Installations: ins, Applications: n,
		InstallURL:  base + appPath(a) + a.Slug + "/installations/new",
		SettingsURL: settings,
		WebhookURL:  s.publicURL(ctx) + "/api/webhooks/github-app",

		WebhookConfigured: s.box.MustDecrypt(a.WebhookSecret) != "",
		PublicURL:         github.IsPublicURL(s.publicURL(ctx)),
	}
}

// rotateGitHubWebhookSecret issues a new webhook secret to paste into the app's
// settings on GitHub (used to enable webhooks after moving to a public URL).
func (s *Server) rotateGitHubWebhookSecret(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadGitHubApp(r)
	if err != nil {
		return err
	}
	secret := secrets.RandomToken(24)
	if _, err := s.db.ExecContext(r.Context(), "UPDATE github_apps SET webhook_secret = ? WHERE id = ?", s.box.Encrypt(secret), app.ID); err != nil {
		return err
	}
	s.activity(r, "github.app.webhook_secret", app.Name)
	app.WebhookSecret = s.box.Encrypt(secret)
	return ok(w, map[string]any{"secret": secret, "app": s.githubAppOut(r.Context(), app)})
}

func (s *Server) listGitHubApps(w http.ResponseWriter, r *http.Request) error {
	apps, err := store.Select[store.GitHubApp](r.Context(), s.db, "SELECT * FROM github_apps ORDER BY id")
	if err != nil {
		return err
	}
	out := make([]githubAppDTO, 0, len(apps))
	for _, a := range apps {
		out = append(out, s.githubAppOut(r.Context(), a))
	}
	return ok(w, out)
}

type manifestState struct {
	HTMLURL string `json:"html_url"`
	APIURL  string `json:"api_url"`
	Created int64  `json:"created"`
}

func normalizeHTMLURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return github.DefaultHTMLURL, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", badRequest("GitHub Enterprise URL must look like https://github.example.com")
	}
	return u.Scheme + "://" + u.Host, nil
}

// createGitHubManifest prepares the GitHub App manifest flow. The browser POSTs
// the manifest to GitHub, which redirects back to /api/github/callback.
func (s *Server) createGitHubManifest(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name          string `json:"name"`
		Organization  string `json:"organization"`
		EnterpriseURL string `json:"enterprise_url"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	htmlURL, err := normalizeHTMLURL(in.EnterpriseURL)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "PortDock " + secrets.RandomPassword(5)
	}
	state := secrets.RandomToken(16)
	st, _ := json.Marshal(manifestState{HTMLURL: htmlURL, APIURL: github.APIURLFor(htmlURL), Created: time.Now().Unix()})
	if err := s.db.SetSetting(r.Context(), "gh_state:"+state, string(st)); err != nil {
		return err
	}
	action := htmlURL + "/settings/apps/new"
	if org := strings.TrimSpace(in.Organization); org != "" {
		action = htmlURL + "/organizations/" + url.PathEscape(org) + "/settings/apps/new"
	}
	manifest, _ := json.Marshal(github.Manifest(name, s.publicURL(r.Context())))
	s.activity(r, "github.manifest", name)
	return ok(w, map[string]string{"action_url": action + "?state=" + state, "manifest": string(manifest), "state": state})
}

func (s *Server) redirectUI(w http.ResponseWriter, r *http.Request, query string) error {
	http.Redirect(w, r, "/settings?tab=git&"+query, http.StatusFound)
	return nil
}

// githubCallback finishes the manifest flow: exchange the code for credentials,
// store the app, then send the user on to install it.
func (s *Server) githubCallback(w http.ResponseWriter, r *http.Request) error {
	u, err := s.auth.Authenticate(r)
	if err != nil || !u.IsAdmin() {
		http.Redirect(w, r, "/login?next=/settings%3Ftab%3Dgit", http.StatusFound)
		return nil
	}
	ctx := r.Context()
	code, state := r.URL.Query().Get("code"), r.URL.Query().Get("state")
	raw := s.db.Setting(ctx, "gh_state:"+state)
	if code == "" || state == "" || raw == "" {
		return s.redirectUI(w, r, "error="+url.QueryEscape("The GitHub App setup session expired. Please start again."))
	}
	_, _ = s.db.ExecContext(ctx, "DELETE FROM settings WHERE key = ?", "gh_state:"+state)
	var st manifestState
	_ = json.Unmarshal([]byte(raw), &st)
	if time.Since(time.Unix(st.Created, 0)) > time.Hour {
		return s.redirectUI(w, r, "error="+url.QueryEscape("The GitHub App setup session expired. Please start again."))
	}
	conv, err := s.github.ConvertManifest(ctx, st.APIURL, code)
	if err != nil {
		return s.redirectUI(w, r, "error="+url.QueryEscape("GitHub rejected the app registration: "+err.Error()))
	}
	app := store.GitHubApp{
		Name: conv.Name, AppID: conv.ID, Slug: conv.Slug, Owner: conv.Owner.Login, ClientID: conv.ClientID,
		ClientSecret: s.box.Encrypt(conv.ClientSecret), WebhookSecret: s.box.Encrypt(conv.WebhookSecret),
		PrivateKey: s.box.Encrypt(conv.PEM), HTMLURL: st.HTMLURL, APIURL: st.APIURL, CreatedAt: store.Now(),
	}
	if _, err := store.Insert(ctx, s.db, "github_apps", &app); err != nil {
		return s.redirectUI(w, r, "error="+url.QueryEscape(err.Error()))
	}
	s.db.LogActivity(ctx, u.ID, "github.app.create", app.Name)
	http.Redirect(w, r, strings.TrimRight(app.HTMLURL, "/")+appPath(app)+app.Slug+"/installations/new", http.StatusFound)
	return nil
}

// githubSetup is GitHub's post-installation redirect.
func (s *Server) githubSetup(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.auth.Authenticate(r); err != nil {
		http.Redirect(w, r, "/login?next=/settings%3Ftab%3Dgit", http.StatusFound)
		return nil
	}
	apps, _ := store.Select[store.GitHubApp](r.Context(), s.db, "SELECT * FROM github_apps")
	for _, a := range apps {
		if _, err := s.github.SyncInstallations(r.Context(), a); err != nil {
			slog.Warn("github sync", "app", a.Name, "err", err)
		}
	}
	return s.redirectUI(w, r, "installed=1")
}

// registerGitHubApp stores credentials of an app created manually on GitHub.
func (s *Server) registerGitHubApp(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		AppID         int64  `json:"app_id"`
		PrivateKey    string `json:"private_key"`
		WebhookSecret string `json:"webhook_secret"`
		ClientID      string `json:"client_id"`
		ClientSecret  string `json:"client_secret"`
		EnterpriseURL string `json:"enterprise_url"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.AppID <= 0 || strings.TrimSpace(in.PrivateKey) == "" {
		return badRequest("App ID and private key are required")
	}
	htmlURL, err := normalizeHTMLURL(in.EnterpriseURL)
	if err != nil {
		return err
	}
	app := store.GitHubApp{
		AppID: in.AppID, ClientID: in.ClientID, ClientSecret: s.box.Encrypt(in.ClientSecret),
		WebhookSecret: s.box.Encrypt(in.WebhookSecret), PrivateKey: s.box.Encrypt(strings.TrimSpace(in.PrivateKey) + "\n"),
		HTMLURL: htmlURL, APIURL: github.APIURLFor(htmlURL), CreatedAt: store.Now(),
	}
	slug, owner, name, err := s.github.Describe(r.Context(), app)
	if err != nil {
		return badRequest("could not authenticate as the GitHub App: %v", err)
	}
	app.Slug, app.Owner, app.Name = slug, owner, name
	if _, err := store.Insert(r.Context(), s.db, "github_apps", &app); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return badRequest("this GitHub App is already registered")
		}
		return err
	}
	if _, err := s.github.SyncInstallations(r.Context(), app); err != nil {
		slog.Warn("github sync", "err", err)
	}
	s.activity(r, "github.app.register", app.Name)
	return ok(w, s.githubAppOut(r.Context(), app))
}

func (s *Server) loadGitHubApp(r *http.Request) (store.GitHubApp, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.GitHubApp{}, err
	}
	return store.ByID[store.GitHubApp](r.Context(), s.db, "github_apps", id)
}

func (s *Server) syncGitHubApp(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadGitHubApp(r)
	if err != nil {
		return err
	}
	if _, err := s.github.SyncInstallations(r.Context(), app); err != nil {
		return badRequest("%v", err)
	}
	return ok(w, s.githubAppOut(r.Context(), app))
}

func (s *Server) deleteGitHubApp(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadGitHubApp(r)
	if err != nil {
		return err
	}
	var n int
	_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM applications WHERE github_app_id = ?", app.ID).Scan(&n)
	if n > 0 {
		return badRequest("%d application(s) still deploy through this GitHub App", n)
	}
	if err := store.Delete(r.Context(), s.db, "github_apps", app.ID); err != nil {
		return err
	}
	s.github.Forget(app.ID)
	s.activity(r, "github.app.delete", app.Name)
	return ok(w, map[string]bool{"ok": true})
}

// listGitHubRepositories aggregates repositories across every installation.
func (s *Server) listGitHubRepositories(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	apps, err := store.Select[store.GitHubApp](ctx, s.db, "SELECT * FROM github_apps ORDER BY id")
	if err != nil {
		return err
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	refresh := r.URL.Query().Get("refresh") == "1"
	type failure struct {
		Account string `json:"account"`
		Error   string `json:"error"`
	}
	repos := []github.Repo{}
	failures := []failure{}
	for _, a := range apps {
		ins, _ := store.Select[store.GitHubInstallation](ctx, s.db, "SELECT * FROM github_installations WHERE github_app_id = ?", a.ID)
		for _, in := range ins {
			list, err := s.github.Repositories(ctx, a, in.InstallationID, refresh)
			if err != nil {
				failures = append(failures, failure{Account: in.AccountLogin, Error: err.Error()})
				continue
			}
			for _, rp := range list {
				if q == "" || strings.Contains(strings.ToLower(rp.FullName+" "+rp.Description), q) {
					repos = append(repos, rp)
				}
			}
		}
	}
	sort.SliceStable(repos, func(i, j int) bool { return repos[i].PushedAt > repos[j].PushedAt })
	return ok(w, map[string]any{"repositories": repos, "errors": failures})
}

// repoParams resolves ?app=&installation=&repo= into an app row.
func (s *Server) repoParams(r *http.Request) (store.GitHubApp, int64, string, error) {
	q := r.URL.Query()
	appID, _ := strconv.ParseInt(q.Get("app"), 10, 64)
	instID, _ := strconv.ParseInt(q.Get("installation"), 10, 64)
	repo := strings.Trim(q.Get("repo"), "/")
	if appID == 0 || instID == 0 || strings.Count(repo, "/") != 1 {
		return store.GitHubApp{}, 0, "", badRequest("app, installation and repo (owner/name) are required")
	}
	app, err := store.ByID[store.GitHubApp](r.Context(), s.db, "github_apps", appID)
	if err != nil {
		return app, 0, "", badRequest("GitHub App not found")
	}
	return app, instID, repo, nil
}

func (s *Server) listGitHubBranches(w http.ResponseWriter, r *http.Request) error {
	app, inst, repo, err := s.repoParams(r)
	if err != nil {
		return err
	}
	branches, err := s.github.Branches(r.Context(), app, inst, repo)
	if err != nil {
		return badRequest("%v", err)
	}
	return ok(w, branches)
}

func (s *Server) detectGitHubRepo(w http.ResponseWriter, r *http.Request) error {
	app, inst, repo, err := s.repoParams(r)
	if err != nil {
		return err
	}
	sug, err := s.github.Detect(r.Context(), app, inst, repo, r.URL.Query().Get("ref"), r.URL.Query().Get("base"))
	if err != nil {
		var ae *github.APIError
		// GitHub answers "Not Found" for a missing path; other 404s (e.g. an
		// uninstalled app) must surface as-is.
		if errors.As(err, &ae) && ae.Status == 404 && ae.Message == "Not Found" {
			return badRequest("directory not found in this branch")
		}
		return badRequest("%v", err)
	}
	return ok(w, sug)
}

// githubAppWebhook receives events for every connected repository of a GitHub App.
func (s *Server) githubAppWebhook(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	body, err := readBody(r)
	if err != nil {
		return badRequest("could not read body")
	}
	targetID, _ := strconv.ParseInt(r.Header.Get("X-GitHub-Hook-Installation-Target-ID"), 10, 64)
	apps, err := store.Select[store.GitHubApp](ctx, s.db, "SELECT * FROM github_apps WHERE app_id = ?", targetID)
	if err != nil {
		return err
	}
	var app *store.GitHubApp
	for i := range apps {
		if validHMAC(s.box.MustDecrypt(apps[i].WebhookSecret), body, r.Header.Get("X-Hub-Signature-256")) {
			app = &apps[i]
			break
		}
	}
	if app == nil {
		return errStatus(401, "invalid signature or unknown GitHub App")
	}

	event := r.Header.Get("X-GitHub-Event")
	var base struct {
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
	}
	_ = json.Unmarshal(body, &base)

	switch event {
	case "ping":
		return accepted(w, webhookResult{Status: "pong"})
	case "installation", "installation_repositories":
		a := *app
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if _, err := s.github.SyncInstallations(ctx, a); err != nil {
				slog.Warn("github sync", "err", err)
			}
		}()
		return accepted(w, webhookResult{Status: "synced"})
	case "push", "pull_request":
	default:
		return accepted(w, webhookResult{Status: "ignored", Message: "event " + event})
	}

	targets, err := store.Select[store.Application](ctx, s.db,
		"SELECT * FROM applications WHERE github_app_id = ? AND lower(git_full_name) = lower(?)", app.ID, base.Repository.FullName)
	if err != nil {
		return err
	}
	results := []webhookResult{}
	for _, a := range targets {
		if a.GitHubInstallation != base.Installation.ID && base.Installation.ID != 0 {
			// Keep the installation current (e.g. the app was reinstalled).
			a.GitHubInstallation = base.Installation.ID
			_, _ = s.db.ExecContext(ctx, "UPDATE applications SET github_installation_id = ? WHERE id = ?", a.GitHubInstallation, a.ID)
		}
		var res webhookResult
		var err error
		if event == "push" {
			var p struct {
				Ref        string      `json:"ref"`
				After      string      `json:"after"`
				Deleted    bool        `json:"deleted"`
				Commits    []gitCommit `json:"commits"`
				HeadCommit *gitCommit  `json:"head_commit"`
			}
			if json.Unmarshal(body, &p) != nil || p.Deleted || !strings.HasPrefix(p.Ref, "refs/heads/") {
				results = append(results, webhookResult{Application: a.Name, Status: "ignored", Message: "not a branch push"})
				continue
			}
			msg := ""
			if p.HeadCommit != nil {
				msg = p.HeadCommit.Message
			}
			res, err = s.pushOutcome(ctx, a, strings.TrimPrefix(p.Ref, "refs/heads/"), p.After, msg, changedFiles(p.Commits))
		} else {
			var p struct {
				Action      string `json:"action"`
				Number      int    `json:"number"`
				PullRequest struct {
					Title string `json:"title"`
					Head  struct {
						Ref  string    `json:"ref"`
						SHA  string    `json:"sha"`
						Repo *repoName `json:"repo"`
					} `json:"head"`
					Base struct {
						Ref  string    `json:"ref"`
						Repo *repoName `json:"repo"`
					} `json:"base"`
				} `json:"pull_request"`
			}
			if json.Unmarshal(body, &p) != nil || p.PullRequest.Base.Ref != a.GitBranch {
				results = append(results, webhookResult{Application: a.Name, Status: "ignored", Message: "PR does not target " + a.GitBranch})
				continue
			}
			head, base := p.PullRequest.Head.Repo, p.PullRequest.Base.Repo
			fork := head == nil || base == nil || !strings.EqualFold(head.FullName, base.FullName)
			res, err = s.pullRequestOutcome(ctx, a, p.Action, p.Number, p.PullRequest.Head.Ref, p.PullRequest.Head.SHA, p.PullRequest.Title, fork)
		}
		if err != nil {
			res = webhookResult{Application: a.Name, Status: "error", Message: err.Error()}
		}
		results = append(results, res)
	}
	writeJSON(w, 202, map[string]any{"event": event, "repository": base.Repository.FullName, "results": results})
	return nil
}

// ensureGitHubSource validates and normalizes GitHub App fields on an application.
func (s *Server) ensureGitHubSource(ctx context.Context, a *store.Application) error {
	if a.GitHubAppID == 0 {
		a.GitHubInstallation, a.GitFullName = 0, ""
		return nil
	}
	app, err := store.ByID[store.GitHubApp](ctx, s.db, "github_apps", a.GitHubAppID)
	if err != nil {
		return badRequest("GitHub App not found")
	}
	if a.GitHubInstallation == 0 || strings.Count(a.GitFullName, "/") != 1 {
		return badRequest("select a repository from the GitHub App")
	}
	a.SourceType = "git"
	a.GitRepository = fmt.Sprintf("%s/%s", strings.TrimRight(app.HTMLURL, "/"), a.GitFullName)
	a.GitToken = "" // tokens are minted per deployment from the app
	return nil
}
