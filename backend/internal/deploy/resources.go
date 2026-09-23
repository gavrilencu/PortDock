package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shipyard/shipyard/internal/docker"
	"github.com/shipyard/shipyard/internal/executor"
	"github.com/shipyard/shipyard/internal/proxy"
	"github.com/shipyard/shipyard/internal/store"
	"github.com/shipyard/shipyard/internal/templates"
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
			"docker ps -aq --filter label=%[2]s=%[3]s | xargs -r docker rm -f >/dev/null 2>&1; "+
			"docker images -q --filter label=%[2]s=%[3]s | sort -u | xargs -r docker rmi -f >/dev/null 2>&1; "+
			"docker images --format '{{.Repository}}:{{.Tag}}' | grep '^shipyard/%[3]s' | xargs -r docker rmi -f >/dev/null 2>&1; "+
			"rm -rf %[4]s %[5]s 2>/dev/null; true",
		q(composeProject(app.UUID)), docker.LabelResource, app.UUID,
		q(path.Join(e.cfg.DataDir, "apps", app.UUID)), q(path.Join(e.cfg.DataDir, "builds", app.UUID)))
	return x.Run(ctx, script, executor.RunOpts{})
}

// DeleteApp removes all containers, images and files of an application from every server.
func (e *Engine) DeleteApp(ctx context.Context, app store.Application) error {
	err := e.eachTarget(ctx, app, func(t target) error { return e.removeAppFrom(ctx, t.x, app) })
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
		"docker ps -aq --filter label=%s=%s --filter label=%s=%d | xargs -r docker rm -f >/dev/null 2>&1; "+
			"docker images --format '{{.Repository}}:{{.Tag}}' | grep '^shipyard/%s-pr%d:' | xargs -r docker rmi -f >/dev/null 2>&1; "+
			"rm -f %s; true",
		docker.LabelResource, app.UUID, docker.LabelPR, pr, app.UUID, pr,
		q(fmt.Sprintf("%s/apps/%s/.env.pr%d", e.cfg.DataDir, app.UUID, pr)))
	return x.Run(ctx, script, executor.RunOpts{})
}

// ---- databases ----

func (e *Engine) dbPassword(db store.Database) string { return e.box.MustDecrypt(db.Password) }

// DatabaseURLs returns the internal (Docker network) and public connection URLs.
func (e *Engine) DatabaseURLs(db store.Database, srv store.Server) (internal, public string) {
	eng, ok := templates.Engines[db.Engine]
	if !ok {
		return "", ""
	}
	pass := e.dbPassword(db)
	internal = eng.URL(db.Username, pass, db.UUID, eng.Port, db.DatabaseName)
	if db.PublicPort > 0 {
		host := srv.Host
		if srv.IsLocal || host == "" {
			host = "localhost"
		}
		public = eng.URL(db.Username, pass, host, db.PublicPort, db.DatabaseName)
	}
	return
}

// StartDatabase creates the database container (or starts an existing one).
func (e *Engine) StartDatabase(ctx context.Context, db store.Database, recreate bool) error {
	eng, ok := templates.Engines[db.Engine]
	if !ok {
		return fmt.Errorf("unknown engine %q", db.Engine)
	}
	x, _, err := e.pool.ForID(ctx, db.ServerID)
	if err != nil {
		return err
	}
	if err := docker.EnsureNetwork(ctx, x, e.cfg.ProxyNetwork); err != nil {
		return err
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
	var b strings.Builder
	fmt.Fprintf(&b, "(docker rm -f %s >/dev/null 2>&1 || true) && docker run -d --name %s --restart unless-stopped --network %s --network-alias %s",
		q(db.UUID), q(db.UUID), q(e.cfg.ProxyNetwork), q(proxy.SafeName(db.Name)))
	fmt.Fprintf(&b, " -v %s:%s", q("sy-"+db.UUID), eng.DataPath)
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
	b.WriteString(" " + q(db.Image))
	if cmd := eng.Command(pass); cmd != "" {
		b.WriteString(" " + cmd)
	}
	if db.CustomArgs != "" {
		b.WriteString(" " + db.CustomArgs)
	}
	if _, err := executor.Output(ctx, x, b.String()); err != nil {
		e.setDBStatus(ctx, db.ID, "failed")
		return err
	}
	e.setDBStatus(ctx, db.ID, "running")
	return nil
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
	x, _, err := e.pool.ForID(ctx, db.ServerID)
	if err != nil {
		return err
	}
	cmd := "docker rm -f " + q(db.UUID) + " >/dev/null 2>&1; true"
	if volumes {
		cmd += "; docker volume rm -f " + q("sy-"+db.UUID) + " >/dev/null 2>&1; true"
	}
	return x.Run(ctx, cmd, executor.RunOpts{})
}

// ---- backups ----

func (e *Engine) backupDir(db store.Database) string {
	return path.Join(e.cfg.DataDir, "backups", db.UUID)
}

// RunBackup dumps the database into the server's backup directory,
// applies retention and optionally uploads to S3.
func (e *Engine) RunBackup(ctx context.Context, b store.Backup) (store.BackupRun, error) {
	run := store.BackupRun{BackupID: b.ID, Status: "running", CreatedAt: store.Now()}
	if _, err := store.Insert(ctx, e.db, "backup_runs", &run); err != nil {
		return run, err
	}
	finish := func(err error) (store.BackupRun, error) {
		run.Status = "success"
		if err != nil {
			run.Status, run.Message = "failed", err.Error()
		}
		_ = store.Update(ctx, e.db, "backup_runs", &run)
		_, _ = e.db.ExecContext(ctx, "UPDATE backups SET last_run_at = ? WHERE id = ?", store.Now(), b.ID)
		e.hub.Event("backup", b.ID, run.Status)
		return run, err
	}
	db, err := store.ByID[store.Database](ctx, e.db, "databases", b.DatabaseID)
	if err != nil {
		return finish(err)
	}
	eng := templates.Engines[db.Engine]
	cmd := eng.BackupCmd(db.Username, e.dbPassword(db), db.DatabaseName)
	if cmd == "" {
		return finish(fmt.Errorf("backups are not supported for %s", eng.Name))
	}
	x, _, err := e.pool.ForID(ctx, db.ServerID)
	if err != nil {
		return finish(err)
	}
	dir := e.backupDir(db)
	file := fmt.Sprintf("%s/%s.%s", dir, time.Now().UTC().Format("20060102-150405"), eng.BackupExt)
	// Enable pipefail where the shell supports it so a failing dump isn't masked by gzip.
	const pipefail = "(set -o pipefail) 2>/dev/null && set -o pipefail; "
	script := fmt.Sprintf("mkdir -p %s && docker exec %s sh -c %s > %s && stat -c %%s %s",
		q(dir), q(db.UUID), q(pipefail+cmd), q(file), q(file))
	out, err := executor.Output(ctx, x, script)
	if err != nil {
		_ = x.Run(ctx, "rm -f "+q(file), executor.RunOpts{})
		return finish(err)
	}
	run.File = file
	run.Size, _ = strconv.ParseInt(strings.TrimSpace(out), 10, 64)

	if b.Retention > 0 {
		_ = x.Run(ctx, fmt.Sprintf("cd %s && ls -1t | tail -n +%d | xargs -r rm -f", q(dir), b.Retention+1), executor.RunOpts{})
	}
	if b.UploadS3 {
		if err := e.uploadS3(ctx, x, file, db.UUID); err != nil {
			return finish(fmt.Errorf("backup stored locally but S3 upload failed: %w", err))
		}
	}
	return finish(nil)
}

func (e *Engine) uploadS3(ctx context.Context, x executor.Executor, file, prefix string) error {
	endpoint := e.db.Setting(ctx, "s3_endpoint")
	bucket := e.db.Setting(ctx, "s3_bucket")
	key := e.db.Setting(ctx, "s3_access_key")
	secret := e.box.MustDecrypt(e.db.Setting(ctx, "s3_secret_key"))
	region := e.db.Setting(ctx, "s3_region")
	if bucket == "" || key == "" || secret == "" {
		return errors.New("S3 storage is not configured (Settings → Backups)")
	}
	if region == "" {
		region = "us-east-1"
	}
	dir, name := path.Split(file)
	cmd := fmt.Sprintf("docker run --rm -v %s:/b:ro -e AWS_ACCESS_KEY_ID=%s -e AWS_SECRET_ACCESS_KEY=%s -e AWS_DEFAULT_REGION=%s amazon/aws-cli s3 cp /b/%s %s",
		q(dir), q(key), q(secret), q(region), q(name), q("s3://"+bucket+"/shipyard/"+prefix+"/"+name))
	if endpoint != "" {
		cmd += " --endpoint-url " + q(endpoint)
	}
	_, err := executor.Output(ctx, x, cmd)
	return err
}

// RestoreBackup pipes a backup file into the database container.
func (e *Engine) RestoreBackup(ctx context.Context, db store.Database, run store.BackupRun) error {
	eng := templates.Engines[db.Engine]
	cmd := eng.RestoreCmd(db.Username, e.dbPassword(db), db.DatabaseName)
	if cmd == "" {
		return fmt.Errorf("restore is not supported for %s", eng.Name)
	}
	if !strings.HasPrefix(run.File, e.backupDir(db)+"/") {
		return errors.New("invalid backup file")
	}
	x, _, err := e.pool.ForID(ctx, db.ServerID)
	if err != nil {
		return err
	}
	_, err = executor.Output(ctx, x, fmt.Sprintf("cat %s | docker exec -i %s sh -c %s", q(run.File), q(db.UUID), q(cmd)))
	return err
}

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
		return RunComposeUp(ctx, ComposeUp{
			X: x, Project: composeProject(svc.UUID), Dir: dir, File: file, Domains: domains, HTTPS: true,
			Env: env.Runtime, Kind: "service", Resource: svc.UUID, Network: e.cfg.ProxyNetwork, Timeout: 180, Log: log,
		})
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
