package deploy

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/proxy"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
	"github.com/portdock/portdock/internal/templates"
)

// Streaming replicas of PostgreSQL. A replica is a hot standby on any server:
// it is cloned with pg_basebackup through a replication slot (the primary
// keeps the WAL it has not received yet) and answers read-only queries.
// Promoting one is a failover: the old primary is stopped (fencing), the
// replica becomes writable, and the database moves onto its server and data
// volume under its own name, so applications keep their connection URL.

const replicationRole = "portdock_replica"

// LabelReplica marks the container of a replica (value: the replica's UUID).
const LabelReplica = "portdock.replica"

// lagWarning is the replay lag above which a replica counts as lagging.
const lagWarning = 64 << 20

// replicaBusy marks databases with a replica being created or promoted.
var replicaBusy sync.Map

// replicationPassword returns (creating it once) the password of the replication role.
func (e *Engine) replicationPassword(ctx context.Context, db *store.Database) (string, error) {
	if db.ReplicationPassword != "" {
		return e.box.Decrypt(db.ReplicationPassword)
	}
	pw := secrets.RandomToken(24)
	db.ReplicationPassword = e.box.Encrypt(pw)
	if _, err := e.db.ExecContext(ctx, "UPDATE databases SET replication_password = ? WHERE id = ?", db.ReplicationPassword, db.ID); err != nil {
		return "", err
	}
	return pw, nil
}

// prepareReplication creates the replication role on the primary and lets it
// connect for replication from the network.
func (e *Engine) prepareReplication(ctx context.Context, x executor.Executor, db store.Database, pw string) error {
	sql := fmt.Sprintf(`DO $$ BEGIN IF EXISTS (SELECT FROM pg_roles WHERE rolname = '%[1]s') THEN ALTER ROLE %[1]s WITH REPLICATION LOGIN PASSWORD '%[2]s'; `+
		`ELSE CREATE ROLE %[1]s WITH REPLICATION LOGIN PASSWORD '%[2]s'; END IF; END $$`, replicationRole, pw)
	if _, err := executor.Output(ctx, x, "docker exec "+q(db.UUID)+" sh -c "+q(e.psql(db, sql))); err != nil {
		return fmt.Errorf("create the replication role: %w", err)
	}
	hba := `grep -q "^host replication ` + replicationRole + ` " "$PGDATA/pg_hba.conf" || echo "host replication ` + replicationRole + ` all scram-sha-256" >> "$PGDATA/pg_hba.conf"`
	if _, err := executor.Output(ctx, x, "docker exec "+q(db.UUID)+" sh -c "+q(hba)); err != nil {
		return fmt.Errorf("allow replication connections: %w", err)
	}
	_, err := executor.Output(ctx, x, "docker exec "+q(db.UUID)+" sh -c "+q(e.psql(db, "SELECT pg_reload_conf()")))
	return err
}

// primaryAddress is how a replica on server replicaServer reaches the primary.
func (e *Engine) primaryAddress(ctx context.Context, db store.Database, replicaServer int64) (string, int, error) {
	if replicaServer == db.ServerID {
		return db.UUID, templates.Engines[db.Engine].Port, nil // the Docker network
	}
	// The private network, when both servers are in it.
	if db.OverlayPort > 0 {
		primary, err1 := store.ByID[store.Server](ctx, e.db, "servers", db.ServerID)
		replica, err2 := store.ByID[store.Server](ctx, e.db, "servers", replicaServer)
		if err1 == nil && err2 == nil && e.privateIP(ctx, primary) != "" && e.privateIP(ctx, replica) != "" {
			return primary.OverlayIP, db.OverlayPort, nil
		}
	}
	if db.PublicPort == 0 {
		return "", 0, errors.New("a replica on another server reaches the primary on the private network (a private port, both servers in the network) or through its public port: set one on the primary first")
	}
	srv, err := store.ByID[store.Server](ctx, e.db, "servers", db.ServerID)
	if err != nil {
		return "", 0, err
	}
	host := DatabasePublicHost(db, srv, "")
	if host == "localhost" {
		return "", 0, errors.New("the primary's server has no address reachable from other servers")
	}
	return host, db.PublicPort, nil
}

// CreateReplica clones db into a new replica on serverID.
func (e *Engine) CreateReplica(ctx context.Context, db store.Database, serverID int64, publicPort int) (store.DBReplica, error) {
	r := store.DBReplica{DatabaseID: db.ID, UUID: secrets.RandomToken(8), ServerID: serverID, PublicPort: publicPort, Status: "creating", CreatedAt: store.Now()}
	if db.Engine != "postgres" {
		return r, errors.New("replicas are available for PostgreSQL")
	}
	if db.Status != "running" {
		return r, errors.New("start the database first")
	}
	if _, err := store.ByID[store.Server](ctx, e.db, "servers", serverID); err != nil {
		return r, errors.New("unknown server")
	}
	if _, _, err := e.primaryAddress(ctx, db, serverID); err != nil {
		return r, err
	}
	if _, busy := replicaBusy.LoadOrStore(db.ID, true); busy {
		return r, errors.New("a replica of this database is being created or promoted")
	}
	if _, err := store.Insert(ctx, e.db, "db_replicas", &r); err != nil {
		replicaBusy.Delete(db.ID)
		return r, err
	}
	e.hub.Event("database", db.ID, db.Status)
	go func() {
		defer replicaBusy.Delete(db.ID)
		bg := context.WithoutCancel(ctx)
		if err := e.buildReplica(bg, db, &r); err != nil {
			r.Status, r.Message = "failed", err.Error()
			_ = store.Update(bg, e.db, "db_replicas", &r)
			e.hub.Event("database", db.ID, db.Status)
			e.notify.Send(notify.Event{Name: "database.failed", Title: "Replica failed: " + db.Name, Message: err.Error()})
		}
	}()
	return r, nil
}

func (e *Engine) buildReplica(ctx context.Context, db store.Database, r *store.DBReplica) error {
	ctx, cancel := context.WithTimeout(ctx, backupTimeout)
	defer cancel()
	progress := func(msg string) {
		r.Message = msg
		_ = store.Update(ctx, e.db, "db_replicas", r)
		e.hub.Event("database", db.ID, db.Status)
	}
	px, _, err := e.pool.ForID(ctx, db.ServerID)
	if err != nil {
		return err
	}
	pw, err := e.replicationPassword(ctx, &db)
	if err != nil {
		return err
	}
	progress("Preparing the primary…")
	if err := e.prepareReplication(ctx, px, db, pw); err != nil {
		return err
	}
	host, port, err := e.primaryAddress(ctx, db, r.ServerID)
	if err != nil {
		return err
	}
	rx, _, err := e.pool.ForID(ctx, r.ServerID)
	if err != nil {
		return err
	}
	if err := docker.EnsureNetwork(ctx, rx, e.cfg.ProxyNetwork); err != nil {
		return err
	}
	progress("Copying the data (pg_basebackup)…")
	conninfo := fmt.Sprintf("host=%s port=%d user=%s password=%s application_name=%s", host, port, replicationRole, pw, r.UUID)
	conf := fmt.Sprintf("primary_conninfo = '%s'\nprimary_slot_name = '%s'\nhot_standby = on\n", conninfo, r.Slot())
	script := `set -e; D="$PGDATA"; mkdir -p "$D"; ` +
		`pg_basebackup -h "$H" -p "$P" -U ` + replicationRole + ` -D "$D" -X stream -C -S "$S" -c fast; ` +
		`touch "$D/standby.signal"; printf %s "$C" >> "$D/postgresql.auto.conf"; ` +
		`chown -R postgres:postgres /var/lib/postgresql; chmod 700 "$D"`
	_, err = executor.Output(ctx, rx, fmt.Sprintf("docker run --rm --network %s -v %s:/var/lib/postgresql -e PGPASSWORD=%s -e H=%s -e P=%d -e S=%s -e C=%s --entrypoint sh %s -c %s",
		q(e.cfg.ProxyNetwork), q("sy-"+r.UUID), q(pw), q(host), port, q(r.Slot()), q(conf), q(db.Image), q(script)))
	if err != nil {
		_, _ = executor.Output(context.WithoutCancel(ctx), rx, "docker volume rm -f "+q("sy-"+r.UUID))
		e.dropSlot(context.WithoutCancel(ctx), db, r.Slot())
		return fmt.Errorf("clone the primary: %w", err)
	}
	progress("Starting the replica…")
	if err := e.startReplica(ctx, rx, db, *r); err != nil {
		return err
	}
	r.Status, r.Message = "streaming", ""
	return store.Update(ctx, e.db, "db_replicas", r)
}

// startReplica (re)creates the replica's container.
func (e *Engine) startReplica(ctx context.Context, x executor.Executor, db store.Database, r store.DBReplica) error {
	eng := templates.Engines[db.Engine]
	pass := e.dbPassword(db)
	var b strings.Builder
	fmt.Fprintf(&b, "(docker rm -f %s >/dev/null 2>&1 || true) && docker run -d --name %s --restart unless-stopped --network %s --network-alias %s",
		q(r.UUID), q(r.UUID), q(e.cfg.ProxyNetwork), q(proxy.SafeName(db.Name)+"-replica"))
	fmt.Fprintf(&b, " -v %s:%s", q("sy-"+r.UUID), eng.DataPath)
	for k, v := range eng.Env(db.Username, pass, db.DatabaseName) {
		b.WriteString(" -e " + q(k+"="+v))
	}
	fmt.Fprintf(&b, " --health-cmd %s --health-interval 10s --health-timeout 5s --health-retries 10", q(eng.HealthCmd(db.Username, pass, db.DatabaseName)))
	fmt.Fprintf(&b, " --label %s=true --label %s=database --label %s=%s --label %s=%s --log-opt max-size=10m --log-opt max-file=3",
		docker.LabelManaged, docker.LabelKind, docker.LabelResource, q(db.UUID), LabelReplica, q(r.UUID))
	if db.CPULimit != "" {
		b.WriteString(" --cpus " + q(db.CPULimit))
	}
	if db.MemoryLimit != "" {
		b.WriteString(" --memory " + q(db.MemoryLimit))
	}
	if r.PublicPort > 0 {
		fmt.Fprintf(&b, " -p %d:%d", r.PublicPort, eng.Port)
	}
	b.WriteString(" " + q(db.Image))
	args, err := docker.CommandArgs(db.CustomArgs)
	if err != nil {
		return err
	}
	b.WriteString(args)
	_, err = executor.Output(ctx, x, b.String())
	return err
}

// dropSlot removes a replication slot from the primary, so it stops keeping WAL.
func (e *Engine) dropSlot(ctx context.Context, db store.Database, slot string) {
	x, _, err := e.pool.ForID(ctx, db.ServerID)
	if err != nil {
		return
	}
	_, _ = executor.Output(ctx, x, "docker exec "+q(db.UUID)+" sh -c "+q(e.psql(db,
		"SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE slot_name = '"+slot+"' AND NOT active")))
}

// DeleteReplica removes a replica, its data and its replication slot.
func (e *Engine) DeleteReplica(ctx context.Context, db store.Database, r store.DBReplica) error {
	if r.Status == "promoting" {
		return errors.New("the replica is being promoted")
	}
	if x, _, err := e.pool.ForID(ctx, r.ServerID); err == nil {
		_, _ = executor.Output(ctx, x, "docker rm -f "+q(r.UUID)+" >/dev/null 2>&1; docker volume rm -f "+q("sy-"+r.UUID)+" >/dev/null 2>&1; true")
	}
	e.dropSlot(ctx, db, r.Slot())
	return store.Delete(ctx, e.db, "db_replicas", r.ID)
}

// DeleteReplicas removes every replica of a database being deleted.
func (e *Engine) DeleteReplicas(ctx context.Context, db store.Database) {
	rs, _ := store.Select[store.DBReplica](ctx, e.db, "SELECT * FROM db_replicas WHERE database_id = ?", db.ID)
	for _, r := range rs {
		if x, _, err := e.pool.ForID(ctx, r.ServerID); err == nil {
			_, _ = executor.Output(ctx, x, "docker rm -f "+q(r.UUID)+" >/dev/null 2>&1; docker volume rm -f "+q("sy-"+r.UUID)+" >/dev/null 2>&1; true")
		}
	}
}

// ReplicaURLs are the read-only connection URLs of a replica.
func (e *Engine) ReplicaURLs(db store.Database, r store.DBReplica, srv store.Server, fallback string) (internal, public string) {
	eng := templates.Engines[db.Engine]
	pass := e.dbPassword(db)
	internal = eng.URL(db.Username, pass, r.UUID, eng.Port, db.DatabaseName)
	if r.PublicPort > 0 {
		host := DatabasePublicHost(store.Database{}, srv, fallback)
		public = eng.URL(db.Username, pass, host, r.PublicPort, db.DatabaseName)
	}
	return internal, public
}

// checkReplicas measures the replication of db's replicas on the primary
// (pg_stat_replication) and notifies when one stops streaming.
func (e *Engine) checkReplicas(ctx context.Context, db store.Database) {
	rs, err := store.Select[store.DBReplica](ctx, e.db, "SELECT * FROM db_replicas WHERE database_id = ? AND status IN ('streaming', 'lagging', 'disconnected')", db.ID)
	if err != nil || len(rs) == 0 {
		return
	}
	x, _, err := e.pool.ForID(ctx, db.ServerID)
	if err != nil {
		return
	}
	out, err := executor.Output(ctx, x, "docker exec "+q(db.UUID)+" sh -c "+q(e.psql(db,
		`SELECT application_name || '|' || state || '|' || coalesce(pg_wal_lsn_diff(pg_current_wal_lsn(), replay_lsn), 0)::bigint || '|' || coalesce(extract(epoch FROM replay_lag), 0) FROM pg_stat_replication`)))
	if err != nil {
		return
	}
	seen := map[string][]string{}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if f := strings.Split(l, "|"); len(f) == 4 {
			seen[f[0]] = f
		}
	}
	for _, r := range rs {
		prev := r.Status
		if f, ok := seen[r.UUID]; ok && f[1] == "streaming" {
			r.LagBytes, _ = strconv.ParseInt(f[2], 10, 64)
			r.LagSeconds, _ = strconv.ParseFloat(f[3], 64)
			r.Status, r.Message = "streaming", ""
			if r.LagBytes > lagWarning {
				r.Status = "lagging"
			}
		} else {
			r.Status, r.Message = "disconnected", "the replica is not connected to the primary"
		}
		r.CheckedAt = store.Now()
		_ = store.Update(ctx, e.db, "db_replicas", &r)
		if r.Status == "disconnected" && prev != "disconnected" {
			e.notify.Send(notify.Event{Name: "database.failed", Title: "Replica disconnected: " + db.Name, Message: "Replica " + r.UUID + " stopped streaming from the primary."})
		}
	}
}

// PromoteReplica makes r the database's primary. The old primary is
// stopped first; force promotes even when its server cannot be reached
// (it must then stay down: two primaries would accept different writes).
func (e *Engine) PromoteReplica(ctx context.Context, db store.Database, r store.DBReplica, force bool, by string) error {
	if r.Status == "creating" || r.Status == "failed" || r.Status == "promoting" {
		return fmt.Errorf("the replica cannot be promoted while it is %s", r.Status)
	}
	if _, busy := replicaBusy.LoadOrStore(db.ID, true); busy {
		return errors.New("a replica of this database is being created or promoted")
	}
	defer replicaBusy.Delete(db.ID)
	rx, _, err := e.pool.ForID(ctx, r.ServerID)
	if err != nil {
		return fmt.Errorf("the replica's server cannot be reached: %w", err)
	}
	set := func(status, msg string) {
		r.Status, r.Message = status, msg
		_ = store.Update(context.WithoutCancel(ctx), e.db, "db_replicas", &r)
		e.hub.Event("database", db.ID, db.Status)
	}
	// 1. Fencing: the old primary stops before the replica accepts writes. Its
	// container goes (the data stays in its volume); a primary that could not
	// be reached is stopped when its server answers again (fenceFormerPrimaries).
	if px, _, err := e.pool.ForID(ctx, db.ServerID); err == nil {
		if _, err := executor.Output(ctx, px, fmt.Sprintf("docker update --restart no %s >/dev/null && docker stop -t 30 %s >/dev/null && docker rm %s >/dev/null",
			q(db.UUID), q(db.UUID), q(db.UUID))); err != nil && !force {
			return fmt.Errorf("the old primary could not be stopped (%v); promote with force only if it is down for good", err)
		}
	} else if !force {
		return fmt.Errorf("the primary's server cannot be reached (%v); promote with force only if it is down for good", err)
	}
	set("promoting", "Promoting the replica…")
	// 2. The replica leaves recovery.
	if _, err := executor.Output(ctx, rx, "docker exec "+q(r.UUID)+" sh -c "+q(e.psql(db, "SELECT pg_promote(true, 120)"))); err != nil {
		set("failed", "promotion failed: "+err.Error())
		return fmt.Errorf("promote: %w", err)
	}
	for _, k := range []string{"primary_conninfo", "primary_slot_name"} {
		_, _ = executor.Output(ctx, rx, "docker exec "+q(r.UUID)+" sh -c "+q(e.psql(db, "ALTER SYSTEM RESET "+k)))
	}
	// 3. The database moves onto the replica's server and volume, under its own name.
	_, _ = executor.Output(ctx, rx, "docker rm -f "+q(r.UUID))
	oldVolume, oldServer := db.VolumeName(), db.ServerID
	db.ServerID, db.DataVolume = r.ServerID, "sy-"+r.UUID
	if r.PublicPort > 0 {
		db.PublicPort = r.PublicPort
	}
	if err := store.Update(ctx, e.db, "databases", &db); err != nil {
		return err
	}
	if err := e.StartDatabase(ctx, db, true); err != nil {
		set("failed", "the promoted data could not be started: "+err.Error())
		return err
	}
	_ = store.Delete(ctx, e.db, "db_replicas", r.ID)
	// Other replicas followed the old primary: they must be created again.
	_, _ = e.db.ExecContext(ctx, "UPDATE db_replicas SET status = 'failed', message = ? WHERE database_id = ?",
		"the primary changed: remove this replica and add a new one", db.ID)
	e.hub.Event("database", db.ID, "running")
	srv, _ := store.ByID[store.Server](ctx, e.db, "servers", oldServer)
	e.notify.Send(notify.Event{Name: "database.failover", Title: "Failover: " + db.Name,
		Message: fmt.Sprintf("Replica %s was promoted by %s. The previous primary's data stays in volume %s on %s.", r.UUID, by, oldVolume, srv.Name)})
	if db.PITR {
		if oldServer != db.ServerID {
			// The archive and base backups stayed on the old server: a new
			// recovery window starts with the next base backup.
			_, _ = e.db.ExecContext(ctx, "DELETE FROM pitr_backups WHERE database_id = ?", db.ID)
			_, _ = e.db.ExecContext(ctx, "UPDATE databases SET pitr_archived_at = '' WHERE id = ?", db.ID)
		}
		go func() { _, _ = e.EnqueueBaseBackup(context.WithoutCancel(ctx), db) }()
	}
	return nil
}

// fenceFormerPrimaries stops the primary containers of databases that now
// run on another server: a failover forced while this server was unreachable
// must not leave two primaries once it answers again.
func (e *Engine) fenceFormerPrimaries(ctx context.Context, x executor.Executor, srv store.Server, cs []docker.Container) {
	for _, c := range cs {
		uuid := c.Labels[docker.LabelResource]
		if c.Labels[docker.LabelKind] != "database" || c.Labels[LabelReplica] != "" || c.State != "running" || c.Name != uuid {
			continue
		}
		var name string
		var serverID int64
		if err := e.db.QueryRowContext(ctx, "SELECT name, server_id FROM databases WHERE uuid = ?", uuid).Scan(&name, &serverID); err != nil || serverID == srv.ID {
			continue
		}
		if _, err := executor.Output(ctx, x, "docker update --restart no "+q(c.Name)+" >/dev/null && docker stop -t 30 "+q(c.Name)+" >/dev/null && docker rm "+q(c.Name)+" >/dev/null"); err == nil {
			e.notify.Send(notify.Event{Name: "database.failover", Title: "Former primary stopped: " + name,
				Message: "The previous primary of " + name + " came back on " + srv.Name + " after a failover and was stopped. Its data stays in its volume."})
		}
	}
}

// unreachableSince remembers when a primary's server stopped answering.
var unreachableSince sync.Map

// autoFailover promotes the most caught-up replica on another, reachable
// server once the primary's server has been unreachable long enough.
func (e *Engine) autoFailover(ctx context.Context, db store.Database) {
	srv, err := store.ByID[store.Server](ctx, e.db, "servers", db.ServerID)
	if err != nil {
		return
	}
	if srv.Status != "unreachable" {
		unreachableSince.Delete(db.ID)
		return
	}
	first, _ := unreachableSince.LoadOrStore(db.ID, time.Now())
	if time.Since(first.(time.Time)) < time.Duration(max(db.FailoverAfter, 30))*time.Second {
		return
	}
	rs, _ := store.Select[store.DBReplica](ctx, e.db, `SELECT x.* FROM db_replicas x JOIN servers s ON s.id = x.server_id
		WHERE x.database_id = ? AND x.server_id != ? AND s.status = 'reachable' AND x.status IN ('streaming', 'lagging', 'disconnected')
		ORDER BY x.lag_bytes ASC, x.id ASC LIMIT 1`, db.ID, db.ServerID)
	if len(rs) == 0 {
		return
	}
	unreachableSince.Delete(db.ID)
	if err := e.PromoteReplica(ctx, db, rs[0], true, "automatic failover"); err != nil {
		e.notify.Send(notify.Event{Name: "database.failed", Title: "Automatic failover failed: " + db.Name, Message: err.Error()})
	}
}

// ReplicationTick checks the replicas and runs automatic failovers.
func (e *Engine) ReplicationTick(ctx context.Context) {
	dbs, err := store.Select[store.Database](ctx, e.db, "SELECT d.* FROM databases d WHERE d.engine = 'postgres' AND EXISTS (SELECT 1 FROM db_replicas x WHERE x.database_id = d.id)")
	if err != nil {
		return
	}
	for _, db := range dbs {
		if _, busy := replicaBusy.Load(db.ID); busy {
			continue
		}
		if db.FailoverAuto {
			e.autoFailover(ctx, db)
		}
		if db.Status == "running" {
			e.checkReplicas(ctx, db)
		}
	}
}
