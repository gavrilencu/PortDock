package deploy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/store"
)

// Point-in-time recovery of PostgreSQL, with the server's own tools:
//
//   - WAL archiving: every finished WAL segment (at least one a minute, see
//     archive_timeout) is gzipped into <backups>/<db>/pitr/wal on the
//     database's server, mounted at /pitr in the container;
//   - base backups: pg_basebackup on a schedule, into pitr/base; WAL older than
//     the oldest base backup kept is removed (pg_archivecleanup);
//   - optional copy of pitr/ to the S3 bucket (aws s3 sync);
//   - restore: the current data is kept aside in the data volume, the newest
//     base backup before the target is unpacked, and PostgreSQL replays the
//     archive up to the target time, then is promoted. If it fails, the data
//     that was there is put back.

const (
	pitrArchiveTimeout = 60 // seconds: the most recent changes a restore can miss
	pitrRecoveryLimit  = 2 * time.Hour
)

// pitrArchiveCommand stores a WAL segment once (an existing archive counts
// as done, so a segment archived just before a crash does not block the archiver).
const pitrArchiveCommand = `f=/pitr/wal/%f.gz; [ -f "$f" ] || { gzip -c "%p" > "$f.tmp" && mv "$f.tmp" "$f"; }`

// pitrDir holds the WAL archive and the base backups of db on its server.
func (e *Engine) pitrDir(db store.Database) string { return path.Join(e.backupDir(db), "pitr") }

// pitrArgs are the server settings of WAL archiving.
func pitrArgs() string {
	args := []string{
		"-c", "wal_level=replica",
		"-c", "archive_mode=on",
		"-c", "archive_timeout=" + strconv.Itoa(pitrArchiveTimeout),
		"-c", "archive_command=" + pitrArchiveCommand,
	}
	var b strings.Builder
	for _, a := range args {
		b.WriteString(" " + q(a))
	}
	return b.String()
}

// preparePITR creates the archive directories: wal/ belongs to the
// postgres user of the image (the archiver writes there), base/ to the
// server account (backups are written by the shell).
func (e *Engine) preparePITR(ctx context.Context, x executor.Executor, db store.Database) error {
	dir := e.pitrDir(db)
	_, err := executor.Output(ctx, x, fmt.Sprintf("mkdir -p %s/wal %s/base && docker run --rm -v %s:/pitr --entrypoint chown %s postgres:postgres /pitr/wal",
		q(dir), q(dir), q(dir), q(db.Image)))
	return err
}

// pitrBusy marks databases whose point-in-time restore is running.
var pitrBusy sync.Map

// SetPITR turns point-in-time recovery on or off (the container is recreated
// with or without WAL archiving) and saves its schedule.
func (e *Engine) SetPITR(ctx context.Context, db store.Database, enabled bool, schedule string, retention int, s3 bool) (store.Database, error) {
	if db.Engine != "postgres" {
		return db, errors.New("point-in-time recovery is available for PostgreSQL")
	}
	if _, busy := pitrBusy.Load(db.ID); busy {
		return db, errors.New("a point-in-time restore of this database is running")
	}
	if retention < 1 {
		retention = 1
	}
	changed := db.PITR != enabled
	db.PITR, db.PITRSchedule, db.PITRRetention, db.PITRS3 = enabled, schedule, retention, s3
	if !enabled {
		db.PITRArchiveError = ""
	}
	if err := store.Update(ctx, e.db, "databases", &db); err != nil {
		return db, err
	}
	if changed && db.Status == "running" {
		if err := e.StartDatabase(ctx, db, true); err != nil {
			return db, fmt.Errorf("saved, but the database could not be restarted: %w", err)
		}
	}
	if changed && enabled {
		// The first base backup starts the recovery window.
		go func() {
			bg := context.WithoutCancel(ctx)
			if err := e.waitDatabaseReady(bg, db, 5*time.Minute); err == nil {
				_, _ = e.EnqueueBaseBackup(bg, db)
			}
		}()
	}
	return db, nil
}

// waitDatabaseReady waits until the database answers queries.
func (e *Engine) waitDatabaseReady(ctx context.Context, db store.Database, limit time.Duration) error {
	x, _, err := e.pool.ForID(ctx, db.ServerID)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if _, err := executor.Output(ctx, x, "docker exec "+q(db.UUID)+" sh -c "+q(e.psql(db, "SELECT 1"))); err == nil {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return errors.New("the database did not start in time")
}

// psql returns a command printing the result of query, unaligned.
func (e *Engine) psql(db store.Database, query string) string {
	return "psql -X -U " + q(db.Username) + " -d " + q(db.DatabaseName) + " -tAc " + q(query)
}

// EnqueueBaseBackup takes a base backup now (after the database's other backup jobs).
func (e *Engine) EnqueueBaseBackup(ctx context.Context, db store.Database) (store.PITRBackup, error) {
	if !db.PITR {
		return store.PITRBackup{}, errors.New("point-in-time recovery is off")
	}
	b := store.PITRBackup{DatabaseID: db.ID, Status: "running", StartedAt: store.Now()}
	if _, err := store.Insert(ctx, e.db, "pitr_backups", &b); err != nil {
		return b, err
	}
	e.hub.Event("database", db.ID, db.Status)
	go func() {
		bg := context.WithoutCancel(ctx)
		release := jobs.acquire(db)
		defer release()
		if err := e.baseBackup(bg, db, &b); err != nil {
			e.notify.Send(notify.Event{Name: "backup.failed", Title: "Base backup failed: " + db.Name, Message: err.Error()})
		}
	}()
	return b, nil
}

var startWALRe = regexp.MustCompile(`START WAL LOCATION: \S+ \(file ([0-9A-F]{24})\)`)

func (e *Engine) baseBackup(ctx context.Context, db store.Database, b *store.PITRBackup) error {
	ctx, cancel := context.WithTimeout(ctx, backupTimeout)
	defer cancel()
	finish := func(err error) error {
		b.FinishedAt = store.Now()
		if err != nil {
			b.Status, b.Message = "failed", err.Error()
		} else {
			b.Status = "success"
		}
		_ = store.Update(context.WithoutCancel(ctx), e.db, "pitr_backups", b)
		e.hub.Event("database", db.ID, db.Status)
		return err
	}
	x, _, err := e.pool.ForID(ctx, db.ServerID)
	if err != nil {
		return finish(err)
	}
	if err := e.preparePITR(ctx, x, db); err != nil {
		return finish(err)
	}
	b.File = fmt.Sprintf("%s/base/%s-%d.tar.gz", e.pitrDir(db), time.Now().UTC().Format("20060102-150405"), b.ID)
	// -X none: the WAL comes from the archive, and the server waits until the
	// segments the backup needs are archived before it reports success.
	out, err := executor.Output(ctx, x, fmt.Sprintf(
		"docker exec %s pg_basebackup -U %s -D - -Ft -z -X none -c fast --no-manifest > %s.part && mv -f %s.part %s && stat -c %%s %s && tar -xzOf %s backup_label",
		q(db.UUID), q(db.Username), q(b.File), q(b.File), q(b.File), q(b.File), q(b.File)))
	if err != nil {
		_, _ = executor.Output(context.WithoutCancel(ctx), x, "rm -f "+q(b.File)+" "+q(b.File+".part"))
		return finish(fmt.Errorf("pg_basebackup: %w", err))
	}
	lines := strings.SplitN(strings.TrimSpace(out), "\n", 2)
	b.Size, _ = strconv.ParseInt(strings.TrimSpace(lines[0]), 10, 64)
	if m := startWALRe.FindStringSubmatch(out); m != nil {
		b.StartWAL = m[1]
	}
	if b.StartWAL == "" {
		return finish(errors.New("the base backup has no backup_label"))
	}
	if err := finish(nil); err != nil {
		return err
	}
	e.prunePITR(ctx, x, db)
	if db.PITRS3 {
		if err := e.syncPITR(ctx, x, db); err != nil {
			slog.Warn("pitr s3 sync", "database", db.Name, "err", err)
		}
	}
	return nil
}

// prunePITR keeps the last PITRRetention base backups and the WAL they need.
func (e *Engine) prunePITR(ctx context.Context, x executor.Executor, db store.Database) {
	keep := max(db.PITRRetention, 1)
	old, err := store.Select[store.PITRBackup](ctx, e.db,
		"SELECT * FROM pitr_backups WHERE database_id = ? AND status = 'success' ORDER BY id DESC LIMIT -1 OFFSET ?", db.ID, keep)
	if err != nil {
		return
	}
	for _, b := range old {
		if strings.HasPrefix(b.File, e.pitrDir(db)+"/base/") {
			_, _ = executor.Output(ctx, x, "rm -f "+q(b.File))
		}
		_ = store.Delete(ctx, e.db, "pitr_backups", b.ID)
	}
	oldest, err := store.Get[store.PITRBackup](ctx, e.db,
		"SELECT * FROM pitr_backups WHERE database_id = ? AND status = 'success' ORDER BY id ASC LIMIT 1", db.ID)
	if err != nil || oldest.StartWAL == "" {
		return
	}
	// Run as root in the container: the archive belongs to postgres.
	if _, err := executor.Output(ctx, x, fmt.Sprintf("docker exec %s pg_archivecleanup -x .gz /pitr/wal %s", q(db.UUID), q(oldest.StartWAL))); err != nil {
		slog.Warn("pitr archive cleanup", "database", db.Name, "err", err)
	}
	// pg_archivecleanup leaves the backup history files (*.backup.gz).
	if out, err := executor.Output(ctx, x, "docker exec "+q(db.UUID)+" ls /pitr/wal"); err == nil {
		var stale []string
		for _, n := range strings.Fields(out) {
			if strings.HasSuffix(n, ".backup.gz") && len(n) > 24 && n[8:24] < oldest.StartWAL[8:24] {
				stale = append(stale, q("/pitr/wal/"+n))
			}
		}
		if len(stale) > 0 {
			_, _ = executor.Output(ctx, x, "docker exec "+q(db.UUID)+" rm -f "+strings.Join(stale, " "))
		}
	}
	// Failed base backups only matter for a while.
	_, _ = e.db.ExecContext(ctx, "DELETE FROM pitr_backups WHERE database_id = ? AND status = 'failed' AND id < ?", db.ID, oldest.ID)
}

// syncPITR mirrors the archive and the base backups to the bucket
// (portdock/<db>/pitr/), removing there what retention removed here.
func (e *Engine) syncPITR(ctx context.Context, x executor.Executor, db store.Database) error {
	c, err := e.s3Config(ctx)
	if err != nil {
		return err
	}
	if _, err := e.awsCLI(ctx, x, c, e.pitrDir(db), true,
		"s3 sync /b "+q(c.url(db.UUID, "pitr/"))+" --delete --exclude '*.tmp' --exclude '*.part'"); err != nil {
		return err
	}
	_, err = e.db.ExecContext(ctx, "UPDATE databases SET pitr_synced_at = ? WHERE id = ?", store.Now(), db.ID)
	return err
}

// PITRStatus is the state of point-in-time recovery of a database.
type PITRStatus struct {
	Enabled bool `json:"enabled"`
	// Window is the range of times a restore can reach ("" when there is none yet).
	From string `json:"from"`
	To   string `json:"to"`
	// The archiver, as PostgreSQL reports it.
	ArchivedCount  int64              `json:"archived_count"`
	LastArchived   string             `json:"last_archived_wal"`
	LastArchivedAt string             `json:"last_archived_at"`
	FailedCount    int64              `json:"failed_count"`
	LastFailedAt   string             `json:"last_failed_at"`
	Backups        []store.PITRBackup `json:"backups"`
	Restore        *store.PITRRestore `json:"restore"`
	Directory      string             `json:"directory"`
}

// archiver reads pg_stat_archiver.
func (e *Engine) archiver(ctx context.Context, x executor.Executor, db store.Database) (count int64, wal, at string, failed int64, failedAt string, err error) {
	out, err := executor.Output(ctx, x, "docker exec "+q(db.UUID)+" sh -c "+q(e.psql(db,
		`SELECT archived_count || '|' || coalesce(last_archived_wal, '') || '|' || coalesce(to_char(last_archived_time AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), '') || '|' || failed_count || '|' || coalesce(to_char(last_failed_time AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), '') FROM pg_stat_archiver`)))
	if err != nil {
		return 0, "", "", 0, "", err
	}
	f := strings.Split(strings.TrimSpace(out), "|")
	if len(f) != 5 {
		return 0, "", "", 0, "", fmt.Errorf("unexpected archiver status %q", out)
	}
	count, _ = strconv.ParseInt(f[0], 10, 64)
	failed, _ = strconv.ParseInt(f[3], 10, 64)
	return count, f[1], f[2], failed, f[4], nil
}

func (e *Engine) PITRStatusOf(ctx context.Context, db store.Database) (PITRStatus, error) {
	st := PITRStatus{Enabled: db.PITR, Directory: e.pitrDir(db), Backups: []store.PITRBackup{}}
	bs, err := store.Select[store.PITRBackup](ctx, e.db, "SELECT * FROM pitr_backups WHERE database_id = ? ORDER BY id DESC LIMIT 50", db.ID)
	if err != nil {
		return st, err
	}
	if bs != nil {
		st.Backups = bs
	}
	if r, err := store.Get[store.PITRRestore](ctx, e.db, "SELECT * FROM pitr_restores WHERE database_id = ? ORDER BY id DESC LIMIT 1", db.ID); err == nil {
		st.Restore = &r
	}
	for i := len(bs) - 1; i >= 0; i-- {
		if bs[i].Status == "success" {
			st.From = bs[i].FinishedAt
			break
		}
	}
	st.To = db.PITRArchivedAt
	if db.PITR && db.Status == "running" {
		if x, _, err := e.pool.ForID(ctx, db.ServerID); err == nil {
			if c, wal, at, failed, failedAt, err := e.archiver(ctx, x, db); err == nil {
				st.ArchivedCount, st.LastArchived, st.LastArchivedAt, st.FailedCount, st.LastFailedAt = c, wal, at, failed, failedAt
				if at != "" {
					st.To = at
				}
			}
		}
	}
	if st.From == "" {
		st.To = ""
	}
	return st, nil
}

// PITRTick runs every minute: it records the archiver's state (and notifies
// when archiving starts failing), and copies the archive to S3 every 5 minutes.
func (e *Engine) PITRTick(ctx context.Context) {
	dbs, err := store.Select[store.Database](ctx, e.db, "SELECT * FROM databases WHERE pitr = 1 AND status = 'running' AND engine = 'postgres'")
	if err != nil {
		return
	}
	for _, db := range dbs {
		if _, busy := pitrBusy.Load(db.ID); busy {
			continue
		}
		x, _, err := e.pool.ForID(ctx, db.ServerID)
		if err != nil {
			continue
		}
		_, _, at, failed, failedAt, err := e.archiver(ctx, x, db)
		msg := ""
		switch {
		case err != nil:
			msg = "the archiver status could not be read: " + err.Error()
		case failed > 0 && failedAt > at:
			msg = fmt.Sprintf("WAL archiving is failing (%d failed attempts, last at %s)", failed, failedAt)
		}
		if msg != "" && db.PITRArchiveError == "" {
			e.notify.Send(notify.Event{Name: "backup.failed", Title: "WAL archiving failed: " + db.Name, Message: msg})
		}
		_, _ = e.db.ExecContext(ctx, "UPDATE databases SET pitr_archived_at = CASE WHEN ? != '' THEN ? ELSE pitr_archived_at END, pitr_archive_error = ? WHERE id = ?", at, at, msg, db.ID)
		if db.PITRS3 && time.Now().Minute()%5 == 0 {
			if err := e.syncPITR(ctx, x, db); err != nil {
				slog.Warn("pitr s3 sync", "database", db.Name, "err", err)
			}
		}
	}
}

// EnqueuePITRRestore restores db as it was at target.
func (e *Engine) EnqueuePITRRestore(ctx context.Context, db store.Database, target time.Time, by int64) (store.PITRRestore, error) {
	r := store.PITRRestore{DatabaseID: db.ID, TargetTime: target.UTC().Format(time.RFC3339), Status: "pending", CreatedBy: by, StartedAt: store.Now()}
	if !db.PITR {
		return r, errors.New("point-in-time recovery is off")
	}
	if db.Status != "running" {
		return r, errors.New("start the database first")
	}
	if target.After(time.Now()) {
		return r, errors.New("the time is in the future")
	}
	base, err := e.baseFor(ctx, db, target)
	if err != nil {
		return r, err
	}
	if _, loaded := pitrBusy.LoadOrStore(db.ID, true); loaded {
		return r, errors.New("a point-in-time restore of this database is already running")
	}
	r.BaseFile = base.File
	if _, err := store.Insert(ctx, e.db, "pitr_restores", &r); err != nil {
		pitrBusy.Delete(db.ID)
		return r, err
	}
	e.hub.Event("database", db.ID, db.Status)
	go func() {
		defer pitrBusy.Delete(db.ID)
		bg := context.WithoutCancel(ctx)
		release := jobs.acquire(db)
		defer release()
		if err := e.pitrRestore(bg, db, &r, base); err != nil {
			e.notify.Send(notify.Event{Name: "backup.restore_failed", Title: "Point-in-time restore failed: " + db.Name, Message: err.Error()})
		}
	}()
	return r, nil
}

// pgTime writes an RFC 3339 time for recovery_target_time, which takes a
// numeric offset but no zone abbreviation such as Z.
func pgTime(rfc string) string {
	t, _ := time.Parse(time.RFC3339, rfc)
	return t.UTC().Format("2006-01-02 15:04:05") + "+00"
}

// timelineAt is the timeline the database was on at target: the one started
// by the last restore before it, or the base backup's own ("current") when
// there was none. Without it, a restore to a time before an earlier restore
// would follow the newer timeline and miss the changes made in between.
func (e *Engine) timelineAt(ctx context.Context, db store.Database, target string) string {
	r, err := store.Get[store.PITRRestore](ctx, e.db,
		"SELECT * FROM pitr_restores WHERE database_id = ? AND status = 'success' AND timeline > 0 AND finished_at <= ? ORDER BY id DESC LIMIT 1", db.ID, target)
	if err != nil {
		return "current"
	}
	return strconv.Itoa(r.Timeline)
}

// baseFor returns the newest base backup that finished before target.
func (e *Engine) baseFor(ctx context.Context, db store.Database, target time.Time) (store.PITRBackup, error) {
	bs, err := store.Select[store.PITRBackup](ctx, e.db, "SELECT * FROM pitr_backups WHERE database_id = ? AND status = 'success' ORDER BY id DESC", db.ID)
	if err != nil {
		return store.PITRBackup{}, err
	}
	for _, b := range bs {
		if t, err := time.Parse(time.RFC3339, b.FinishedAt); err == nil && !t.After(target) {
			return b, nil
		}
	}
	if len(bs) == 0 {
		return store.PITRBackup{}, errors.New("there is no base backup yet")
	}
	return store.PITRBackup{}, fmt.Errorf("the earliest time that can be restored is %s", bs[len(bs)-1].FinishedAt)
}

func (e *Engine) pitrRestore(ctx context.Context, db store.Database, r *store.PITRRestore, base store.PITRBackup) error {
	set := func(status, msg string) {
		r.Status, r.Message = status, msg
		if status == "success" || status == "failed" {
			r.FinishedAt = store.Now()
		}
		_ = store.Update(context.WithoutCancel(ctx), e.db, "pitr_restores", r)
		e.hub.Event("database", db.ID, db.Status)
	}
	fail := func(err error) error {
		set("failed", err.Error())
		return err
	}
	x, _, err := e.pool.ForID(ctx, db.ServerID)
	if err != nil {
		return fail(err)
	}
	// 1. The latest changes reach the archive.
	set("running", "Archiving the latest changes…")
	if out, err := executor.Output(ctx, x, "docker exec "+q(db.UUID)+" sh -c "+q(e.psql(db, "SELECT pg_walfile_name(pg_switch_wal())"))); err == nil {
		wal := strings.TrimSpace(out)
		deadline := time.Now().Add(3 * time.Minute)
		for time.Now().Before(deadline) {
			if _, last, _, _, _, err := e.archiver(ctx, x, db); err == nil && last >= wal {
				break
			}
			time.Sleep(2 * time.Second)
		}
	}
	envOut, err := executor.Output(ctx, x, "docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "+q(db.UUID))
	if err != nil {
		return fail(err)
	}
	pgdata := ""
	for _, l := range strings.Split(envOut, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "PGDATA="); ok {
			pgdata = v
		}
	}
	if pgdata == "" || !strings.HasPrefix(pgdata, "/var/lib/postgresql") {
		return fail(fmt.Errorf("unexpected data directory %q", pgdata))
	}
	replaced := path.Join(path.Dir(pgdata), "replaced-"+time.Now().UTC().Format("20060102-150405"))
	helper := func(script string) error {
		_, err := executor.Output(ctx, x, fmt.Sprintf("docker run --rm -v %s:/var/lib/postgresql -v %s:/pitr -e D=%s -e R=%s --entrypoint sh %s -c %s",
			q(db.VolumeName()), q(e.pitrDir(db)), q(pgdata), q(replaced), q(db.Image), q(script)))
		return err
	}

	// 2. Stop the database and unpack the base backup, keeping the current data.
	set("running", "Stopping the database and unpacking the base backup…")
	if _, err := executor.Output(ctx, x, "docker stop -t 60 "+q(db.UUID)); err != nil {
		return fail(err)
	}
	conf := fmt.Sprintf("restore_command = 'gunzip -c /pitr/wal/%%f.gz > %%p'\nrecovery_target_time = '%s'\nrecovery_target_timeline = '%s'\nrecovery_target_action = 'promote'\n",
		pgTime(r.TargetTime), e.timelineAt(ctx, db, r.TargetTime))
	err = helper(`set -e; P=$(dirname "$D"); for old in "$P"/replaced-*; do [ -e "$old" ] && rm -rf "$old"; done; ` +
		`mv "$D" "$R"; mkdir -p "$D"; tar -xzf /pitr/base/` + q(path.Base(base.File)) + ` -C "$D"; ` +
		`touch "$D/recovery.signal"; printf %s ` + q(conf) + ` >> "$D/postgresql.auto.conf"; chown -R postgres:postgres "$D"; chmod 700 "$D"`)
	rollback := func(cause error) error {
		_, _ = executor.Output(context.WithoutCancel(ctx), x, "docker stop -t 30 "+q(db.UUID))
		if rerr := helper(`set -e; if [ -d "$R" ]; then rm -rf "$D"; mv "$R" "$D"; fi`); rerr != nil {
			return fail(fmt.Errorf("%v; putting the previous data back failed too: %v (it is in %s)", cause, rerr, replaced))
		}
		_, _ = executor.Output(context.WithoutCancel(ctx), x, "docker start "+q(db.UUID))
		return fail(fmt.Errorf("%v; the previous data was put back", cause))
	}
	if err != nil {
		return rollback(fmt.Errorf("unpack the base backup: %w", err))
	}
	r.ReplacedDir = replaced

	// 3. Replay the archive up to the target, then promote.
	set("running", "Replaying the WAL archive up to "+r.TargetTime+"…")
	before, _ := executor.Output(ctx, x, "docker inspect -f '{{.RestartCount}}' "+q(db.UUID))
	if _, err := executor.Output(ctx, x, "docker start "+q(db.UUID)); err != nil {
		return rollback(err)
	}
	deadline := time.Now().Add(pitrRecoveryLimit)
	for {
		if time.Now().After(deadline) {
			return rollback(errors.New("the recovery did not finish in 2 hours"))
		}
		time.Sleep(3 * time.Second)
		state, _ := executor.Output(ctx, x, "docker inspect -f '{{.State.Status}} {{.RestartCount}}' "+q(db.UUID))
		f := strings.Fields(state)
		if len(f) == 2 && (f[0] == "exited" || f[0] == "restarting" || f[1] != strings.TrimSpace(before)) {
			logs, _ := executor.Output(ctx, x, "docker logs --tail 40 "+q(db.UUID)+" 2>&1 | grep -E 'FATAL|PANIC|ERROR' | tail -3")
			if strings.TrimSpace(logs) == "" {
				logs, _ = executor.Output(ctx, x, "docker logs --tail 4 "+q(db.UUID)+" 2>&1")
			}
			return rollback(fmt.Errorf("PostgreSQL stopped during the recovery: %s", strings.TrimSpace(logs)))
		}
		out, err := executor.Output(ctx, x, "docker exec "+q(db.UUID)+" sh -c "+q(e.psql(db, "SELECT pg_is_in_recovery()")))
		if err == nil && strings.TrimSpace(out) == "f" {
			break
		}
	}
	// 4. Back to normal: the recovery settings go away.
	for _, k := range []string{"restore_command", "recovery_target_time", "recovery_target_timeline", "recovery_target_action"} {
		_, _ = executor.Output(ctx, x, "docker exec "+q(db.UUID)+" sh -c "+q(e.psql(db, "ALTER SYSTEM RESET "+k)))
	}
	_, _ = executor.Output(ctx, x, "docker exec "+q(db.UUID)+" sh -c "+q(e.psql(db, "SELECT pg_reload_conf()")))
	if out, err := executor.Output(ctx, x, "docker exec "+q(db.UUID)+" sh -c "+q(e.psql(db, "SELECT timeline_id FROM pg_control_checkpoint()"))); err == nil {
		r.Timeline, _ = strconv.Atoi(strings.TrimSpace(out))
	}
	e.setDBStatus(ctx, db.ID, "running")
	set("success", fmt.Sprintf("Restored to %s from the base backup of %s. The data it replaced is kept in %s until the next restore.", r.TargetTime, base.FinishedAt, replaced))
	return nil
}
