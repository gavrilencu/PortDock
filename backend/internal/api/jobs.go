package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/deploy"
	"github.com/portdock/portdock/internal/scheduler"
	"github.com/portdock/portdock/internal/store"
)

// Scheduled jobs of an application (Jobs tab).

type jobDTO struct {
	store.AppJob
	NextRunAt string `json:"next_run_at"`
}

func (s *Server) jobOut(r *http.Request, j store.AppJob) jobDTO {
	d := jobDTO{AppJob: j}
	if j.Enabled {
		if next, err := scheduler.NextRun(j.Schedule, s.db.Setting(r.Context(), "instance_timezone")); err == nil {
			d.NextRunAt = next.UTC().Format(time.RFC3339)
		}
	}
	return d
}

func (s *Server) loadJob(r *http.Request) (store.AppJob, store.Application, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.AppJob{}, store.Application{}, err
	}
	j, err := store.ByID[store.AppJob](r.Context(), s.db, "app_jobs", id)
	if err != nil {
		return j, store.Application{}, err
	}
	a, err := store.ByID[store.Application](r.Context(), s.db, "applications", j.ApplicationID)
	return j, a, err
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	jobs, err := store.Select[store.AppJob](r.Context(), s.db, "SELECT * FROM app_jobs WHERE application_id = ? ORDER BY name", app.ID)
	if err != nil {
		return err
	}
	out := make([]jobDTO, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, s.jobOut(r, j))
	}
	return ok(w, out)
}

type jobInput struct {
	Name     *string `json:"name"`
	Schedule *string `json:"schedule"`
	Command  *string `json:"command"`
	Service  *string `json:"service"`
	Timeout  *int    `json:"timeout"`
	Enabled  *bool   `json:"enabled"`
}

func (in jobInput) apply(j *store.AppJob, app store.Application) error {
	if in.Name != nil {
		j.Name = strings.TrimSpace(*in.Name)
	}
	if in.Schedule != nil {
		j.Schedule = strings.TrimSpace(*in.Schedule)
	}
	if in.Command != nil {
		j.Command = strings.TrimSpace(*in.Command)
	}
	if in.Service != nil {
		j.Service = strings.TrimSpace(*in.Service)
	}
	if in.Timeout != nil {
		j.Timeout = *in.Timeout
	}
	if in.Enabled != nil {
		j.Enabled = *in.Enabled
	}
	switch {
	case j.Name == "" || len(j.Name) > 100:
		return badRequest("the name is required (100 characters at most)")
	case j.Command == "" || len(j.Command) > 8192:
		return badRequest("the command is required (8192 characters at most)")
	case j.Timeout < 10 || j.Timeout > 86400:
		return badRequest("the timeout must be between 10 seconds and 24 hours")
	case j.Service != "" && (app.BuildPack != "compose" || !composeServiceRe.MatchString(j.Service)):
		return badRequest("a service can only be chosen for a Docker Compose application")
	}
	if err := scheduler.Validate(j.Schedule); err != nil {
		return badRequest("invalid schedule %q: use a cron expression (minute hour day month weekday) or @hourly, @daily, @every 30m", j.Schedule)
	}
	return nil
}

func (s *Server) reloadJobs(r *http.Request) {
	if s.sched != nil {
		s.sched.Reload(r.Context())
	}
}

func (s *Server) createJob(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	var in jobInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	j := store.AppJob{ApplicationID: app.ID, Timeout: 3600, Enabled: true, CreatedAt: store.Now()}
	if err := in.apply(&j, app); err != nil {
		return err
	}
	if _, err := store.Insert(r.Context(), s.db, "app_jobs", &j); err != nil {
		return err
	}
	s.reloadJobs(r)
	s.activity(r, "job.create", app.Name+": "+j.Name+" ("+j.Schedule+")")
	return ok(w, s.jobOut(r, j))
}

func (s *Server) updateJob(w http.ResponseWriter, r *http.Request) error {
	j, app, err := s.loadJob(r)
	if err != nil {
		return err
	}
	var in jobInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := in.apply(&j, app); err != nil {
		return err
	}
	if err := store.Update(r.Context(), s.db, "app_jobs", &j); err != nil {
		return err
	}
	s.reloadJobs(r)
	s.activity(r, "job.update", app.Name+": "+j.Name)
	return ok(w, s.jobOut(r, j))
}

func (s *Server) deleteJob(w http.ResponseWriter, r *http.Request) error {
	j, app, err := s.loadJob(r)
	if err != nil {
		return err
	}
	if err := store.Delete(r.Context(), s.db, "app_jobs", j.ID); err != nil {
		return err
	}
	s.reloadJobs(r)
	s.activity(r, "job.delete", app.Name+": "+j.Name)
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) runJobNow(w http.ResponseWriter, r *http.Request) error {
	j, app, err := s.loadJob(r)
	if err != nil {
		return err
	}
	run, err := s.engine.RunJob(r.Context(), j.ID, "manual", auth.UserFrom(r.Context()).ID)
	if errors.Is(err, deploy.ErrJobRunning) {
		return errStatus(409, "%v", err)
	}
	if err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "job.run", app.Name+": "+j.Name)
	return ok(w, run)
}

func (s *Server) listJobRuns(w http.ResponseWriter, r *http.Request) error {
	j, _, err := s.loadJob(r)
	if err != nil {
		return err
	}
	runs, err := store.Select[store.JobRun](r.Context(), s.db, "SELECT "+store.JobRunCols+" FROM job_runs WHERE job_id = ? ORDER BY id DESC LIMIT 50", j.ID)
	if err != nil {
		return err
	}
	return ok(w, runs)
}

func (s *Server) getJobRun(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	run, err := store.ByID[store.JobRun](r.Context(), s.db, "job_runs", id)
	if err != nil {
		return err
	}
	return ok(w, run)
}
