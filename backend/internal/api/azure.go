package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/azuredevops"
	"github.com/portdock/portdock/internal/store"
)

// Azure DevOps: organizations are connected with a personal access token
// (Settings → Git sources); applications created from their repositories are
// cloned with it and deployed on every push through a service hook.

type azureConnectionOut struct {
	store.AzureConnection
	Applications int `json:"applications"`
}

func (s *Server) azureClient(ctx context.Context, id int64) (*azuredevops.Client, store.AzureConnection, error) {
	conn, err := store.ByID[store.AzureConnection](ctx, s.db, "azure_connections", id)
	if err != nil {
		return nil, conn, badRequest("Azure DevOps connection not found")
	}
	return azuredevops.New(conn.URL, s.box.MustDecrypt(conn.Token)), conn, nil
}

func (s *Server) listAzureConnections(w http.ResponseWriter, r *http.Request) error {
	cs, err := store.Select[store.AzureConnection](r.Context(), s.db, "SELECT * FROM azure_connections ORDER BY name")
	if err != nil {
		return err
	}
	out := make([]azureConnectionOut, 0, len(cs))
	for _, c := range cs {
		o := azureConnectionOut{AzureConnection: c}
		_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM applications WHERE azure_connection_id = ?", c.ID).Scan(&o.Applications)
		out = append(out, o)
	}
	return ok(w, out)
}

type azureConnectionIn struct {
	Name  *string `json:"name"`
	URL   *string `json:"url"`
	Token *string `json:"token"`
}

// checkAzure validates the organization and the token by listing its
// repositories (the Code scope is needed anyway; projects would need another).
func checkAzure(ctx context.Context, base, token string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rs, err := azuredevops.New(base, token).Repositories(ctx)
	if err != nil {
		return 0, err
	}
	return len(rs), nil
}

func (s *Server) createAzureConnection(w http.ResponseWriter, r *http.Request) error {
	var in azureConnectionIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.URL == nil || in.Token == nil || strings.TrimSpace(*in.Token) == "" {
		return badRequest("organization and personal access token are required")
	}
	base, err := azuredevops.BaseURL(*in.URL)
	if err != nil {
		return badRequest("%v", err)
	}
	token := strings.TrimSpace(*in.Token)
	n, err := checkAzure(r.Context(), base, token)
	if err != nil {
		return badRequest("%v", err)
	}
	name := base[strings.LastIndex(base, "/")+1:]
	if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
		name = strings.TrimSpace(*in.Name)
	}
	c := store.AzureConnection{Name: name, URL: base, Token: s.box.Encrypt(token), CreatedAt: store.Now()}
	if _, err := store.Insert(r.Context(), s.db, "azure_connections", &c); err != nil {
		return err
	}
	s.activity(r, "azure.connect", c.Name+" ("+c.URL+")")
	return ok(w, map[string]any{"connection": c, "repositories": n})
}

// updateAzureConnection renames a connection or replaces its token (tokens expire).
func (s *Server) updateAzureConnection(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, err := store.ByID[store.AzureConnection](r.Context(), s.db, "azure_connections", id)
	if err != nil {
		return err
	}
	var in azureConnectionIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
		c.Name = strings.TrimSpace(*in.Name)
	}
	if in.Token != nil && strings.TrimSpace(*in.Token) != "" {
		token := strings.TrimSpace(*in.Token)
		if _, err := checkAzure(r.Context(), c.URL, token); err != nil {
			return badRequest("%v", err)
		}
		c.Token = s.box.Encrypt(token)
	}
	if err := store.Update(r.Context(), s.db, "azure_connections", &c); err != nil {
		return err
	}
	s.activity(r, "azure.update", c.Name)
	return ok(w, c)
}

func (s *Server) deleteAzureConnection(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var n int
	_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM applications WHERE azure_connection_id = ?", id).Scan(&n)
	if n > 0 {
		return errStatus(409, "%d application(s) deploy from this connection; delete them or move them to another source first", n)
	}
	if err := store.Delete(r.Context(), s.db, "azure_connections", id); err != nil {
		return err
	}
	s.activity(r, "azure.disconnect", strconv.FormatInt(id, 10))
	return ok(w, map[string]bool{"ok": true})
}

func queryID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.URL.Query().Get(name), 10, 64)
	if err != nil {
		return 0, badRequest("invalid %s", name)
	}
	return id, nil
}

func (s *Server) listAzureRepositories(w http.ResponseWriter, r *http.Request) error {
	id, err := queryID(r, "connection")
	if err != nil {
		return err
	}
	c, _, err := s.azureClient(r.Context(), id)
	if err != nil {
		return err
	}
	repos, err := c.Repositories(r.Context())
	if err != nil {
		return errStatus(502, "%v", err)
	}
	return ok(w, repos)
}

func (s *Server) listAzureBranches(w http.ResponseWriter, r *http.Request) error {
	id, err := queryID(r, "connection")
	if err != nil {
		return err
	}
	c, _, err := s.azureClient(r.Context(), id)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	bs, err := c.Branches(r.Context(), q.Get("project"), q.Get("repo"))
	if err != nil {
		return errStatus(502, "%v", err)
	}
	return ok(w, bs)
}

func (s *Server) detectAzureRepo(w http.ResponseWriter, r *http.Request) error {
	id, err := queryID(r, "connection")
	if err != nil {
		return err
	}
	c, _, err := s.azureClient(r.Context(), id)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	sug, err := c.Detect(r.Context(), q.Get("project"), q.Get("repo"), q.Get("ref"), q.Get("base"))
	if err != nil {
		return errStatus(502, "%v", err)
	}
	return ok(w, sug)
}

// validAzureSource checks the Azure DevOps fields of an application.
func (s *Server) validAzureSource(ctx context.Context, a *store.Application) error {
	if a.AzureConnectionID == 0 {
		a.AzureProjectID, a.AzureRepoID = "", ""
		return nil
	}
	if _, err := store.ByID[store.AzureConnection](ctx, s.db, "azure_connections", a.AzureConnectionID); err != nil {
		return badRequest("Azure DevOps connection not found")
	}
	if a.AzureProjectID == "" || a.AzureRepoID == "" {
		return badRequest("select an Azure DevOps repository")
	}
	a.GitRepository = azuredevops.CleanRemoteURL(strings.TrimSpace(a.GitRepository))
	if !strings.HasPrefix(a.GitRepository, "https://") && !strings.HasPrefix(a.GitRepository, "http://") {
		return badRequest("invalid Azure DevOps repository URL")
	}
	a.SourceType, a.GitToken = "git", ""
	return nil
}

func (s *Server) azureHookURL(ctx context.Context, app store.Application) string {
	return s.publicURL(ctx) + "/api/webhooks/azure/" + app.UUID
}

// ensureAzureHook (re)creates the service hooks of app (pushes, pull requests)
// and records their ids, comma separated, or the error on the application.
func (s *Server) ensureAzureHook(ctx context.Context, app store.Application) error {
	if app.AzureConnectionID == 0 {
		return nil
	}
	c, _, err := s.azureClient(ctx, app.AzureConnectionID)
	var ids []string
	if err == nil {
		for _, id := range store.SplitList(app.AzureHookID) {
			_ = c.DeleteHook(ctx, id) // replaced below
		}
		for _, ev := range azuredevops.HookEvents {
			var id string
			if id, err = c.CreateHook(ctx, ev, app.AzureProjectID, app.AzureRepoID, s.azureHookURL(ctx, app), app.WebhookSecret); err != nil {
				break
			}
			ids = append(ids, id)
		}
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	_, _ = s.db.ExecContext(ctx, "UPDATE applications SET azure_hook_id = ?, azure_hook_error = ? WHERE id = ?", strings.Join(ids, ","), msg, app.ID)
	return err
}

// removeAzureHook deletes the service hooks of app (best effort).
func (s *Server) removeAzureHook(ctx context.Context, app store.Application) {
	if app.AzureConnectionID == 0 || app.AzureHookID == "" {
		return
	}
	if c, _, err := s.azureClient(ctx, app.AzureConnectionID); err == nil {
		for _, id := range store.SplitList(app.AzureHookID) {
			_ = c.DeleteHook(ctx, id)
		}
	}
}

// azureHookInfo reports the service hook of an application and how to add it by hand.
func (s *Server) azureHookInfo(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	if r.Method == http.MethodPost {
		if app.AzureConnectionID == 0 {
			return badRequest("this application doesn't deploy from Azure DevOps")
		}
		_ = s.ensureAzureHook(r.Context(), app)
		s.activity(r, "azure.hook", app.Name)
		app, _ = store.ByID[store.Application](r.Context(), s.db, "applications", app.ID)
	}
	return ok(w, map[string]any{
		"hook_id": app.AzureHookID, "hooks": len(store.SplitList(app.AzureHookID)), "events": len(azuredevops.HookEvents), "error": app.AzureHookError,
		"url": s.azureHookURL(r.Context(), app), "header": azuredevops.HookHeader,
	})
}

// azureWebhook receives the service hooks of an application: pushes deploy,
// pull requests targeting its branch get preview deployments.
func (s *Server) azureWebhook(w http.ResponseWriter, r *http.Request) error {
	app, err := s.appByUUID(r)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get(azuredevops.HookHeader)), []byte(app.WebhookSecret)) != 1 {
		return errStatus(401, "invalid token")
	}
	body, err := readBody(r)
	if err != nil {
		return badRequest("could not read body")
	}
	var p azuredevops.Event
	if err := json.Unmarshal(body, &p); err != nil {
		return badRequest("invalid payload")
	}
	switch p.EventType {
	case azuredevops.EventPRCreated, azuredevops.EventPRUpdated:
		return s.azurePullRequest(r.Context(), w, app, p)
	case azuredevops.EventPush:
	default:
		return accepted(w, webhookResult{Status: "ignored", Message: "event " + p.EventType})
	}
	for _, ref := range p.Resource.RefUpdates {
		if !strings.HasPrefix(ref.Name, "refs/heads/") {
			continue
		}
		branch := strings.TrimPrefix(ref.Name, "refs/heads/")
		if strings.Trim(ref.NewObjectID, "0") == "" {
			continue // branch deleted
		}
		if branch != app.GitBranch {
			continue
		}
		msg := ""
		for _, c := range p.Resource.Commits {
			if c.CommitID == ref.NewObjectID {
				msg = c.Comment
			}
		}
		if msg == "" && len(p.Resource.Commits) > 0 {
			msg = p.Resource.Commits[0].Comment
		}
		// The event doesn't list the changed files: ask for them only when watch paths need them.
		var files []string
		if app.AutoDeploy && app.WatchPaths != "" {
			if c, _, err := s.azureClient(r.Context(), app.AzureConnectionID); err == nil {
				for _, cm := range p.Resource.Commits {
					f, err := c.ChangedFiles(r.Context(), app.AzureProjectID, app.AzureRepoID, cm.CommitID)
					if err != nil {
						files = nil // unknown: deploy rather than miss a change
						break
					}
					files = append(files, f...)
				}
			}
		}
		return s.enqueuePush(r.Context(), w, app, branch, ref.NewObjectID, msg, files)
	}
	return accepted(w, webhookResult{Status: "ignored", Message: "no push to " + app.GitBranch})
}

// azurePullRequest maps a pull request event to a preview deployment: active
// pull requests deploy their latest commit, completed or abandoned ones are removed.
func (s *Server) azurePullRequest(ctx context.Context, w http.ResponseWriter, app store.Application, p azuredevops.Event) error {
	pr := p.Resource
	if target := strings.TrimPrefix(pr.TargetRefName, "refs/heads/"); target != app.GitBranch {
		return accepted(w, webhookResult{Application: app.Name, Status: "ignored", Message: "pull request does not target " + app.GitBranch})
	}
	branch, sha := strings.TrimPrefix(pr.SourceRefName, "refs/heads/"), pr.LastMergeSourceCommit.CommitID
	action := "update"
	switch pr.Status {
	case "completed", "abandoned":
		action = "closed"
	case "active":
		// Updates also fire for votes, reviewers and titles: deploy only new commits.
		var n int
		_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM deployments WHERE application_id = ? AND pr_number = ? AND commit_sha = ? AND status IN ('queued','running','success','pending_approval')",
			app.ID, pr.PullRequestID, sha).Scan(&n)
		if n > 0 && sha != "" {
			return accepted(w, webhookResult{Application: app.Name, Status: "ignored", Message: "commit already deployed"})
		}
	default:
		return accepted(w, webhookResult{Application: app.Name, Status: "ignored", Message: "pull request " + pr.Status})
	}
	return s.handlePullRequest(ctx, w, app, action, pr.PullRequestID, branch, sha, pr.Title)
}
