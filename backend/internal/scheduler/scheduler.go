// Package scheduler runs cron jobs: scheduled database backups and housekeeping.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/catalogs"
	"github.com/portdock/portdock/internal/deploy"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/store"
	"github.com/robfig/cron/v3"
)

type Scheduler struct {
	db     *store.DB
	engine *deploy.Engine
	pool   *executor.Pool
	notify *notify.Notifier
	cron   *cron.Cron
	mu     sync.Mutex
	jobs   map[int64]cron.EntryID
	// verifyJobs are the test restores of backup schedules (by backup id).
	verifyJobs map[int64]cron.EntryID
	// pitrJobs are the base backups of databases with point-in-time recovery.
	pitrJobs map[int64]cron.EntryID
	// InstanceBackup backs up PortDock itself (instancebackup), on the
	// schedule of the instance_backup_* settings.
	InstanceBackup func(ctx context.Context)
	instanceJob    cron.EntryID
	appJobs        map[int64]cron.EntryID
}

func New(db *store.DB, engine *deploy.Engine, pool *executor.Pool, n *notify.Notifier) *Scheduler {
	return &Scheduler{db: db, engine: engine, pool: pool, notify: n, cron: cron.New(), jobs: map[int64]cron.EntryID{}, verifyJobs: map[int64]cron.EntryID{}, pitrJobs: map[int64]cron.EntryID{}, appJobs: map[int64]cron.EntryID{}}
}

// Validate reports whether spec is a valid 5-field cron expression.
func Validate(spec string) error {
	_, err := cron.ParseStandard(spec)
	return err
}

func (s *Scheduler) Start(ctx context.Context) {
	s.Reload(ctx)
	_, _ = s.cron.AddFunc("30 4 * * *", func() { s.housekeeping(context.Background()) })
	// New templates of the service catalogs that come from a URL.
	_, _ = s.cron.AddFunc("15 5 * * *", func() { catalogs.RefreshAll(context.Background(), s.db) })
	// The WAL archivers and the replicas of PostgreSQL databases.
	_, _ = s.cron.AddFunc("@every 1m", func() {
		s.engine.PITRTick(context.Background())
		s.engine.ReplicationTick(context.Background())
	})
	s.cron.Start()
	go func() {
		<-ctx.Done()
		s.cron.Stop()
	}()
}

// Reload re-registers all enabled backup schedules.
func (s *Scheduler) Reload(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, entry := range s.jobs {
		s.cron.Remove(entry)
		delete(s.jobs, id)
	}
	for id, entry := range s.verifyJobs {
		s.cron.Remove(entry)
		delete(s.verifyJobs, id)
	}
	for id, entry := range s.pitrJobs {
		s.cron.Remove(entry)
		delete(s.pitrJobs, id)
	}
	tz := s.db.Setting(ctx, "instance_timezone")
	s.reloadInstanceBackup(ctx, tz)
	s.reloadAppJobs(ctx, tz)
	s.reloadPITR(ctx, tz)
	backups, err := store.Select[store.Backup](ctx, s.db, "SELECT * FROM backups WHERE enabled = 1 AND schedule != ''")
	if err != nil {
		return
	}
	for _, b := range backups {
		id := b.ID
		entry, err := s.cron.AddFunc(withTZ(b.Schedule, tz), func() { s.runBackup(id) })
		if err != nil {
			slog.Warn("invalid backup schedule", "backup", b.ID, "schedule", b.Schedule, "err", err)
			continue
		}
		s.jobs[id] = entry
		if b.VerifySchedule != "" {
			entry, err := s.cron.AddFunc(withTZ(b.VerifySchedule, tz), func() { s.runVerify(id) })
			if err != nil {
				slog.Warn("invalid test restore schedule", "backup", b.ID, "schedule", b.VerifySchedule, "err", err)
				continue
			}
			s.verifyJobs[id] = entry
		}
	}
}

// reloadPITR registers the base backups of databases with point-in-time recovery.
func (s *Scheduler) reloadPITR(ctx context.Context, tz string) {
	dbs, err := store.Select[store.Database](ctx, s.db, "SELECT * FROM databases WHERE pitr = 1 AND pitr_schedule != ''")
	if err != nil {
		return
	}
	for _, d := range dbs {
		id := d.ID
		entry, err := s.cron.AddFunc(withTZ(d.PITRSchedule, tz), func() {
			ctx := context.Background()
			db, err := store.ByID[store.Database](ctx, s.db, "databases", id)
			if err != nil || !db.PITR {
				return
			}
			if _, err := s.engine.EnqueueBaseBackup(ctx, db); err != nil {
				s.notify.Send(notify.Event{Name: "backup.failed", Title: "Base backup failed: " + db.Name, Message: err.Error()})
			}
		})
		if err != nil {
			slog.Warn("invalid base backup schedule", "database", d.ID, "schedule", d.PITRSchedule, "err", err)
			continue
		}
		s.pitrJobs[id] = entry
	}
}

// runVerify test-restores the latest backup of a schedule.
func (s *Scheduler) runVerify(id int64) {
	ctx := context.Background()
	b, err := store.ByID[store.Backup](ctx, s.db, "backups", id)
	if err != nil || !b.Enabled {
		return
	}
	db, err := store.ByID[store.Database](ctx, s.db, "databases", b.DatabaseID)
	if err != nil {
		return
	}
	run, err := s.engine.LatestBackup(ctx, db.ID, b.ID)
	if err == nil {
		err = s.engine.EnqueueVerify(ctx, db, run)
	} else {
		err = fmt.Errorf("there is no successful backup to test")
	}
	if err != nil {
		s.notify.Send(notify.Event{Name: "backup.verify_failed", Title: "Backup test restore failed: " + db.Name, Message: err.Error()})
	}
}

// reloadAppJobs registers the enabled jobs of applications.
func (s *Scheduler) reloadAppJobs(ctx context.Context, tz string) {
	for id, entry := range s.appJobs {
		s.cron.Remove(entry)
		delete(s.appJobs, id)
	}
	jobs, err := store.Select[store.AppJob](ctx, s.db, "SELECT * FROM app_jobs WHERE enabled = 1")
	if err != nil {
		return
	}
	for _, j := range jobs {
		id := j.ID
		entry, err := s.cron.AddFunc(withTZ(j.Schedule, tz), func() {
			if _, err := s.engine.RunJob(context.Background(), id, "schedule", 0); err != nil {
				slog.Warn("scheduled job not started", "job", id, "err", err)
			}
		})
		if err != nil {
			slog.Warn("invalid job schedule", "job", j.ID, "schedule", j.Schedule, "err", err)
			continue
		}
		s.appJobs[id] = entry
	}
}

// NextRun returns when spec next fires in the instance timezone tz.
func NextRun(spec, tz string) (time.Time, error) {
	sched, err := cron.ParseStandard(withTZ(spec, tz))
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(time.Now()), nil
}

// withTZ makes a schedule follow the instance timezone.
func withTZ(spec, tz string) string {
	if tz != "" && !strings.HasPrefix(spec, "CRON_TZ=") && !strings.HasPrefix(spec, "TZ=") && !strings.HasPrefix(spec, "@every") {
		return "CRON_TZ=" + tz + " " + spec
	}
	return spec
}

func (s *Scheduler) reloadInstanceBackup(ctx context.Context, tz string) {
	if s.instanceJob != 0 {
		s.cron.Remove(s.instanceJob)
		s.instanceJob = 0
	}
	if s.InstanceBackup == nil || s.db.Setting(ctx, "instance_backup_enabled") != "true" {
		return
	}
	spec := s.db.Setting(ctx, "instance_backup_schedule")
	if spec == "" {
		spec = "0 3 * * *"
	}
	entry, err := s.cron.AddFunc(withTZ(spec, tz), func() { s.InstanceBackup(context.Background()) })
	if err != nil {
		slog.Warn("invalid instance backup schedule", "schedule", spec, "err", err)
		return
	}
	s.instanceJob = entry
}

func (s *Scheduler) runBackup(id int64) {
	ctx := context.Background()
	b, err := store.ByID[store.Backup](ctx, s.db, "backups", id)
	if err != nil || !b.Enabled {
		return
	}
	// The job itself notifies when the backup fails; this covers backups that can't start.
	if _, err := s.engine.EnqueueBackup(ctx, b, deploy.BackupScheduled); err != nil {
		db, _ := store.ByID[store.Database](ctx, s.db, "databases", b.DatabaseID)
		s.notify.Send(notify.Event{
			Name:    "backup.failed",
			Title:   fmt.Sprintf("Backup failed: %s", db.Name),
			Message: err.Error(),
		})
	}
}

// housekeeping trims old deployment logs and build caches.
func (s *Scheduler) housekeeping(ctx context.Context) {
	// Keep the 50 most recent deployments per application.
	_, _ = s.db.ExecContext(ctx, `DELETE FROM deployments WHERE id IN (
		SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY application_id ORDER BY id DESC) AS rn FROM deployments) WHERE rn > 50)`)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM activity WHERE id NOT IN (SELECT id FROM activity ORDER BY id DESC LIMIT 5000)`)
	_, _ = s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < ?", store.Now())
	if s.db.Setting(ctx, "auto_cleanup") == "false" {
		return
	}
	servers, _ := store.Select[store.Server](ctx, s.db, "SELECT * FROM servers WHERE status = 'reachable'")
	for _, srv := range servers {
		x, err := s.pool.For(ctx, srv)
		if err != nil {
			continue
		}
		_ = x.Run(ctx, "docker builder prune -af --filter until=72h >/dev/null 2>&1; docker image prune -f >/dev/null 2>&1; true", executor.RunOpts{})
	}
}
