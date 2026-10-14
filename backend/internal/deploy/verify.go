package deploy

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/store"
	"github.com/portdock/portdock/internal/templates"
)

// A backup only counts once it has been restored. A test restore starts a
// temporary container of the database's image, with no network and no
// volume of the database, restores the backup into it, counts the tables
// (collections, keys) and removes it. The database itself is not touched.

const (
	// LabelVerify marks the temporary containers of test restores (value: the database UUID).
	LabelVerify   = "portdock.verify"
	verifyTimeout = 3 * time.Hour
	verifyStartup = 10 * time.Minute
)

// LatestBackup returns the newest successful backup of a schedule (or of
// the database when backupID is 0).
func (e *Engine) LatestBackup(ctx context.Context, dbID, backupID int64) (store.BackupRun, error) {
	q := `SELECT r.* FROM backup_runs r JOIN backups b ON b.id = r.backup_id WHERE b.database_id = ? AND r.status = 'success' AND r.file != ''`
	args := []any{dbID}
	if backupID != 0 {
		q += " AND r.backup_id = ?"
		args = append(args, backupID)
	}
	return store.Get[store.BackupRun](ctx, e.db, q+" ORDER BY r.id DESC LIMIT 1", args...)
}

// EnqueueVerify queues a test restore of run.
func (e *Engine) EnqueueVerify(ctx context.Context, db store.Database, run store.BackupRun) error {
	eng := templates.Engines[db.Engine]
	if !eng.Verifiable() {
		return fmt.Errorf("test restores of %s backups aren't supported", eng.Name)
	}
	if run.Status != "success" || run.File == "" {
		return errors.New("only a successful backup can be test-restored")
	}
	if run.VerifyStatus == "pending" || run.VerifyStatus == "running" {
		return errors.New("a test restore of this backup is already running")
	}
	run.VerifyStatus, run.VerifyMessage = "pending", ""
	if err := store.Update(ctx, e.db, "backup_runs", &run); err != nil {
		return err
	}
	e.hub.Event("database", db.ID, db.Status)
	go func() {
		bg := context.WithoutCancel(ctx)
		release := jobs.server(db.ServerID)
		defer release()
		e.verify(bg, db, run)
	}()
	return nil
}

// server takes one of the server's job slots (a test restore does not lock
// the database: backups and restores of it go on).
func (j *backupJobs) server(serverID int64) func() {
	j.mu.Lock()
	sem, ok := j.servers[serverID]
	if !ok {
		sem = make(chan struct{}, backupsPerServer)
		j.servers[serverID] = sem
	}
	j.mu.Unlock()
	sem <- struct{}{}
	return func() { <-sem }
}

func (e *Engine) verify(ctx context.Context, db store.Database, run store.BackupRun) {
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	start := time.Now()
	set := func(status, msg string) {
		run.VerifyStatus, run.VerifyMessage = status, msg
		if status == "success" || status == "failed" {
			run.VerifiedAt = store.Now()
		}
		_ = store.Update(context.WithoutCancel(ctx), e.db, "backup_runs", &run)
		e.hub.Event("database", db.ID, db.Status)
	}
	set("running", "Starting a temporary container…")
	count, err := e.testRestore(ctx, db, run, func(msg string) { set("running", msg) })
	if err != nil {
		set("failed", err.Error())
		e.notify.Send(notify.Event{Name: "backup.verify_failed", Title: "Backup test restore failed: " + db.Name,
			Message: fmt.Sprintf("The backup of %s could not be restored: %v", run.CreatedAt, err)})
		return
	}
	eng := templates.Engines[db.Engine]
	set("success", fmt.Sprintf("Restored in %s: %d %s.", time.Since(start).Round(time.Second), count, eng.CheckUnit))
}

// testRestore restores run in a temporary container and returns what it counts.
func (e *Engine) testRestore(ctx context.Context, db store.Database, run store.BackupRun, progress func(string)) (int64, error) {
	eng := templates.Engines[db.Engine]
	x, _, err := e.pool.ForID(ctx, db.ServerID)
	if err != nil {
		return 0, err
	}
	if !strings.HasPrefix(run.File, e.backupDir(db)+"/") {
		return 0, errors.New("the backup file is not in the database's backup directory")
	}
	if _, err := executor.Output(ctx, x, "test -s "+q(run.File)); err != nil {
		return 0, errors.New("the backup file is missing on the server")
	}
	pass := e.dbPassword(db)
	name := fmt.Sprintf("%s-verify-%d", db.UUID, run.ID)
	health := eng.HealthCmd(db.Username, pass, db.DatabaseName)
	var b strings.Builder
	fmt.Fprintf(&b, "(docker rm -f -v %s >/dev/null 2>&1 || true) && docker run -d --name %s --network none", q(name), q(name))
	for k, v := range eng.Env(db.Username, pass, db.DatabaseName) {
		b.WriteString(" -e " + q(k+"="+v))
	}
	fmt.Fprintf(&b, " --health-cmd %s --health-interval 3s --health-timeout 5s --health-retries 1000", q(health))
	fmt.Fprintf(&b, " --label %s=true --label %s=verify --label %s=%s", docker.LabelManaged, docker.LabelKind, LabelVerify, q(db.UUID))
	if db.CPULimit != "" {
		b.WriteString(" --cpus " + q(db.CPULimit))
	}
	if db.MemoryLimit != "" {
		b.WriteString(" --memory " + q(db.MemoryLimit))
	}
	fromFile := eng.FromFileCmd(pass, path.Base(run.File))
	if fromFile != "" {
		fmt.Fprintf(&b, " -v %s:/restore:ro --entrypoint sh %s -c %s", q(path.Dir(run.File)), q(db.Image), q(fromFile))
	} else {
		b.WriteString(" " + q(db.Image))
		if cmd := eng.Command(pass); cmd != "" {
			b.WriteString(" " + cmd)
		}
		args, err := docker.CommandArgs(db.CustomArgs)
		if err != nil {
			return 0, err
		}
		b.WriteString(args)
	}
	defer func() {
		_, _ = executor.Output(context.WithoutCancel(ctx), x, "docker rm -f -v "+q(name)+" >/dev/null 2>&1 || true")
	}()
	if _, err := executor.Output(ctx, x, b.String()); err != nil {
		return 0, fmt.Errorf("start a temporary container: %w", err)
	}
	if err := waitHealthy(ctx, x, name); err != nil {
		return 0, err
	}
	if init := eng.InitCmd(db.Username, pass, db.DatabaseName); init != "" {
		if _, err := executor.Output(ctx, x, "docker exec "+q(name)+" sh -c "+q(init)); err != nil {
			return 0, fmt.Errorf("prepare the temporary database: %w", err)
		}
	}
	if fromFile == "" {
		progress("Restoring the backup into a temporary container…")
		cmd := eng.RestoreCmd(db.Username, pass, db.DatabaseName, path.Base(run.File))
		if _, err := executor.Output(ctx, x, fmt.Sprintf("cat %s | docker exec -i %s sh -c %s", q(run.File), q(name), q(cmd))); err != nil {
			return 0, fmt.Errorf("restore: %w", err)
		}
	}
	progress("Checking the restored data…")
	out, err := executor.Output(ctx, x, "docker exec "+q(name)+" sh -c "+q(eng.CheckCmd(db.Username, pass, db.DatabaseName)))
	if err != nil {
		return 0, fmt.Errorf("query the restored data: %w", err)
	}
	return lastNumber(out)
}

// waitHealthy waits until the container's health check passes.
func waitHealthy(ctx context.Context, x executor.Executor, name string) error {
	deadline := time.Now().Add(verifyStartup)
	for time.Now().Before(deadline) {
		out, err := executor.Output(ctx, x, "docker inspect -f '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}}' "+q(name))
		if err != nil {
			return err
		}
		switch f := strings.Fields(out); {
		case len(f) > 1 && f[1] == "healthy":
			return nil
		case len(f) > 0 && f[0] != "running" && f[0] != "created":
			logs, _ := executor.Output(ctx, x, "docker logs --tail 20 "+q(name)+" 2>&1")
			return fmt.Errorf("the temporary container stopped (%s): %s", f[0], strings.TrimSpace(logs))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return errors.New("the temporary container did not become ready in 10 minutes")
}

// lastNumber parses the last non-empty line of a query's output.
func lastNumber(out string) (int64, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	n, err := strconv.ParseInt(last, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("unexpected answer from the restored database: %q", last)
	}
	return n, nil
}
