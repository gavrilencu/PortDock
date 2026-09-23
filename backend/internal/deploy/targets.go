package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/shipyard/shipyard/internal/docker"
	"github.com/shipyard/shipyard/internal/executor"
	"github.com/shipyard/shipyard/internal/registry"
	"github.com/shipyard/shipyard/internal/store"
)

// target is one server an application runs on.
type target struct {
	x   executor.Executor
	srv store.Server
}

// AppServerIDs returns the primary server followed by additional servers.
func (e *Engine) AppServerIDs(ctx context.Context, app store.Application) []int64 {
	ids := []int64{app.ServerID}
	rows, err := e.db.QueryContext(ctx, "SELECT server_id FROM application_servers WHERE application_id = ? ORDER BY server_id", app.ID)
	if err != nil {
		return ids
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil && id != app.ServerID {
			ids = append(ids, id)
		}
	}
	return ids
}

// SetAppServers replaces the additional servers of an application.
func (e *Engine) SetAppServers(ctx context.Context, app store.Application, ids []int64) error {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM application_servers WHERE application_id = ?", app.ID); err != nil {
		return err
	}
	for _, id := range ids {
		if id == app.ServerID {
			continue
		}
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO application_servers(application_id, server_id) VALUES(?, ?)", app.ID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// appTargets connects to every server of the application.
func (e *Engine) appTargets(ctx context.Context, app store.Application) ([]target, error) {
	var out []target
	for _, id := range e.AppServerIDs(ctx, app) {
		x, srv, err := e.pool.ForID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("connect to server #%d: %w", id, err)
		}
		out = append(out, target{x: x, srv: srv})
	}
	return out, nil
}

// eachTarget runs fn on every reachable server and joins the errors.
func (e *Engine) eachTarget(ctx context.Context, app store.Application, fn func(t target) error) error {
	var errs []error
	for _, id := range e.AppServerIDs(ctx, app) {
		x, srv, err := e.pool.ForID(ctx, id)
		if err != nil {
			errs = append(errs, fmt.Errorf("server #%d: %w", id, err))
			continue
		}
		if err := fn(target{x: x, srv: srv}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", srv.Name, err))
		}
	}
	return errors.Join(errs...)
}

// Registry loads an application's registry (nil when none is configured).
func (e *Engine) Registry(ctx context.Context, id int64) (*store.Registry, error) {
	if id == 0 {
		return nil, nil
	}
	reg, err := store.ByID[store.Registry](ctx, e.db, "registries", id)
	if err != nil {
		return nil, fmt.Errorf("registry #%d: %w", id, err)
	}
	return &reg, nil
}

// TestRegistry checks the stored credentials against the registry API.
func (e *Engine) TestRegistry(ctx context.Context, reg store.Registry) error {
	return registry.Ping(ctx, reg, e.box.MustDecrypt(reg.Password))
}

// dockerLogin authenticates the server's Docker daemon against reg. Credentials
// are passed on stdin so they never appear in process lists or logs.
func (e *Engine) dockerLogin(ctx context.Context, x executor.Executor, reg store.Registry, log *logger) error {
	user, pass, err := registry.Credentials(ctx, reg, e.box.MustDecrypt(reg.Password))
	if err != nil {
		return err
	}
	if user == "" && pass == "" {
		return nil // anonymous registry
	}
	if log != nil {
		log.Mask(pass)
		log.Printf("$ docker login %s -u %s", reg.Host, user)
	}
	var out strings.Builder
	cmd := fmt.Sprintf("docker login %s -u %s --password-stdin", q(reg.Host), q(user))
	if err := x.Run(ctx, cmd, executor.RunOpts{Stdin: strings.NewReader(pass), Stdout: &out}); err != nil {
		msg := strings.ReplaceAll(strings.TrimSpace(out.String()), pass, "********")
		return fmt.Errorf("docker login %s failed: %s", reg.Host, truncateStr(msg, 300))
	}
	return nil
}

func truncateStr(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// transferImage streams an image from one server to another (docker save | docker load)
// when no registry is configured.
func transferImage(ctx context.Context, from, to executor.Executor, image string, log *logger) error {
	pr, pw := io.Pipe()
	errc := make(chan error, 1)
	var saveErr strings.Builder
	go func() {
		err := from.Run(ctx, "docker save "+q(image)+" | gzip -1", executor.RunOpts{Stdout: pw, Stderr: &saveErr})
		if err != nil {
			err = fmt.Errorf("docker save: %v %s", err, strings.TrimSpace(saveErr.String()))
		}
		pw.CloseWithError(err)
		errc <- err
	}()
	counter := &countingReader{r: pr}
	start := time.Now()
	var loadOut strings.Builder
	loadErr := to.Run(ctx, "gunzip | docker load", executor.RunOpts{Stdin: counter, Stdout: &loadOut})
	_ = pr.CloseWithError(io.ErrClosedPipe)
	if err := <-errc; err != nil {
		return err
	}
	if loadErr != nil {
		return fmt.Errorf("docker load: %v %s", loadErr, strings.TrimSpace(loadOut.String()))
	}
	log.Printf("Transferred %s (%.1f MB compressed) in %s.", image, float64(counter.n)/1e6, time.Since(start).Round(time.Second))
	return nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func hasImage(ctx context.Context, x executor.Executor, image string) bool {
	_, err := executor.Output(ctx, x, "docker image inspect "+q(image)+" >/dev/null 2>&1")
	return err == nil
}

// distribute makes image available on every target: pull from the registry,
// pull a public/private source image, or stream it from the build server.
func (j *job) distribute(ctx context.Context, image string, build target, targets []target, pull bool) error {
	loggedIn := map[int64]bool{}
	login := func(t target) error {
		if j.reg == nil || loggedIn[t.srv.ID] {
			return nil
		}
		if err := j.e.dockerLogin(ctx, t.x, *j.reg, j.log); err != nil {
			return err
		}
		loggedIn[t.srv.ID] = true
		return nil
	}
	for _, t := range targets {
		switch {
		case pull:
			j.log.Printf("[%s] pulling %s", t.srv.Name, image)
			if err := login(t); err != nil {
				return fmt.Errorf("%s: %w", t.srv.Name, err)
			}
			if err := t.x.Run(ctx, "docker pull "+q(image), executor.RunOpts{Stdout: j.log}); err != nil {
				return fmt.Errorf("%s: pull image: %w", t.srv.Name, err)
			}
		case t.srv.ID == build.srv.ID || hasImage(ctx, t.x, image):
			// already present
		case j.reg != nil && strings.HasPrefix(image, j.reg.Host+"/"):
			j.log.Printf("[%s] pulling %s from %s", t.srv.Name, image, j.reg.Name)
			if err := login(t); err != nil {
				return fmt.Errorf("%s: %w", t.srv.Name, err)
			}
			if err := t.x.Run(ctx, "docker pull -q "+q(image), executor.RunOpts{Stdout: j.log}); err != nil {
				return fmt.Errorf("%s: pull image: %w", t.srv.Name, err)
			}
		default:
			if !hasImage(ctx, build.x, image) {
				return fmt.Errorf("image %s is no longer available on %s", image, build.srv.Name)
			}
			j.log.Printf("[%s] no registry configured — streaming the image from %s", t.srv.Name, build.srv.Name)
			if err := transferImage(ctx, build.x, t.x, image, j.log); err != nil {
				return fmt.Errorf("%s: %w", t.srv.Name, err)
			}
		}
	}
	return nil
}

// push uploads a freshly built image (and a moving :latest tag) to the registry.
func (j *job) push(ctx context.Context, image string) error {
	if j.reg == nil {
		return nil
	}
	j.log.Step("Pushing to " + j.reg.Name)
	if err := j.e.dockerLogin(ctx, j.x, *j.reg, j.log); err != nil {
		return err
	}
	if err := j.run(ctx, "docker push "+q(image)); err != nil {
		return fmt.Errorf("push to %s failed: %w", j.reg.Host, err)
	}
	latest := j.imageRepo() + ":latest"
	if err := j.run(ctx, fmt.Sprintf("docker tag %s %s && docker push -q %s", q(image), q(latest), q(latest))); err != nil {
		j.log.Printf("⚠ could not push %s: %v", latest, err)
	}
	return nil
}

// ---- multi-server resource operations ----

// listAppContainers gathers containers of app on all its servers.
func (e *Engine) listAppContainers(ctx context.Context, app store.Application, all bool, filters ...string) ([]docker.Container, error) {
	var out []docker.Container
	ids := e.AppServerIDs(ctx, app)
	err := e.eachTarget(ctx, app, func(t target) error {
		cs, err := docker.PS(ctx, t.x, all, filters...)
		if err != nil {
			return err
		}
		for i := range cs {
			if len(ids) > 1 {
				cs[i].ServerID, cs[i].ServerName = t.srv.ID, t.srv.Name
			} else {
				cs[i].ServerID = t.srv.ID
			}
		}
		out = append(out, cs...)
		return nil
	})
	return out, err
}
