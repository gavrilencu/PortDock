package deploy

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/store"
	"github.com/portdock/portdock/internal/templates"
)

// Database backups run as jobs: a backup or restore that is asked for while
// another job of the same database runs waits as "pending", and at most
// backupsPerServer jobs run on a server at the same time. The commands follow
// each engine's documentation (see templates.Engines) so the database keeps
// serving while it is backed up.

const (
	BackupScheduled  = "scheduled"
	BackupManual     = "manual"
	BackupPreRestore = "pre-restore" // taken automatically before a restore

	backupsPerServer = 2
	preRestoreKept   = 3
	backupTimeout    = 6 * time.Hour
)

type backupJobs struct {
	mu      sync.Mutex
	dbs     map[int64]*sync.Mutex
	servers map[int64]chan struct{}
}

var jobs = &backupJobs{dbs: map[int64]*sync.Mutex{}, servers: map[int64]chan struct{}{}}

// acquire serializes jobs per database and limits them per server.
func (j *backupJobs) acquire(db store.Database) func() {
	j.mu.Lock()
	l, ok := j.dbs[db.ID]
	if !ok {
		l = &sync.Mutex{}
		j.dbs[db.ID] = l
	}
	sem, ok := j.servers[db.ServerID]
	if !ok {
		sem = make(chan struct{}, backupsPerServer)
		j.servers[db.ServerID] = sem
	}
	j.mu.Unlock()
	l.Lock()
	sem <- struct{}{}
	return func() {
		<-sem
		l.Unlock()
	}
}

func (e *Engine) backupDir(db store.Database) string {
	return path.Join(e.cfg.DataDir, "backups", db.UUID)
}

// BackupDir is where the backups of db are stored on its server.
func (e *Engine) BackupDir(db store.Database) string { return e.backupDir(db) }

// BackupSupported reports whether PortDock can back up db's engine.
func BackupSupported(db store.Database) bool {
	eng, ok := templates.Engines[db.Engine]
	return ok && eng.BackupCmd("u", "p", "d") != ""
}

// ManualBackup returns the (unscheduled) backup row that manual and safety
// backups of db belong to, creating it on first use.
func (e *Engine) ManualBackup(ctx context.Context, dbID int64) (store.Backup, error) {
	b, err := store.Get[store.Backup](ctx, e.db, "SELECT * FROM backups WHERE database_id = ? AND schedule = '' ORDER BY id LIMIT 1", dbID)
	if err == nil {
		return b, nil
	}
	b = store.Backup{DatabaseID: dbID, Schedule: "", Retention: 0, Enabled: false, CreatedAt: store.Now()}
	_, err = store.Insert(ctx, e.db, "backups", &b)
	return b, err
}

// EnqueueBackup records a pending backup of b's database and runs it in the background.
func (e *Engine) EnqueueBackup(ctx context.Context, b store.Backup, kind string) (store.BackupRun, error) {
	db, err := store.ByID[store.Database](ctx, e.db, "databases", b.DatabaseID)
	if err != nil {
		return store.BackupRun{}, err
	}
	if !BackupSupported(db) {
		return store.BackupRun{}, fmt.Errorf("backups are not supported for %s", templates.Engines[db.Engine].Name)
	}
	run := store.BackupRun{BackupID: b.ID, Kind: kind, Status: "pending", CreatedAt: store.Now()}
	if _, err := store.Insert(ctx, e.db, "backup_runs", &run); err != nil {
		return run, err
	}
	e.hub.Event("database", db.ID, db.Status)
	go func() {
		release := jobs.acquire(db)
		defer release()
		_ = e.backup(context.Background(), b, db, &run)
	}()
	return run, nil
}

// EnqueueAllBackups queues a manual backup of every database that supports backups.
func (e *Engine) EnqueueAllBackups(ctx context.Context) (int, error) {
	dbs, err := store.Select[store.Database](ctx, e.db, "SELECT * FROM databases ORDER BY id")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, db := range dbs {
		if !BackupSupported(db) {
			continue
		}
		b, err := e.ManualBackup(ctx, db.ID)
		if err != nil {
			return n, err
		}
		if _, err := e.EnqueueBackup(ctx, b, BackupManual); err == nil {
			n++
		}
	}
	return n, nil
}

// backup dumps the database into the server's backup directory. The caller holds the job slot.
func (e *Engine) backup(ctx context.Context, b store.Backup, db store.Database, run *store.BackupRun) error {
	ctx, cancel := context.WithTimeout(ctx, backupTimeout)
	defer cancel()
	eng := templates.Engines[db.Engine]
	dir := e.backupDir(db)
	run.Status, run.StartedAt = "running", store.Now()
	// The path is recorded before the dump starts, so an interrupted backup can be cleaned up.
	run.File = fmt.Sprintf("%s/%s-%s-%d.%s", dir, time.Now().UTC().Format("20060102-150405"), run.Kind, run.ID, eng.BackupExt)
	_ = store.Update(ctx, e.db, "backup_runs", run)
	e.hub.Event("database", db.ID, db.Status)

	var warning string
	err := func() error {
		x, _, err := e.pool.ForID(ctx, db.ServerID)
		if err != nil {
			return err
		}
		if state, _, err := docker.State(ctx, x, db.UUID); err != nil || state != "running" {
			return errors.New("the database is not running; start it to back it up")
		}
		cmd := eng.BackupCmd(db.Username, e.dbPassword(db), db.DatabaseName)
		// pipefail so a failing dump isn't hidden by gzip; the dump goes to a
		// temporary name first so a half-written file never looks complete.
		const pipefail = "(set -o pipefail) 2>/dev/null && set -o pipefail; "
		part := run.File + ".part"
		script := fmt.Sprintf("mkdir -p %s && docker exec %s sh -c %s > %s && mv -f %s %s && stat -c %%s %s",
			q(dir), q(db.UUID), q(pipefail+cmd), q(part), q(part), q(run.File), q(run.File))
		out, err := executor.Output(ctx, x, script)
		if err != nil {
			_ = x.Run(context.WithoutCancel(ctx), "rm -f "+q(part)+" "+q(run.File), executor.RunOpts{})
			return err
		}
		run.Size, _ = strconv.ParseInt(strings.TrimSpace(out), 10, 64)
		if run.Size == 0 {
			return errors.New("the backup is empty")
		}
		if b.UploadS3 && run.Kind == BackupScheduled {
			if err := e.uploadS3(ctx, x, run.File, db.UUID); err != nil {
				// The local backup is complete and usable: keep it, and warn.
				warning = "stored on the server, but the S3 upload failed: " + err.Error()
			}
		}
		return nil
	}()

	bg := context.WithoutCancel(ctx)
	run.Status, run.Message, run.FinishedAt = "success", warning, store.Now()
	if err != nil {
		run.Status, run.Message = "failed", err.Error()
		if run.Size == 0 {
			run.File = ""
		}
	}
	_ = store.Update(bg, e.db, "backup_runs", run)
	_, _ = e.db.ExecContext(bg, "UPDATE backups SET last_run_at = ? WHERE id = ?", store.Now(), b.ID)
	e.hub.Event("backup", b.ID, run.Status)
	e.hub.Event("database", db.ID, db.Status)
	if err != nil {
		e.notify.Send(notify.Event{Name: "backup.failed", Title: "Backup failed: " + db.Name, Message: err.Error()})
		return err
	}
	if warning != "" {
		e.notify.Send(notify.Event{Name: "backup.failed", Title: "Backup upload failed: " + db.Name, Message: warning})
	}
	e.pruneBackups(bg, b, db, run.Kind)
	return nil
}

// pruneBackups applies retention: a schedule keeps its last N backups, safety
// backups taken before a restore keep the last few, manual backups stay until deleted.
func (e *Engine) pruneBackups(ctx context.Context, b store.Backup, db store.Database, kind string) {
	keep := 0
	switch kind {
	case BackupScheduled:
		keep = b.Retention
	case BackupPreRestore:
		keep = preRestoreKept
	}
	if keep <= 0 {
		return
	}
	old, err := store.Select[store.BackupRun](ctx, e.db,
		"SELECT * FROM backup_runs WHERE backup_id = ? AND kind = ? AND status = 'success' AND restore_status NOT IN ('pending', 'running') AND verify_status NOT IN ('pending', 'running') ORDER BY id DESC LIMIT -1 OFFSET ?",
		b.ID, kind, keep)
	if err != nil || len(old) == 0 {
		return
	}
	x, _, err := e.pool.ForID(ctx, db.ServerID)
	if err != nil {
		return
	}
	for _, r := range old {
		if strings.HasPrefix(r.File, e.backupDir(db)+"/") {
			_ = x.Run(ctx, "rm -f "+q(r.File), executor.RunOpts{})
		}
		_ = store.Delete(ctx, e.db, "backup_runs", r.ID)
	}
}

// EnqueueRestore queues restoring run into db. Before the restore, a safety
// backup of the current data is taken; if it fails, nothing is restored.
func (e *Engine) EnqueueRestore(ctx context.Context, db store.Database, run store.BackupRun) error {
	eng := templates.Engines[db.Engine]
	if !eng.Restore || eng.RestoreCmd("u", "p", "d", run.File) == "" {
		return fmt.Errorf("restoring %s backups from PortDock isn't supported; download the file to restore it manually", eng.Name)
	}
	if !strings.HasPrefix(run.File, e.backupDir(db)+"/") {
		return errors.New("invalid backup file")
	}
	if run.RestoreStatus == "pending" || run.RestoreStatus == "running" {
		return errors.New("this backup is already being restored")
	}
	run.RestoreStatus, run.RestoreMessage = "pending", ""
	if err := store.Update(ctx, e.db, "backup_runs", &run); err != nil {
		return err
	}
	e.hub.Event("database", db.ID, db.Status)
	go func() {
		release := jobs.acquire(db)
		defer release()
		e.restore(context.Background(), db, run)
	}()
	return nil
}

func (e *Engine) restore(ctx context.Context, db store.Database, run store.BackupRun) {
	ctx, cancel := context.WithTimeout(ctx, backupTimeout)
	defer cancel()
	set := func(status, msg string) {
		run.RestoreStatus, run.RestoreMessage = status, msg
		if status == "success" || status == "failed" {
			run.RestoredAt = store.Now()
		}
		_ = store.Update(context.WithoutCancel(ctx), e.db, "backup_runs", &run)
		e.hub.Event("database", db.ID, db.Status)
	}
	set("running", "Taking a safety backup of the current data…")

	fail := func(err error) {
		set("failed", err.Error())
		e.notify.Send(notify.Event{Name: "backup.restore_failed", Title: "Restore failed: " + db.Name, Message: err.Error()})
	}
	manual, err := e.ManualBackup(ctx, db.ID)
	if err != nil {
		fail(err)
		return
	}
	safety := store.BackupRun{BackupID: manual.ID, Kind: BackupPreRestore, Status: "pending", CreatedAt: store.Now()}
	if _, err := store.Insert(ctx, e.db, "backup_runs", &safety); err != nil {
		fail(err)
		return
	}
	if err := e.backup(ctx, manual, db, &safety); err != nil {
		fail(fmt.Errorf("nothing was restored: the safety backup of the current data failed: %w", err))
		return
	}
	set("running", "Restoring…")

	eng := templates.Engines[db.Engine]
	x, _, err := e.pool.ForID(ctx, db.ServerID)
	if err != nil {
		fail(err)
		return
	}
	cmd := eng.RestoreCmd(db.Username, e.dbPassword(db), db.DatabaseName, path.Base(run.File))
	if _, err := executor.Output(ctx, x, fmt.Sprintf("cat %s | docker exec -i %s sh -c %s", q(run.File), q(db.UUID), q(cmd))); err != nil {
		fail(fmt.Errorf("%w (the data before the restore is kept in the safety backup of %s)", err, safety.CreatedAt))
		return
	}
	set("success", "")
}

// DeleteBackupRun removes a finished backup and its file.
func (e *Engine) DeleteBackupRun(ctx context.Context, db store.Database, run store.BackupRun) error {
	if run.Status == "pending" || run.Status == "running" || run.RestoreStatus == "pending" || run.RestoreStatus == "running" || run.VerifyStatus == "pending" || run.VerifyStatus == "running" {
		return errors.New("this backup is in use; wait until it finishes")
	}
	if run.File != "" {
		if !strings.HasPrefix(run.File, e.backupDir(db)+"/") {
			return errors.New("invalid backup file")
		}
		x, _, err := e.pool.ForID(ctx, db.ServerID)
		if err != nil {
			return err
		}
		if err := x.Run(ctx, "rm -f "+q(run.File), executor.RunOpts{}); err != nil {
			return err
		}
	}
	return store.Delete(ctx, e.db, "backup_runs", run.ID)
}

// RecoverBackups marks jobs that a restart of PortDock interrupted as failed
// and removes their unfinished files.
func (e *Engine) RecoverBackups(ctx context.Context) {
	runs, err := store.Select[store.BackupRun](ctx, e.db, "SELECT * FROM backup_runs WHERE status IN ('pending', 'running') OR restore_status IN ('pending', 'running') OR verify_status IN ('pending', 'running')")
	if err != nil {
		return
	}
	for _, r := range runs {
		if r.Status == "pending" || r.Status == "running" {
			if r.File != "" {
				if b, err := store.ByID[store.Backup](ctx, e.db, "backups", r.BackupID); err == nil {
					if db, err := store.ByID[store.Database](ctx, e.db, "databases", b.DatabaseID); err == nil {
						if x, _, err := e.pool.ForID(ctx, db.ServerID); err == nil && strings.HasPrefix(r.File, e.backupDir(db)+"/") {
							_ = x.Run(ctx, "rm -f "+q(r.File)+" "+q(r.File+".part"), executor.RunOpts{})
						}
					}
				}
			}
			r.Status, r.Message, r.File, r.FinishedAt = "failed", "interrupted: PortDock restarted", "", store.Now()
		}
		if r.RestoreStatus == "pending" || r.RestoreStatus == "running" {
			r.RestoreStatus, r.RestoreMessage, r.RestoredAt = "failed", "interrupted: PortDock restarted; check the database and restore again", store.Now()
		}
		if r.VerifyStatus == "pending" || r.VerifyStatus == "running" {
			r.VerifyStatus, r.VerifyMessage, r.VerifiedAt = "failed", "interrupted: PortDock restarted", store.Now()
			if b, err := store.ByID[store.Backup](ctx, e.db, "backups", r.BackupID); err == nil {
				if db, err := store.ByID[store.Database](ctx, e.db, "databases", b.DatabaseID); err == nil {
					if x, _, err := e.pool.ForID(ctx, db.ServerID); err == nil {
						_ = x.Run(ctx, fmt.Sprintf("docker rm -f -v %s-verify-%d >/dev/null 2>&1 || true", db.UUID, r.ID), executor.RunOpts{})
					}
				}
			}
		}
		_ = store.Update(ctx, e.db, "backup_runs", &r)
	}
}
