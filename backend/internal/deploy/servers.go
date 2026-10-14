package deploy

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/proxy"
	"github.com/portdock/portdock/internal/store"
)

func ServerTopic(id int64) string { return fmt.Sprintf("server:%d", id) }

// NixpacksVersion is used when the GitHub API can't be reached (rate limits).
const NixpacksVersion = "v1.41.0"

// installNixpacks downloads the static musl binary directly from GitHub releases.
// The official install.sh is avoided because it breaks with BusyBox tar (Alpine):
// it passes an empty argument that BusyBox treats as an archive member name.
var installNixpacks = `set -e
if command -v nixpacks >/dev/null 2>&1; then nixpacks --version; exit 0; fi
case "$(uname -m)" in
  x86_64|amd64) arch=x86_64 ;;
  aarch64|arm64) arch=aarch64 ;;
  *) echo "unsupported architecture: $(uname -m)"; exit 1 ;;
esac
v=$(curl -fsSL https://api.github.com/repos/railwayapp/nixpacks/releases/latest 2>/dev/null | grep '"tag_name"' | head -1 | cut -d'"' -f4)
[ -n "$v" ] || v=` + NixpacksVersion + `
url="https://github.com/railwayapp/nixpacks/releases/download/$v/nixpacks-$v-$arch-unknown-linux-musl.tar.gz"
echo "Downloading $url"
tmp=$(mktemp -d)
curl -fsSL "$url" -o "$tmp/nixpacks.tar.gz"
tar -xzf "$tmp/nixpacks.tar.gz" -C "$tmp"
install -m 0755 "$tmp/nixpacks" /usr/local/bin/nixpacks
rm -rf "$tmp"
nixpacks --version`

// ValidateServer checks connectivity and Docker availability and stores the result.
func (e *Engine) ValidateServer(ctx context.Context, srv store.Server) (store.Server, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	prev := srv.Status
	check := func() error {
		x, err := e.pool.For(ctx, srv)
		if err != nil {
			return err
		}
		if _, err := executor.Output(ctx, x, "echo ok"); err != nil {
			e.pool.Forget(srv.ID)
			return fmt.Errorf("connection failed: %w", err)
		}
		v, err := docker.Version(ctx, x)
		if err != nil {
			srv.DockerVersion = ""
			return fmt.Errorf("docker is not available: %w", err)
		}
		srv.DockerVersion = v
		if srv.ProxyEnabled {
			srv.ProxyStatus = proxy.Status(ctx, x)
		} else {
			srv.ProxyStatus = "disabled"
		}
		return nil
	}
	err := check()
	srv.LastCheckAt = store.Now()
	if err != nil {
		srv.Status, srv.StatusMessage = "unreachable", err.Error()
	} else {
		srv.Status, srv.StatusMessage = "reachable", ""
	}
	_, _ = e.db.ExecContext(context.WithoutCancel(ctx),
		"UPDATE servers SET status = ?, status_message = ?, docker_version = ?, proxy_status = ?, last_check_at = ? WHERE id = ?",
		srv.Status, srv.StatusMessage, srv.DockerVersion, srv.ProxyStatus, srv.LastCheckAt, srv.ID)
	e.hub.Event("server", srv.ID, srv.Status)
	if prev != srv.Status && prev != "unknown" {
		e.notify.Send(notify.Event{
			Name:    "server." + srv.Status,
			Title:   fmt.Sprintf("Server %s is %s", srv.Name, srv.Status),
			Message: srv.StatusMessage,
			Success: srv.Status == "reachable",
		})
	}
	return srv, err
}

// SetupServer installs Docker, prepares directories, the network and the proxy.
func (e *Engine) SetupServer(ctx context.Context, srv store.Server, withNixpacks bool) error {
	log := &topicLog{e: e, topic: ServerTopic(srv.ID)}
	x, err := e.pool.For(ctx, srv)
	if err != nil {
		log.Printf("❌ %v", err)
		return err
	}
	run := func(title, cmd string) error {
		log.Printf("━━━ %s", title)
		if err := x.Run(ctx, cmd, executor.RunOpts{Stdout: log}); err != nil {
			log.Printf("❌ %s failed: %v", title, err)
			return err
		}
		return nil
	}
	steps := []struct{ title, cmd string }{
		{"Checking prerequisites", "command -v curl >/dev/null || (apt-get update -y && apt-get install -y curl) || (dnf install -y curl) || (apk add --no-cache curl)"},
		{"Installing Docker", "if command -v docker >/dev/null; then docker --version; else curl -fsSL https://get.docker.com | sh; fi"},
		{"Enabling Docker", "(systemctl enable --now docker 2>/dev/null || service docker start 2>/dev/null || true) && docker info --format 'Docker {{.ServerVersion}} ({{.OperatingSystem}})'"},
		{"Preparing directories", fmt.Sprintf("mkdir -p %s/builds %s/apps %s/services %s/backups %s/proxy", e.cfg.DataDir, e.cfg.DataDir, e.cfg.DataDir, e.cfg.DataDir, e.cfg.DataDir)},
		{"Creating network", fmt.Sprintf("docker network inspect %s >/dev/null 2>&1 || docker network create --attachable %s", q(e.cfg.ProxyNetwork), q(e.cfg.ProxyNetwork))},
	}
	if withNixpacks {
		steps = append(steps, struct{ title, cmd string }{"Installing Nixpacks", installNixpacks})
	}
	for _, s := range steps {
		if err := run(s.title, s.cmd); err != nil {
			e.hub.Publish(ServerTopic(srv.ID), "status", "failed")
			return err
		}
	}
	if srv.ProxyEnabled {
		log.Printf("━━━ Starting Traefik proxy")
		if err := proxy.Ensure(ctx, x, e.ProxyOptions(ctx), false); err != nil {
			log.Printf("❌ proxy: %v", err)
		}
	}
	_, _ = e.ValidateServer(ctx, srv)
	log.Printf("✅ Server is ready.")
	e.hub.Publish(ServerTopic(srv.ID), "status", "success")
	return nil
}

// Monitor periodically reconciles server reachability and resource statuses.
func (e *Engine) Monitor(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	e.reconcile(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.reconcile(ctx)
		}
	}
}

// reconcile checks every server (in parallel, backing off from unreachable
// ones) and aligns the stored status of each resource with its containers.
// One `docker ps` per server feeds everything, including applications that
// run on several servers.
func (e *Engine) reconcile(ctx context.Context) {
	servers, err := store.Select[store.Server](ctx, e.db, "SELECT * FROM servers")
	if err != nil {
		return
	}
	var mu sync.Mutex
	reached := map[int64]bool{}
	byRes := map[string][]docker.Container{} // resource uuid -> containers on every reached server
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, srv := range servers {
		if !e.reconcileDue(srv) {
			continue
		}
		wg.Add(1)
		go func(srv store.Server) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			srv, err := e.ValidateServer(ctx, srv)
			e.reconcileNote(srv.ID, err == nil)
			if err != nil {
				return
			}
			found, err := e.reconcileServer(ctx, srv)
			if err != nil {
				slog.Debug("reconcile", "server", srv.Name, "err", err)
				return
			}
			mu.Lock()
			reached[srv.ID] = true
			for uuid, cs := range found {
				byRes[uuid] = append(byRes[uuid], cs...)
			}
			mu.Unlock()
		}(srv)
	}
	wg.Wait()
	// Applications on several servers: status is aggregated over all of them,
	// once every one of their servers answered.
	apps, err := store.Select[store.Application](ctx, e.db,
		"SELECT * FROM applications WHERE id IN (SELECT application_id FROM application_servers) AND status NOT IN ('deploying', 'queued')")
	if err != nil {
		return
	}
	for _, app := range apps {
		complete := true
		for _, id := range e.AppServerIDs(ctx, app) {
			if !reached[id] {
				complete = false
			}
		}
		if !complete {
			continue
		}
		status := aggregateStatus(byRes[app.UUID], true)
		if (status == "stopped" && app.Status == "failed") || status == app.Status {
			continue
		}
		_, _ = e.db.ExecContext(ctx, "UPDATE applications SET status = ? WHERE id = ?", status, app.ID)
		e.hub.Event("application", app.ID, status)
	}
}

// reconcileDue skips a server that failed recently: the wait doubles with
// every failure, up to ten minutes, so unreachable servers do not stretch
// every tick with connection timeouts.
func (e *Engine) reconcileDue(srv store.Server) bool {
	e.recon.Lock()
	defer e.recon.Unlock()
	if e.recon.next == nil {
		e.recon.fails, e.recon.next, e.recon.proxyAt = map[int64]int{}, map[int64]time.Time{}, map[int64]time.Time{}
	}
	return time.Now().After(e.recon.next[srv.ID])
}

func (e *Engine) reconcileNote(serverID int64, ok bool) {
	e.recon.Lock()
	defer e.recon.Unlock()
	if ok {
		delete(e.recon.fails, serverID)
		delete(e.recon.next, serverID)
		return
	}
	n := e.recon.fails[serverID] + 1
	e.recon.fails[serverID] = n
	wait := min(time.Duration(1<<min(n, 5))*30*time.Second, 10*time.Minute)
	e.recon.next[serverID] = time.Now().Add(wait)
}

// proxyCheckDue limits the proxy upgrade check to once every ten minutes per server.
func (e *Engine) proxyCheckDue(serverID int64) bool {
	e.recon.Lock()
	defer e.recon.Unlock()
	if e.recon.proxyAt == nil {
		e.recon.proxyAt = map[int64]time.Time{}
	}
	if time.Since(e.recon.proxyAt[serverID]) < 10*time.Minute {
		return false
	}
	e.recon.proxyAt[serverID] = time.Now()
	return true
}

// reconcileServer aligns the resources of one server with its containers and
// returns the managed containers found, by resource.
func (e *Engine) reconcileServer(ctx context.Context, srv store.Server) (map[string][]docker.Container, error) {
	x, err := e.pool.For(ctx, srv)
	if err != nil {
		return nil, err
	}
	if srv.ProxyEnabled && srv.ProxyStatus == "running" && e.proxyCheckDue(srv.ID) {
		// Upgrades a proxy running an outdated Traefik image (no-op otherwise).
		if err := proxy.Ensure(ctx, x, e.ProxyOptions(ctx), false); err != nil {
			slog.Warn("proxy upgrade", "server", srv.Name, "err", err)
		}
	}
	cs, err := docker.PS(ctx, x, true, "label="+docker.LabelManaged+"=true")
	if err != nil {
		return nil, err
	}
	byRes := map[string][]docker.Container{}
	for _, c := range cs {
		// Replicas have their own status (db_replicas): they don't make the database degraded.
		if r := c.Labels[docker.LabelResource]; r != "" && c.Labels[LabelReplica] == "" {
			byRes[r] = append(byRes[r], c)
		}
	}
	e.fenceFormerPrimaries(ctx, x, srv, cs)
	type row struct {
		ID     int64  `db:"id"`
		UUID   string `db:"uuid"`
		Status string `db:"status"`
	}
	for _, table := range []string{"applications", "databases", "services"} {
		query := "SELECT id, uuid, status FROM " + table + " WHERE server_id = ?"
		if table == "applications" {
			query += " AND id NOT IN (SELECT application_id FROM application_servers)"
		}
		rows, err := store.Select[row](ctx, e.db, query, srv.ID)
		if err != nil {
			continue
		}
		kind := map[string]string{"applications": "application", "databases": "database", "services": "service"}[table]
		for _, r := range rows {
			if r.Status == "deploying" || r.Status == "queued" {
				continue
			}
			status := aggregateStatus(byRes[r.UUID], table == "applications")
			if status == "stopped" && r.Status == "failed" {
				continue // keep "failed" until the next deploy
			}
			if status != r.Status {
				_, _ = e.db.ExecContext(ctx, "UPDATE "+table+" SET status = ? WHERE id = ?", status, r.ID)
				e.hub.Event(kind, r.ID, status)
			}
		}
	}
	return byRes, nil
}
