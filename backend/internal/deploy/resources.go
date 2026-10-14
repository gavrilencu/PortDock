package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/overlay"
	"github.com/portdock/portdock/internal/proxy"
	"github.com/portdock/portdock/internal/store"
	"github.com/portdock/portdock/internal/templates"
)

// ---- applications ----

// AppAction runs start | stop | restart on an application's production containers on all its servers.
func (e *Engine) AppAction(ctx context.Context, app store.Application, action string) error {
	found := 0
	err := e.eachTarget(ctx, app, func(t target) error {
		if app.BuildPack == "compose" {
			_, err := executor.Output(ctx, t.x, fmt.Sprintf("docker compose -p %s %s", q(composeProject(app.UUID)), action))
			found++
			return err
		}
		cs, err := docker.PS(ctx, t.x, true, "label="+docker.LabelResource+"="+app.UUID, "label="+docker.LabelPR+"=0")
		if err != nil {
			return err
		}
		found += len(cs)
		for _, c := range cs {
			if err := docker.ContainerAction(ctx, t.x, action, c.Name); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil && found == 0 {
		err = errors.New("no containers found; deploy the application first")
	}
	status := e.liveStatus(ctx, app)
	_, _ = e.db.ExecContext(ctx, "UPDATE applications SET status = ? WHERE id = ?", status, app.ID)
	e.hub.Event("application", app.ID, status)
	return err
}

// AppContainers lists all containers (production + previews) of an application on all its servers.
func (e *Engine) AppContainers(ctx context.Context, app store.Application) ([]docker.Container, error) {
	filter := "label=" + docker.LabelResource + "=" + app.UUID
	if app.BuildPack == "compose" {
		filter = "label=com.docker.compose.project=" + composeProject(app.UUID)
	}
	cs, err := e.listAppContainers(ctx, app, true, filter)
	if err != nil && len(cs) == 0 {
		return nil, err
	}
	return cs, nil
}

// removeAppFrom deletes containers, routes, images and files of app on one server.
func (e *Engine) removeAppFrom(ctx context.Context, x executor.Executor, app store.Application) error {
	e.removeAllRoutes(ctx, x, app.UUID)
	script := fmt.Sprintf(
		"docker compose -p %[1]s down -v --remove-orphans >/dev/null 2>&1; "+
			"%[6]s | xargs -r docker rm -f >/dev/null 2>&1; "+
			"%[7]s | sort -u | xargs -r docker rmi -f >/dev/null 2>&1; "+
			"docker images --format '{{.Repository}}:{{.Tag}}' | grep -E '^(portdock|shipyard)/%[3]s' | xargs -r docker rmi -f >/dev/null 2>&1; "+
			"rm -rf %[4]s %[5]s 2>/dev/null; true",
		q(composeProject(app.UUID)), docker.LabelResource, app.UUID,
		q(path.Join(e.cfg.DataDir, "apps", app.UUID)), q(path.Join(e.cfg.DataDir, "builds", app.UUID)),
		docker.IDsCmd("ps", true, "label="+docker.LabelResource+"="+app.UUID), docker.IDsCmd("images", false, "label="+docker.LabelResource+"="+app.UUID))
	return x.Run(ctx, script, executor.RunOpts{})
}

// DeleteApp removes all containers, images and files of an application from
// every server, and its persistent volumes when volumes is set.
func (e *Engine) DeleteApp(ctx context.Context, app store.Application, volumes bool) error {
	err := e.eachTarget(ctx, app, func(t target) error {
		if err := e.removeAppFrom(ctx, t.x, app); err != nil {
			return err
		}
		if volumes {
			return removeAppVolumes(ctx, t.x, app.UUID)
		}
		return nil
	})
	if app.BuildServerID != 0 && app.BuildServerID != app.ServerID {
		if x, _, berr := e.pool.ForID(ctx, app.BuildServerID); berr == nil {
			_ = e.removeAppFrom(ctx, x, app)
		}
	}
	return err
}

// RemoveAppFromServer undeploys app from a server that no longer belongs to it.
func (e *Engine) RemoveAppFromServer(ctx context.Context, app store.Application, serverID int64) error {
	x, _, err := e.pool.ForID(ctx, serverID)
	if err != nil {
		return err
	}
	return e.removeAppFrom(ctx, x, app)
}

// CleanupPreview removes the containers and images of a pull request preview.
func (e *Engine) CleanupPreview(ctx context.Context, app store.Application, pr int) error {
	x, _, err := e.pool.ForID(ctx, app.ServerID)
	if err != nil {
		return err
	}
	_ = writeRoutes(ctx, x, e.routesPath(app.UUID, pr), nil)
	script := fmt.Sprintf(
		docker.IDsCmd("ps", true, "label="+docker.LabelResource+"="+app.UUID, fmt.Sprintf("label=%s=%d", docker.LabelPR, pr))+" | xargs -r docker rm -f >/dev/null 2>&1; "+
			"docker images --format '{{.Repository}}:{{.Tag}}' | grep -E '^(portdock|shipyard)/%s-pr%d:' | xargs -r docker rmi -f >/dev/null 2>&1; "+
			"rm -f %s; true",
		app.UUID, pr,
		q(fmt.Sprintf("%s/apps/%s/.env.pr%d", e.cfg.DataDir, app.UUID, pr)))
	if err := x.Run(ctx, script, executor.RunOpts{}); err != nil {
		return err
	}
	return removeAppVolumes(ctx, x, app.UUID, fmt.Sprintf("%s=%d", docker.LabelPR, pr))
}

// ---- databases ----

func (e *Engine) dbPassword(db store.Database) string { return e.box.MustDecrypt(db.Password) }

// DatabaseURLs returns the internal (Docker network) and public connection URLs.
// fallbackHost (e.g. the server's detected public IP) is used when the database has
// no public host and the server's own host is not reachable from outside.
func (e *Engine) DatabaseURLs(db store.Database, srv store.Server, fallbackHost string) (internal, public string) {
	eng, ok := templates.Engines[db.Engine]
	if !ok {
		return "", ""
	}
	pass := e.dbPassword(db)
	internal = eng.URL(db.Username, pass, db.UUID, eng.Port, db.DatabaseName)
	if db.PublicPort > 0 {
		public = eng.URL(db.Username, pass, DatabasePublicHost(db, srv, fallbackHost), db.PublicPort, db.DatabaseName)
	}
	return
}

// DatabasePublicHost picks the host external clients use to reach a database:
// its public host (domain) if set, else the server's host when it is reachable
// from outside, else fallback (the server's public IP), and "localhost" last.
func DatabasePublicHost(db store.Database, srv store.Server, fallback string) string {
	if db.PublicHost != "" {
		return db.PublicHost
	}
	if h := strings.TrimSpace(srv.Host); !srv.IsLocal && h != "" && !IsLoopbackHost(h) {
		return h
	}
	if f := strings.TrimSpace(fallback); f != "" && !IsLoopbackHost(f) {
		return f
	}
	return "localhost"
}

// IsLoopbackHost reports whether h only names this machine (localhost, 127.0.0.0/8, ::1).
func IsLoopbackHost(h string) bool {
	h = strings.ToLower(strings.Trim(h, "[]"))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

// StartDatabase creates the database container (or starts an existing one).
func (e *Engine) StartDatabase(ctx context.Context, db store.Database, recreate bool) error {
	eng, ok := templates.Engines[db.Engine]
	if !ok {
		return fmt.Errorf("unknown engine %q", db.Engine)
	}
	x, srv, err := e.pool.ForID(ctx, db.ServerID)
	if err != nil {
		return err
	}
	if err := docker.EnsureNetwork(ctx, x, e.cfg.ProxyNetwork); err != nil {
		return err
	}
	privateIP := e.privateIP(ctx, srv)
	if db.OverlayPort > 0 && privateIP == "" {
		return errors.New("the database's server is not in the private network: add it in Settings → Network & proxy, or turn off the private port")
	}
	if !recreate && !publishedPortMatches(ctx, x, db, privateIP) {
		recreate = true // a published port changed while the container was stopped
	}
	if !recreate {
		if state, _, err := docker.State(ctx, x, db.UUID); err == nil {
			if state != "running" {
				if _, err := executor.Output(ctx, x, "docker start "+q(db.UUID)); err != nil {
					return err
				}
			}
			e.setDBStatus(ctx, db.ID, "running")
			return nil
		}
	}
	pass := e.dbPassword(db)
	pitr := db.PITR && db.Engine == "postgres"
	if pitr {
		if err := e.preparePITR(ctx, x, db); err != nil {
			e.setDBStatus(ctx, db.ID, "failed")
			return fmt.Errorf("prepare the WAL archive: %w", err)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "(docker rm -f %s >/dev/null 2>&1 || true) && docker run -d --name %s --restart unless-stopped --network %s --network-alias %s",
		q(db.UUID), q(db.UUID), q(e.cfg.ProxyNetwork), q(proxy.SafeName(db.Name)))
	fmt.Fprintf(&b, " -v %s:%s", q(db.VolumeName()), eng.DataPath)
	if pitr {
		fmt.Fprintf(&b, " -v %s:/pitr", q(e.pitrDir(db)))
	}
	for k, v := range eng.Env(db.Username, pass, db.DatabaseName) {
		b.WriteString(" -e " + q(k+"="+v))
	}
	fmt.Fprintf(&b, " --health-cmd %s --health-interval 10s --health-timeout 5s --health-retries 10", q(eng.HealthCmd(db.Username, pass, db.DatabaseName)))
	fmt.Fprintf(&b, " --label %s=true --label %s=database --label %s=%s --log-opt max-size=10m --log-opt max-file=3",
		docker.LabelManaged, docker.LabelKind, docker.LabelResource, q(db.UUID))
	if db.CPULimit != "" {
		b.WriteString(" --cpus " + q(db.CPULimit))
	}
	if db.MemoryLimit != "" {
		b.WriteString(" --memory " + q(db.MemoryLimit))
	}
	if db.PublicPort > 0 {
		fmt.Fprintf(&b, " -p %d:%d", db.PublicPort, eng.Port)
	}
	if db.OverlayPort > 0 {
		// Only on the private network: the other servers' applications and replicas.
		fmt.Fprintf(&b, " -p %s --label %s=true", q(fmt.Sprintf("%s:%d:%d", privateIP, db.OverlayPort, eng.Port)), overlay.LabelBind)
	}
	b.WriteString(" " + q(db.Image))
	if cmd := eng.Command(pass); cmd != "" {
		b.WriteString(" " + cmd)
	}
	// Extra server flags become quoted command arguments, never docker options.
	args, err := docker.CommandArgs(db.CustomArgs)
	if err != nil {
		e.setDBStatus(ctx, db.ID, "failed")
		return err
	}
	b.WriteString(args)
	if pitr {
		b.WriteString(pitrArgs())
	}
	if _, err := executor.Output(ctx, x, b.String()); err != nil {
		e.setDBStatus(ctx, db.ID, "failed")
		return err
	}
	e.setDBStatus(ctx, db.ID, "running")
	if init := eng.InitCmd(db.Username, pass, db.DatabaseName); init != "" {
		go e.initDatabase(context.WithoutCancel(ctx), x, db, eng.HealthCmd(db.Username, pass, db.DatabaseName), init)
	}
	return nil
}

// initDatabase waits for a new container to accept connections, then runs the
// engine's init command (SQL Server doesn't create the database by itself).
func (e *Engine) initDatabase(ctx context.Context, x executor.Executor, db store.Database, health, init string) {
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := executor.Output(ctx, x, "docker exec "+q(db.UUID)+" sh -c "+q(health)); err == nil {
			if _, err := executor.Output(ctx, x, "docker exec "+q(db.UUID)+" sh -c "+q(init)); err != nil {
				e.notify.Send(notify.Event{Name: "database.failed", Title: "Database setup failed: " + db.Name, Message: err.Error()})
			}
			return
		}
		time.Sleep(3 * time.Second)
	}
}

// publishedPortMatches reports whether the existing container publishes exactly
// db.PublicPort (or nothing when it is 0). A missing container counts as a match:
// StartDatabase creates it with the current settings anyway.
func publishedPortMatches(ctx context.Context, x executor.Executor, db store.Database, privateIP string) bool {
	out, err := executor.Output(ctx, x, "docker inspect -f "+q(`{{range $p, $b := .HostConfig.PortBindings}}{{range $b}}{{.HostIp}}:{{.HostPort}} {{end}}{{end}}`)+" "+q(db.UUID))
	if err != nil {
		return true
	}
	want := map[string]bool{}
	if db.PublicPort > 0 {
		want[":"+strconv.Itoa(db.PublicPort)] = true
	}
	if db.OverlayPort > 0 && privateIP != "" {
		want[privateIP+":"+strconv.Itoa(db.OverlayPort)] = true
	}
	got := strings.Fields(strings.TrimSpace(out))
	if len(got) != len(want) {
		return false
	}
	for _, g := range got {
		if !want[strings.TrimPrefix(g, "0.0.0.0")] {
			return false
		}
	}
	return true
}

// privateIP is the private network address of srv ("" when it is not in the network).
func (e *Engine) privateIP(ctx context.Context, srv store.Server) string {
	if !srv.OverlayJoin || srv.OverlayIP == "" || e.db.Setting(ctx, "overlay_enabled") != "true" {
		return ""
	}
	return srv.OverlayIP
}

// DatabasePrivateURL is how the applications of other servers reach db on
// the private network ("" without a private port).
func (e *Engine) DatabasePrivateURL(ctx context.Context, db store.Database, srv store.Server) (url, address string) {
	eng, ok := templates.Engines[db.Engine]
	ip := e.privateIP(ctx, srv)
	if !ok || db.OverlayPort == 0 || ip == "" {
		return "", ""
	}
	return eng.URL(db.Username, e.dbPassword(db), ip, db.OverlayPort, db.DatabaseName), fmt.Sprintf("%s:%d", ip, db.OverlayPort)
}

func (e *Engine) setDBStatus(ctx context.Context, id int64, status string) {
	_, _ = e.db.ExecContext(ctx, "UPDATE databases SET status = ? WHERE id = ?", status, id)
	e.hub.Event("database", id, status)
}

func (e *Engine) DatabaseAction(ctx context.Context, db store.Database, action string) error {
	if action == "start" {
		return e.StartDatabase(ctx, db, false)
	}
	x, _, err := e.pool.ForID(ctx, db.ServerID)
	if err != nil {
		return err
	}
	if err := docker.ContainerAction(ctx, x, action, db.UUID); err != nil {
		return err
	}
	status := "running"
	if action == "stop" {
		status = "stopped"
	}
	e.setDBStatus(ctx, db.ID, status)
	return nil
}

func (e *Engine) DeleteDatabase(ctx context.Context, db store.Database, volumes bool) error {
	e.DeleteReplicas(ctx, db)
	x, _, err := e.pool.ForID(ctx, db.ServerID)
	if err != nil {
		return err
	}
	cmd := "docker rm -f " + q(db.UUID) + " >/dev/null 2>&1; true"
	if volumes {
		cmd += "; docker volume rm -f " + q(db.VolumeName()) + " >/dev/null 2>&1; true"
	}
	return x.Run(ctx, cmd, executor.RunOpts{})
}

// ---- backups ----

// StreamBackup writes a backup file's bytes to w.
func (e *Engine) StreamBackup(ctx context.Context, db store.Database, run store.BackupRun, w io.Writer) error {
	if !strings.HasPrefix(run.File, e.backupDir(db)+"/") {
		return errors.New("invalid backup file")
	}
	x, _, err := e.pool.ForID(ctx, db.ServerID)
	if err != nil {
		return err
	}
	return x.Run(ctx, "cat "+q(run.File), executor.RunOpts{Stdout: w, Stderr: io.Discard})
}

// ---- services (one-click docker compose stacks) ----

type topicLog struct {
	e     *Engine
	topic string
	mu    sync.Mutex
	b     strings.Builder
}

func (t *topicLog) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			t.line(line)
		}
	}
	return len(p), nil
}
func (t *topicLog) Printf(f string, a ...any) { t.line(fmt.Sprintf(f, a...)) }
func (t *topicLog) line(s string) {
	s = time.Now().UTC().Format("15:04:05") + " " + s
	t.mu.Lock()
	t.b.WriteString(s + "\n")
	t.mu.Unlock()
	t.e.hub.Publish(t.topic, "log", s)
}

func ServiceTopic(id int64) string { return fmt.Sprintf("service:%d", id) }

func (e *Engine) setServiceStatus(ctx context.Context, id int64, status string) {
	_, _ = e.db.ExecContext(ctx, "UPDATE services SET status = ? WHERE id = ?", status, id)
	e.hub.Event("service", id, status)
}

// DeployService writes the compose stack to the server and brings it up.
// It returns the deployment log.
func (e *Engine) DeployService(ctx context.Context, svc store.Service) (string, error) {
	log := &topicLog{e: e, topic: ServiceTopic(svc.ID)}
	e.setServiceStatus(ctx, svc.ID, "deploying")
	err := func() error {
		x, srv, err := e.pool.ForID(ctx, svc.ServerID)
		if err != nil {
			return err
		}
		log.Printf("Deploying service %s on %s", svc.Name, srv.Name)
		if err := docker.EnsureNetwork(ctx, x, e.cfg.ProxyNetwork); err != nil {
			return err
		}
		var domains []string
		if svc.Domain != "" {
			domains = store.SplitList(svc.Domain)
			if srv.ProxyEnabled {
				if err := proxy.Ensure(ctx, x, e.ProxyOptions(ctx), false); err != nil {
					log.Printf("⚠ proxy: %v", err)
				}
			}
		}
		dir := path.Join(e.cfg.DataDir, "services", svc.UUID)
		file := dir + "/docker-compose.yml"
		if err := executor.WriteFile(ctx, x, file, []byte(svc.Compose), "0644"); err != nil {
			return err
		}
		env, err := e.ResolveEnv(ctx, "service", svc.ID, false)
		if err != nil {
			return err
		}
		net := e.Network(ctx) // the compose build is logged without its arguments
		_, err = RunComposeUp(ctx, ComposeUp{
			X: x, Project: composeProject(svc.UUID), Dir: dir, File: file, Domains: domains, HTTPS: true,
			HostAccess: svc.HostAccess, Root: dir, Forbidden: []string{e.cfg.DataDir},
			Env: env.Runtime, Kind: "service", Resource: svc.UUID, Network: e.cfg.ProxyNetwork, Timeout: 180, Log: log,
			ProxyEnv: containerProxyEnv(net, ""), BuildArgs: buildProxyEnv(net),
		})
		return err
	}()
	if err != nil {
		log.Printf("❌ %v", err)
		e.setServiceStatus(ctx, svc.ID, "failed")
	} else {
		log.Printf("✅ Service is up.")
		e.setServiceStatus(ctx, svc.ID, "running")
	}
	e.hub.Publish(ServiceTopic(svc.ID), "status", map[bool]string{true: "failed", false: "success"}[err != nil])
	return log.b.String(), err
}

func (e *Engine) ServiceAction(ctx context.Context, svc store.Service, action string) error {
	x, _, err := e.pool.ForID(ctx, svc.ServerID)
	if err != nil {
		return err
	}
	if _, err := executor.Output(ctx, x, fmt.Sprintf("docker compose -p %s %s", q(composeProject(svc.UUID)), action)); err != nil {
		return err
	}
	status := "running"
	if action == "stop" {
		status = "stopped"
	}
	e.setServiceStatus(ctx, svc.ID, status)
	return nil
}

func (e *Engine) ServiceContainers(ctx context.Context, svc store.Service) ([]docker.Container, error) {
	x, _, err := e.pool.ForID(ctx, svc.ServerID)
	if err != nil {
		return nil, err
	}
	return docker.PS(ctx, x, true, "label=com.docker.compose.project="+composeProject(svc.UUID))
}

func (e *Engine) DeleteService(ctx context.Context, svc store.Service, volumes bool) error {
	x, _, err := e.pool.ForID(ctx, svc.ServerID)
	if err != nil {
		return err
	}
	flag := ""
	if volumes {
		flag = " -v"
	}
	cmd := fmt.Sprintf("docker compose -p %s down --remove-orphans%s >/dev/null 2>&1; rm -rf %s; true",
		q(composeProject(svc.UUID)), flag, q(path.Join(e.cfg.DataDir, "services", svc.UUID)))
	return x.Run(ctx, cmd, executor.RunOpts{})
}
