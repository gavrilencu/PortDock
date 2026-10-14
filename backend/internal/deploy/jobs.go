package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/store"
)

// Scheduled jobs of applications (cron). A job runs on the application's
// primary server, in a new container of its current image with the variables
// and persistent volumes of the application (like the pre-deploy command), or
// inside a running container of a compose service.

// jobOutputLimit caps the output kept per run (the end is kept).
const jobOutputLimit = 64 << 10

// jobRunsKept is how many runs of a job are kept.
const jobRunsKept = 50

// ErrJobRunning means the previous run of the job has not finished.
var ErrJobRunning = errors.New("the previous run of this job is still running")

var runningJobs = struct {
	sync.Mutex
	m map[int64]bool
}{m: map[int64]bool{}}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf.Write(p)
	if over := t.buf.Len() - t.limit; over > 0 {
		t.buf.Next(over)
		t.truncated = true
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.truncated {
		return "[… earlier output truncated]\n" + t.buf.String()
	}
	return t.buf.String()
}

// RecoverJobs marks the runs interrupted by a restart as failed.
func (e *Engine) RecoverJobs(ctx context.Context) {
	_, _ = e.db.ExecContext(ctx, "UPDATE job_runs SET status = 'failed', exit_code = -1, finished_at = ?, output = output || ? WHERE status = 'running'",
		store.Now(), "\n[portdock] Interrupted by a PortDock restart.\n")
}

// RunJob starts a run of a job and returns it; the command runs in the
// background. trigger is schedule or manual.
func (e *Engine) RunJob(ctx context.Context, jobID int64, trigger string, by int64) (store.JobRun, error) {
	j, err := store.ByID[store.AppJob](ctx, e.db, "app_jobs", jobID)
	if err != nil {
		return store.JobRun{}, err
	}
	app, err := store.ByID[store.Application](ctx, e.db, "applications", j.ApplicationID)
	if err != nil {
		return store.JobRun{}, err
	}
	if app.CurrentImage == "" {
		return store.JobRun{}, errors.New("deploy the application first: jobs run with its current image")
	}
	runningJobs.Lock()
	if runningJobs.m[j.ID] {
		runningJobs.Unlock()
		return store.JobRun{}, ErrJobRunning
	}
	runningJobs.m[j.ID] = true
	runningJobs.Unlock()

	run := store.JobRun{JobID: j.ID, ApplicationID: app.ID, Trigger: trigger, Status: "running", StartedAt: store.Now(), StartedBy: by}
	if _, err := store.Insert(ctx, e.db, "job_runs", &run); err != nil {
		runningJobs.Lock()
		delete(runningJobs.m, j.ID)
		runningJobs.Unlock()
		return run, err
	}
	_, _ = e.db.ExecContext(ctx, "UPDATE app_jobs SET last_run_at = ?, last_status = 'running' WHERE id = ?", run.StartedAt, j.ID)
	if e.hub != nil {
		e.hub.Event("job", j.ID, "running")
	}
	go e.executeJob(context.WithoutCancel(ctx), j, app, run)
	return run, nil
}

var exitCodeRe = regexp.MustCompile(`exit status (\d+)`)

func (e *Engine) executeJob(ctx context.Context, j store.AppJob, app store.Application, run store.JobRun) {
	defer func() {
		runningJobs.Lock()
		delete(runningJobs.m, j.ID)
		runningJobs.Unlock()
	}()
	timeout := time.Duration(max(j.Timeout, 10)) * time.Second
	out := &tailBuffer{limit: jobOutputLimit}
	status, code, err := "success", 0, e.jobCommand(ctx, j, app, run, timeout, out)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status, code = "timeout", -1
		fmt.Fprintf(out, "\n[portdock] Stopped after %s (the job's timeout).\n", timeout)
	case err != nil:
		status, code = "failed", -1
		if m := exitCodeRe.FindStringSubmatch(err.Error()); m != nil {
			code, _ = strconv.Atoi(m[1])
		} else {
			fmt.Fprintf(out, "\n[portdock] %v\n", err)
		}
	}
	finished := store.Now()
	_, _ = e.db.ExecContext(ctx, "UPDATE job_runs SET status = ?, exit_code = ?, output = ?, finished_at = ? WHERE id = ?", status, code, out.String(), finished, run.ID)
	_, _ = e.db.ExecContext(ctx, "UPDATE app_jobs SET last_status = ? WHERE id = ?", status, j.ID)
	_, _ = e.db.ExecContext(ctx, "DELETE FROM job_runs WHERE job_id = ? AND id NOT IN (SELECT id FROM job_runs WHERE job_id = ? ORDER BY id DESC LIMIT ?)", j.ID, j.ID, jobRunsKept)
	if e.hub != nil {
		e.hub.Event("job", j.ID, status)
	}
	if status != "success" && e.notify != nil {
		e.notify.Send(notify.Event{
			Name: "job.failed", Title: fmt.Sprintf("Job %s of %s %s", j.Name, app.Name, map[string]string{"failed": "failed", "timeout": "timed out"}[status]),
			Message: fmt.Sprintf("Exit code %d", code),
			URL:     fmt.Sprintf("%s/applications/%d?tab=jobs", e.PublicURL(ctx), app.ID),
		})
	}
	slog.Info("job finished", "app", app.Name, "job", j.Name, "status", status, "exit", code)
}

// jobCommand runs the command of j and streams its output to out.
func (e *Engine) jobCommand(ctx context.Context, j store.AppJob, app store.Application, run store.JobRun, timeout time.Duration, out *tailBuffer) error {
	x, srv, err := e.pool.ForID(ctx, app.ServerID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	opts := executor.RunOpts{Stdout: out, Stderr: out}
	if app.BuildPack == "compose" {
		service := j.Service
		if service == "" {
			service = app.ComposeService
		}
		filters := []string{"label=com.docker.compose.project=" + composeProject(app.UUID)}
		if service != "" {
			filters = append(filters, "label="+composeServiceLabel+"="+service)
		}
		cs, err := docker.PS(ctx, x, false, filters...)
		if err != nil {
			return err
		}
		if len(cs) == 0 {
			return fmt.Errorf("no running container of service %q", service)
		}
		return x.Run(ctx, "docker exec "+q(cs[0].Name)+" sh -c "+q(j.Command), opts)
	}

	jb := &job{e: e, app: app, srv: srv, x: x, d: &store.Deployment{}}
	if jb.env, err = e.ResolveEnv(ctx, "application", app.ID, false); err != nil {
		return err
	}
	jb.net = e.Network(ctx)
	jb.routes, _ = e.DomainRoutes(ctx, app)
	envFile, err := jb.writeEnvFile(ctx)
	if err != nil {
		return err
	}
	mounts, err := jb.volumeArgs(ctx)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%s-job%d-%d", app.UUID, j.ID, run.ID)
	cmd := fmt.Sprintf("docker run --rm --name %s --network %s --env-file %s --label %s --label %s --label %s%s",
		q(name), q(e.cfg.ProxyNetwork), q(envFile),
		q(docker.LabelManaged+"=true"), q(docker.LabelKind+"=job"), q(docker.LabelResource+"="+app.UUID), mounts)
	if app.CPULimit != "" {
		cmd += " --cpus " + q(app.CPULimit)
	}
	if app.MemoryLimit != "" {
		cmd += " --memory " + q(app.MemoryLimit)
	}
	cmd += " --entrypoint sh " + q(app.CurrentImage) + " -c " + q(j.Command)
	err = x.Run(ctx, cmd, opts)
	if ctx.Err() != nil {
		// The client went away, the container keeps running: stop it.
		_ = x.Run(context.Background(), "docker rm -f "+q(name)+" >/dev/null 2>&1", executor.RunOpts{})
		return context.DeadlineExceeded
	}
	return err
}
