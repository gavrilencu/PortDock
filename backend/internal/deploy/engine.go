// Package deploy implements the CI/CD pipeline: a persistent deployment queue,
// git checkout, image builds, zero-downtime rollout, health checks, rollbacks,
// preview deployments, plus lifecycle management for databases and services.
package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/shipyard/shipyard/internal/config"
	"github.com/shipyard/shipyard/internal/executor"
	"github.com/shipyard/shipyard/internal/github"
	"github.com/shipyard/shipyard/internal/notify"
	"github.com/shipyard/shipyard/internal/secrets"
	"github.com/shipyard/shipyard/internal/store"
	"github.com/shipyard/shipyard/internal/stream"
)

type Engine struct {
	cfg    config.Config
	db     *store.DB
	box    *secrets.Box
	pool   *executor.Pool
	hub    *stream.Hub
	notify *notify.Notifier
	github *github.Client

	queue    chan int64
	mu       sync.Mutex
	running  map[int64]context.CancelFunc // deployment id -> cancel
	loggers  map[int64]*logger
	appLocks map[int64]*sync.Mutex
}

func New(cfg config.Config, db *store.DB, box *secrets.Box, pool *executor.Pool, hub *stream.Hub, n *notify.Notifier, gh *github.Client) *Engine {
	return &Engine{
		cfg: cfg, db: db, box: box, pool: pool, hub: hub, notify: n, github: gh,
		queue:    make(chan int64, 1024),
		running:  map[int64]context.CancelFunc{},
		loggers:  map[int64]*logger{},
		appLocks: map[int64]*sync.Mutex{},
	}
}

// Start launches the workers and re-queues deployments interrupted by a restart.
func (e *Engine) Start(ctx context.Context) {
	_, _ = e.db.ExecContext(ctx, "UPDATE deployments SET status = 'failed', finished_at = ?, logs = logs || ? WHERE status = 'running'",
		store.Now(), "\n[shipyard] Deployment interrupted by a Shipyard restart.\n")
	queued, _ := store.Select[store.Deployment](ctx, e.db, "SELECT id, uuid, application_id, status FROM deployments WHERE status = 'queued' ORDER BY id")
	for i := 0; i < e.cfg.MaxConcurrentBuilds; i++ {
		go e.worker(ctx)
	}
	for _, d := range queued {
		e.queue <- d.ID
	}
}

type Options struct {
	Trigger       string
	CommitSHA     string
	CommitMessage string
	Branch        string
	PRNumber      int
	ForceRebuild  bool
	RollbackOf    int64 // deployment whose image should be redeployed
	RequestedBy   int64 // user who triggered it (0 = automation)
	// NeedsApproval holds the deployment as pending_approval until a maintainer approves it.
	NeedsApproval bool
}

// Enqueue creates a queued deployment for app.
func (e *Engine) Enqueue(ctx context.Context, app store.Application, o Options) (store.Deployment, error) {
	if o.Trigger == "" {
		o.Trigger = "manual"
	}
	if o.Branch == "" {
		o.Branch = app.GitBranch
	}
	d := store.Deployment{
		UUID: secrets.RandomToken(8), ApplicationID: app.ID, Status: "queued", Trigger: o.Trigger,
		CommitSHA: o.CommitSHA, CommitMessage: o.CommitMessage, Branch: o.Branch, PRNumber: o.PRNumber,
		ForceRebuild: o.ForceRebuild, RollbackOf: o.RollbackOf, CreatedAt: store.Now(), RequestedBy: o.RequestedBy,
	}
	if o.NeedsApproval {
		d.Status = "pending_approval"
	}
	if o.RollbackOf != 0 {
		prev, err := store.ByID[store.Deployment](ctx, e.db, "deployments", o.RollbackOf)
		if err != nil || prev.ApplicationID != app.ID {
			return d, errors.New("rollback target not found")
		}
		if prev.Image == "" {
			return d, errors.New("rollback target has no image")
		}
		d.Image, d.CommitSHA, d.CommitMessage, d.Branch, d.PRNumber = prev.Image, prev.CommitSHA, prev.CommitMessage, prev.Branch, prev.PRNumber
	}
	if _, err := store.Insert(ctx, e.db, "deployments", &d); err != nil {
		return d, err
	}
	e.hub.Event("deployment", d.ID, d.Status)
	if d.Status == "pending_approval" {
		e.reportCommitStatus(app, d, "pending", "Waiting for deployment approval")
		requester := "automation"
		if o.RequestedBy != 0 {
			_ = e.db.QueryRowContext(ctx, "SELECT name FROM users WHERE id = ?", o.RequestedBy).Scan(&requester)
		}
		e.notify.Send(notify.Event{
			Name:    "deployment.approval_required",
			Title:   fmt.Sprintf("Approval required: %s", app.Name),
			Message: fmt.Sprintf("%s requested a deployment of %s (%s).", requester, app.Name, firstNonEmpty(short(d.CommitSHA), d.Branch, "latest")),
			URL:     fmt.Sprintf("%s/applications/%d/deployments/%d", e.PublicURL(ctx), app.ID, d.ID),
			Fields:  map[string]string{"Trigger": d.Trigger, "Branch": d.Branch},
		})
		return d, nil
	}
	e.hub.Event("application", app.ID, "queued")
	e.queue <- d.ID
	return d, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// ErrNotPending is returned when approving a deployment that no longer waits.
var ErrNotPending = errors.New("deployment is not waiting for approval")

// Review approves (queues) or rejects a pending deployment.
func (e *Engine) Review(ctx context.Context, id, reviewer int64, approve bool, comment string) (store.Deployment, error) {
	status := "rejected"
	if approve {
		status = "queued"
	}
	res, err := e.db.ExecContext(ctx, `UPDATE deployments SET status = ?, reviewed_by = ?, reviewed_at = ?, review_comment = ?,
		finished_at = CASE WHEN ? = 'rejected' THEN ? ELSE finished_at END WHERE id = ? AND status = 'pending_approval'`,
		status, reviewer, store.Now(), comment, status, store.Now(), id)
	if err != nil {
		return store.Deployment{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.Deployment{}, ErrNotPending
	}
	d, err := store.ByID[store.Deployment](ctx, e.db, "deployments", id)
	if err != nil {
		return d, err
	}
	e.hub.Event("deployment", d.ID, d.Status)
	if app, err := store.ByID[store.Application](ctx, e.db, "applications", d.ApplicationID); err == nil {
		if approve {
			e.hub.Event("application", app.ID, "queued")
		} else {
			e.reportCommitStatus(app, d, "error", "Deployment rejected")
		}
	}
	if approve {
		e.queue <- d.ID
	}
	return d, nil
}

// Cancel stops a queued or running deployment.
func (e *Engine) Cancel(ctx context.Context, id int64) error {
	e.mu.Lock()
	cancel, ok := e.running[id]
	e.mu.Unlock()
	if ok {
		cancel()
		return nil
	}
	res, err := e.db.ExecContext(ctx, "UPDATE deployments SET status = 'cancelled', finished_at = ? WHERE id = ? AND status IN ('queued', 'pending_approval')", store.Now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("deployment is not queued or running")
	}
	e.hub.Event("deployment", id, "cancelled")
	return nil
}

func (e *Engine) appLock(appID int64) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	l, ok := e.appLocks[appID]
	if !ok {
		l = &sync.Mutex{}
		e.appLocks[appID] = l
	}
	return l
}

func (e *Engine) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-e.queue:
			e.process(ctx, id)
		}
	}
}

func (e *Engine) process(parent context.Context, id int64) {
	d, err := store.ByID[store.Deployment](parent, e.db, "deployments", id)
	if err != nil || d.Status != "queued" {
		return
	}
	lock := e.appLock(d.ApplicationID)
	lock.Lock()
	defer lock.Unlock()

	// Re-read after acquiring the lock: it may have been cancelled meanwhile.
	if d, err = store.ByID[store.Deployment](parent, e.db, "deployments", id); err != nil || d.Status != "queued" {
		return
	}
	// Skip superseded builds: a newer queued deployment for the same app/PR wins.
	if d.RollbackOf == 0 {
		var newer int64
		_ = e.db.QueryRowContext(parent, "SELECT COUNT(*) FROM deployments WHERE application_id = ? AND pr_number = ? AND status = 'queued' AND id > ? AND rollback_of = 0",
			d.ApplicationID, d.PRNumber, d.ID).Scan(&newer)
		if newer > 0 {
			_, _ = e.db.ExecContext(parent, "UPDATE deployments SET status = 'cancelled', finished_at = ?, logs = ? WHERE id = ?",
				store.Now(), "Superseded by a newer deployment.\n", d.ID)
			e.hub.Event("deployment", d.ID, "cancelled")
			return
		}
	}

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	e.mu.Lock()
	e.running[id] = cancel
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.running, id)
		e.mu.Unlock()
	}()

	d.Status, d.StartedAt = "running", store.Now()
	_, _ = e.db.ExecContext(ctx, "UPDATE deployments SET status = ?, started_at = ? WHERE id = ?", d.Status, d.StartedAt, d.ID)
	e.hub.Event("deployment", d.ID, d.Status)

	log := newLogger(e, d.ID)
	e.mu.Lock()
	e.loggers[id] = log
	e.mu.Unlock()
	start := time.Now()
	runErr := e.runApplication(ctx, &d, log)

	status := "success"
	switch {
	case errors.Is(ctx.Err(), context.Canceled) && parent.Err() == nil:
		status = "cancelled"
		log.Printf("Deployment cancelled.")
	case runErr != nil:
		status = "failed"
		log.Printf("❌ Deployment failed: %v", runErr)
	default:
		log.Printf("✅ Deployment finished successfully in %s.", time.Since(start).Round(time.Second))
	}
	log.Close()
	// Unregister before publishing the final status: late subscribers then
	// fall back to the database and still receive the status message.
	e.mu.Lock()
	delete(e.loggers, id)
	e.mu.Unlock()

	bg := context.WithoutCancel(ctx)
	_, _ = e.db.ExecContext(bg, "UPDATE deployments SET status = ?, finished_at = ?, image = ?, commit_sha = ?, commit_message = ?, logs = ? WHERE id = ?",
		status, store.Now(), d.Image, d.CommitSHA, d.CommitMessage, log.String(), d.ID)
	e.hub.Publish(deploymentTopic(d.ID), "status", status)
	e.hub.Event("deployment", d.ID, status)

	app, err := store.ByID[store.Application](bg, e.db, "applications", d.ApplicationID)
	if err != nil {
		return
	}
	if d.PRNumber == 0 {
		switch status {
		case "success":
			app.Status, app.CurrentImage = "running", d.Image
		case "failed", "cancelled":
			// Keep the previous version's status if it's still serving.
			app.Status = e.liveStatus(bg, app)
		}
		_, _ = e.db.ExecContext(bg, "UPDATE applications SET status = ?, current_image = ?, updated_at = ? WHERE id = ?", app.Status, app.CurrentImage, store.Now(), app.ID)
		e.hub.Event("application", app.ID, app.Status)
	}
	switch status {
	case "success":
		e.reportCommitStatus(app, d, "success", "Deployed successfully")
	case "failed":
		desc := "Deployment failed"
		if runErr != nil {
			desc = "Deployment failed: " + runErr.Error()
		}
		e.reportCommitStatus(app, d, "failure", desc)
	case "cancelled":
		e.reportCommitStatus(app, d, "error", "Deployment cancelled")
	}
	if status != "cancelled" {
		ev := notify.Event{
			Name:    "deployment." + status,
			Title:   fmt.Sprintf("Deployment %s: %s", status, app.Name),
			Success: status == "success",
			URL:     fmt.Sprintf("%s/applications/%d/deployments/%d", e.PublicURL(bg), app.ID, d.ID),
			Fields:  map[string]string{"Trigger": d.Trigger, "Branch": d.Branch, "Commit": short(d.CommitSHA)},
		}
		if d.PRNumber > 0 {
			ev.Fields["Preview"] = fmt.Sprintf("PR #%d", d.PRNumber)
		}
		if runErr != nil {
			ev.Message = runErr.Error()
		}
		e.notify.Send(ev)
	}
}

// AttachLogs atomically snapshots a running deployment's log and subscribes
// to new lines, so no line is lost or duplicated. live is false when the
// deployment is not currently running (the subscription is still active).
func (e *Engine) AttachLogs(id int64) (snapshot string, ch <-chan stream.Message, unsub func(), live bool) {
	e.mu.Lock()
	l, ok := e.loggers[id]
	e.mu.Unlock()
	if !ok {
		ch, unsub = e.hub.Subscribe(deploymentTopic(id))
		return "", ch, unsub, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	ch, unsub = e.hub.Subscribe(deploymentTopic(id))
	return l.buf.String(), ch, unsub, true
}

// reportCommitStatus posts the deployment state to GitHub for GitHub App sources.
func (e *Engine) reportCommitStatus(app store.Application, d store.Deployment, state, description string) {
	if e.github == nil || app.GitHubAppID == 0 || app.GitHubInstallation == 0 || app.GitFullName == "" || len(d.CommitSHA) < 7 {
		return
	}
	target := fmt.Sprintf("%s/applications/%d/deployments/%d", e.PublicURL(context.Background()), app.ID, d.ID)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ghApp, err := e.github.App(ctx, app.GitHubAppID)
		if err != nil {
			return
		}
		if err := e.github.SetStatus(ctx, ghApp, app.GitHubInstallation, app.GitFullName, d.CommitSHA, state, description, target); err != nil {
			slog.Warn("commit status", "app", app.Name, "err", err)
		}
	}()
}

func deploymentTopic(id int64) string { return fmt.Sprintf("deployment:%d", id) }

// DeploymentTopic is the hub topic carrying live log lines for a deployment.
func DeploymentTopic(id int64) string { return deploymentTopic(id) }

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// ---- logger ----

type logger struct {
	e       *Engine
	id      int64
	mu      sync.Mutex
	buf     bytes.Buffer
	partial []byte
	masks   []string
	dirty   bool
	stop    chan struct{}
	done    chan struct{}
}

func newLogger(e *Engine, id int64) *logger {
	l := &logger{e: e, id: id, stop: make(chan struct{}), done: make(chan struct{})}
	go l.flusher()
	return l
}

// Mask hides secret values in the log output.
func (l *logger) Mask(values ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, v := range values {
		if len(v) >= 4 {
			l.masks = append(l.masks, v)
		}
	}
}

func (l *logger) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	data := append(l.partial, p...)
	for {
		i := bytes.IndexAny(data, "\n\r")
		if i < 0 {
			break
		}
		line := string(data[:i])
		data = data[i+1:]
		if strings.TrimSpace(line) == "" {
			continue
		}
		l.lineLocked(line)
	}
	l.partial = append(l.partial[:0], data...)
	if len(l.partial) > 64*1024 {
		l.lineLocked(string(l.partial))
		l.partial = l.partial[:0]
	}
	return len(p), nil
}

func (l *logger) lineLocked(line string) {
	for _, m := range l.masks {
		line = strings.ReplaceAll(line, m, "********")
	}
	line = time.Now().UTC().Format("15:04:05") + " " + line
	l.buf.WriteString(line + "\n")
	l.dirty = true
	l.e.hub.Publish(deploymentTopic(l.id), "log", line)
}

func (l *logger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lineLocked(fmt.Sprintf(format, args...))
}

func (l *logger) Step(name string) { l.Printf("━━━ %s", name) }

func (l *logger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func (l *logger) flusher() {
	defer close(l.done)
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-t.C:
			l.mu.Lock()
			if l.dirty {
				logs := l.buf.String()
				l.dirty = false
				l.mu.Unlock()
				_, err := l.e.db.ExecContext(context.Background(), "UPDATE deployments SET logs = ? WHERE id = ?", logs, l.id)
				if err != nil {
					slog.Warn("flush logs", "err", err)
				}
				continue
			}
			l.mu.Unlock()
		}
	}
}

func (l *logger) Close() {
	l.mu.Lock()
	if len(l.partial) > 0 {
		l.lineLocked(string(l.partial))
		l.partial = nil
	}
	l.mu.Unlock()
	close(l.stop)
	<-l.done
}
