// Package scheduler runs cron jobs: scheduled database backups and housekeeping.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

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
}

func New(db *store.DB, engine *deploy.Engine, pool *executor.Pool, n *notify.Notifier) *Scheduler {
	return &Scheduler{db: db, engine: engine, pool: pool, notify: n, cron: cron.New(), jobs: map[int64]cron.EntryID{}}
}

// Validate reports whether spec is a valid 5-field cron expression.
func Validate(spec string) error {
	_, err := cron.ParseStandard(spec)
	return err
}

func (s *Scheduler) Start(ctx context.Context) {
	s.Reload(ctx)
	_, _ = s.cron.AddFunc("30 4 * * *", func() { s.housekeeping(context.Background()) })
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
	backups, err := store.Select[store.Backup](ctx, s.db, "SELECT * FROM backups WHERE enabled = 1")
	if err != nil {
		return
	}
	tz := s.db.Setting(ctx, "instance_timezone")
	for _, b := range backups {
		id := b.ID
		spec := b.Schedule
		if tz != "" && !strings.HasPrefix(spec, "CRON_TZ=") && !strings.HasPrefix(spec, "TZ=") && !strings.HasPrefix(spec, "@every") {
			spec = "CRON_TZ=" + tz + " " + spec // schedules follow the instance timezone
		}
		entry, err := s.cron.AddFunc(spec, func() { s.runBackup(id) })
		if err != nil {
			slog.Warn("invalid backup schedule", "backup", b.ID, "schedule", b.Schedule, "err", err)
			continue
		}
		s.jobs[id] = entry
	}
}

func (s *Scheduler) runBackup(id int64) {
	ctx := context.Background()
	b, err := store.ByID[store.Backup](ctx, s.db, "backups", id)
	if err != nil || !b.Enabled {
		return
	}
	if _, err := s.engine.RunBackup(ctx, b); err != nil {
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
