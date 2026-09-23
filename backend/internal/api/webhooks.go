package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/portdock/portdock/internal/deploy"
	"github.com/portdock/portdock/internal/store"
)

func (s *Server) appByUUID(r *http.Request) (store.Application, error) {
	return store.Get[store.Application](r.Context(), s.db, "SELECT * FROM applications WHERE uuid = ?", r.PathValue("uuid"))
}

func readBody(r *http.Request) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(nil, r.Body, 25<<20))
}

func validHMAC(secret string, body []byte, signature string) bool {
	signature = strings.TrimPrefix(signature, "sha256=")
	got, err := hex.DecodeString(signature)
	if err != nil || secret == "" {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hmac.Equal(m.Sum(nil), got)
}

type webhookResult struct {
	Application  string `json:"application,omitempty"`
	Status       string `json:"status"`
	Message      string `json:"message,omitempty"`
	DeploymentID int64  `json:"deployment_id,omitempty"`
}

func accepted(w http.ResponseWriter, res webhookResult) error {
	writeJSON(w, 202, res)
	return nil
}

type gitCommit struct {
	ID       string   `json:"id"`
	Message  string   `json:"message"`
	Added    []string `json:"added"`
	Modified []string `json:"modified"`
	Removed  []string `json:"removed"`
}

func changedFiles(commits []gitCommit) []string {
	var out []string
	for _, c := range commits {
		out = append(out, c.Added...)
		out = append(out, c.Modified...)
		out = append(out, c.Removed...)
	}
	return out
}

// matchesWatchPaths reports whether any changed file matches the configured globs.
// Patterns support `*`, `?` and `**` (any number of directories); a leading `!` excludes.
func matchesWatchPaths(patterns string, files []string) bool {
	globs := store.SplitList(patterns)
	if len(globs) == 0 || len(files) == 0 {
		return true
	}
	for _, f := range files {
		included := false
		for _, g := range globs {
			if neg := strings.HasPrefix(g, "!"); neg {
				if globMatch(strings.TrimPrefix(g, "!"), f) {
					included = false
				}
			} else if globMatch(g, f) {
				included = true
			}
		}
		if included {
			return true
		}
	}
	return false
}

func globMatch(pattern, name string) bool {
	pattern = strings.TrimPrefix(pattern, "/")
	if !strings.Contains(pattern, "**") {
		ok, _ := path.Match(pattern, name)
		if !ok && !strings.Contains(pattern, "/") {
			ok, _ = path.Match(pattern, path.Base(name))
		}
		return ok
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(p, n []string) bool {
	for len(p) > 0 {
		if p[0] == "**" {
			if len(p) == 1 {
				return true
			}
			for i := 0; i <= len(n); i++ {
				if matchSegments(p[1:], n[i:]) {
					return true
				}
			}
			return false
		}
		if len(n) == 0 {
			return false
		}
		if ok, _ := path.Match(p[0], n[0]); !ok {
			return false
		}
		p, n = p[1:], n[1:]
	}
	return len(n) == 0
}

func (s *Server) enqueuePush(ctx context.Context, w http.ResponseWriter, app store.Application, branch, sha, msg string, files []string) error {
	res, err := s.pushOutcome(ctx, app, branch, sha, msg, files)
	if err != nil {
		return err
	}
	return accepted(w, res)
}

// pushOutcome decides whether a push deploys app and enqueues the deployment.
func (s *Server) pushOutcome(ctx context.Context, app store.Application, branch, sha, msg string, files []string) (webhookResult, error) {
	res := webhookResult{Application: app.Name}
	switch {
	case !app.AutoDeploy:
		res.Status, res.Message = "ignored", "auto deploy is disabled"
	case branch != app.GitBranch:
		res.Status, res.Message = "ignored", "branch "+branch+" is not "+app.GitBranch
	case !matchesWatchPaths(app.WatchPaths, files):
		res.Status, res.Message = "ignored", "no changes in watch paths"
	default:
		d, err := s.engine.Enqueue(ctx, app, deploy.Options{Trigger: "webhook", CommitSHA: sha, CommitMessage: firstLine(msg), Branch: branch,
			NeedsApproval: s.automationNeedsApproval(ctx, app, 0)})
		if err != nil {
			return res, err
		}
		s.db.LogActivity(ctx, 0, "webhook.deploy", app.Name+"@"+short(sha))
		res.Status, res.DeploymentID = d.Status, d.ID
	}
	return res, nil
}

func (s *Server) handlePullRequest(ctx context.Context, w http.ResponseWriter, app store.Application, action string, number int, branch, sha, title string) error {
	res, err := s.pullRequestOutcome(ctx, app, action, number, branch, sha, title)
	if err != nil {
		return err
	}
	return accepted(w, res)
}

// pullRequestOutcome creates, updates or removes the preview deployment of a PR.
func (s *Server) pullRequestOutcome(ctx context.Context, app store.Application, action string, number int, branch, sha, title string) (webhookResult, error) {
	res := webhookResult{Application: app.Name}
	if !app.PreviewEnabled {
		res.Status, res.Message = "ignored", "preview deployments are disabled"
		return res, nil
	}
	switch action {
	case "opened", "reopened", "synchronize", "open", "reopen", "update":
		d, err := s.engine.Enqueue(ctx, app, deploy.Options{Trigger: "webhook", PRNumber: number, Branch: branch, CommitSHA: sha, CommitMessage: firstLine(title)})
		if err != nil {
			return res, err
		}
		res.Status, res.DeploymentID = "queued", d.ID
	case "closed", "close", "merge", "merged":
		go func() {
			if err := s.engine.CleanupPreview(context.Background(), app, number); err != nil {
				slog.Warn("preview cleanup failed", "app", app.Name, "pr", number, "err", err)
			}
		}()
		_, _ = s.db.ExecContext(ctx, "DELETE FROM deployments WHERE application_id = ? AND pr_number = ? AND status NOT IN ('queued','running')", app.ID, number)
		res.Status, res.Message = "cleaned", "preview removed"
	default:
		res.Status, res.Message = "ignored", "action "+action
	}
	return res, nil
}

// githubWebhook handles GitHub and Gitea (same payload shape).
func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) error {
	app, err := s.appByUUID(r)
	if err != nil {
		return err
	}
	body, err := readBody(r)
	if err != nil {
		return badRequest("could not read body")
	}
	sig := r.Header.Get("X-Hub-Signature-256")
	if sig == "" {
		sig = r.Header.Get("X-Gitea-Signature")
	}
	if !validHMAC(app.WebhookSecret, body, sig) {
		return errStatus(401, "invalid signature")
	}
	event := r.Header.Get("X-GitHub-Event")
	if event == "" {
		event = r.Header.Get("X-Gitea-Event")
	}
	switch event {
	case "ping":
		return accepted(w, webhookResult{Status: "pong"})
	case "push":
		var p struct {
			Ref        string      `json:"ref"`
			After      string      `json:"after"`
			Deleted    bool        `json:"deleted"`
			Commits    []gitCommit `json:"commits"`
			HeadCommit *gitCommit  `json:"head_commit"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return badRequest("invalid payload")
		}
		if p.Deleted || !strings.HasPrefix(p.Ref, "refs/heads/") {
			return accepted(w, webhookResult{Status: "ignored", Message: "not a branch push"})
		}
		msg := ""
		if p.HeadCommit != nil {
			msg = p.HeadCommit.Message
		}
		return s.enqueuePush(r.Context(), w, app, strings.TrimPrefix(p.Ref, "refs/heads/"), p.After, msg, changedFiles(p.Commits))
	case "pull_request":
		var p struct {
			Action      string `json:"action"`
			Number      int    `json:"number"`
			PullRequest struct {
				Title string `json:"title"`
				Head  struct {
					Ref string `json:"ref"`
					SHA string `json:"sha"`
				} `json:"head"`
				Base struct {
					Ref string `json:"ref"`
				} `json:"base"`
			} `json:"pull_request"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return badRequest("invalid payload")
		}
		if p.PullRequest.Base.Ref != "" && p.PullRequest.Base.Ref != app.GitBranch {
			return accepted(w, webhookResult{Status: "ignored", Message: "PR does not target " + app.GitBranch})
		}
		return s.handlePullRequest(r.Context(), w, app, p.Action, p.Number, p.PullRequest.Head.Ref, p.PullRequest.Head.SHA, p.PullRequest.Title)
	}
	return accepted(w, webhookResult{Status: "ignored", Message: "event " + event})
}

func (s *Server) gitlabWebhook(w http.ResponseWriter, r *http.Request) error {
	app, err := s.appByUUID(r)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Gitlab-Token")), []byte(app.WebhookSecret)) != 1 {
		return errStatus(401, "invalid token")
	}
	body, err := readBody(r)
	if err != nil {
		return badRequest("could not read body")
	}
	switch r.Header.Get("X-Gitlab-Event") {
	case "Push Hook":
		var p struct {
			Ref         string      `json:"ref"`
			CheckoutSHA string      `json:"checkout_sha"`
			Commits     []gitCommit `json:"commits"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return badRequest("invalid payload")
		}
		if p.CheckoutSHA == "" {
			return accepted(w, webhookResult{Status: "ignored", Message: "branch deleted"})
		}
		msg := ""
		if n := len(p.Commits); n > 0 {
			msg = p.Commits[n-1].Message
		}
		return s.enqueuePush(r.Context(), w, app, strings.TrimPrefix(p.Ref, "refs/heads/"), p.CheckoutSHA, msg, changedFiles(p.Commits))
	case "Merge Request Hook":
		var p struct {
			Attrs struct {
				IID          int    `json:"iid"`
				Action       string `json:"action"`
				Title        string `json:"title"`
				SourceBranch string `json:"source_branch"`
				TargetBranch string `json:"target_branch"`
				LastCommit   struct {
					ID string `json:"id"`
				} `json:"last_commit"`
			} `json:"object_attributes"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return badRequest("invalid payload")
		}
		if p.Attrs.TargetBranch != app.GitBranch {
			return accepted(w, webhookResult{Status: "ignored", Message: "MR does not target " + app.GitBranch})
		}
		return s.handlePullRequest(r.Context(), w, app, p.Attrs.Action, p.Attrs.IID, p.Attrs.SourceBranch, p.Attrs.LastCommit.ID, p.Attrs.Title)
	}
	return accepted(w, webhookResult{Status: "ignored"})
}

// deployHook is a generic trigger for any CI system:
//
//	curl -X POST "https://portdock.example.com/api/webhooks/deploy/<uuid>?token=<secret>&commit=<sha>&force=true"
func (s *Server) deployHook(w http.ResponseWriter, r *http.Request) error {
	app, err := s.appByUUID(r)
	if err != nil {
		return err
	}
	token := r.URL.Query().Get("token")
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		token = strings.TrimPrefix(h, "Bearer ")
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(app.WebhookSecret)) != 1 {
		return errStatus(401, "invalid token")
	}
	q := r.URL.Query()
	d, err := s.engine.Enqueue(r.Context(), app, deploy.Options{
		Trigger: "api", CommitSHA: q.Get("commit"), Branch: q.Get("branch"), ForceRebuild: q.Get("force") == "true",
		NeedsApproval: s.automationNeedsApproval(r.Context(), app, 0),
	})
	if err != nil {
		return err
	}
	return accepted(w, webhookResult{Status: d.Status, DeploymentID: d.ID})
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
