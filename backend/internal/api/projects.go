package api

import (
	"net/http"
	"strings"

	"github.com/shipyard/shipyard/internal/store"
)

type projectDTO struct {
	store.Project
	Environments []store.Environment `json:"environments"`
	Resources    int                 `json:"resources"`
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	ps, err := store.Select[store.Project](ctx, s.db, "SELECT * FROM projects ORDER BY name")
	if err != nil {
		return err
	}
	envs, err := store.Select[store.Environment](ctx, s.db, "SELECT * FROM environments ORDER BY id")
	if err != nil {
		return err
	}
	type cnt struct {
		ProjectID int64 `db:"project_id"`
		N         int   `db:"n"`
	}
	counts, err := store.Select[cnt](ctx, s.db, `SELECT e.project_id AS project_id, COUNT(*) AS n FROM (
		SELECT environment_id FROM applications UNION ALL SELECT environment_id FROM databases UNION ALL SELECT environment_id FROM services
	) x JOIN environments e ON e.id = x.environment_id GROUP BY e.project_id`)
	if err != nil {
		return err
	}
	byProject := map[int64][]store.Environment{}
	for _, e := range envs {
		byProject[e.ProjectID] = append(byProject[e.ProjectID], e)
	}
	nres := map[int64]int{}
	for _, c := range counts {
		nres[c.ProjectID] = c.N
	}
	out := make([]projectDTO, 0, len(ps))
	for _, p := range ps {
		e := byProject[p.ID]
		if e == nil {
			e = []store.Environment{}
		}
		out = append(out, projectDTO{Project: p, Environments: e, Resources: nres[p.ID]})
	}
	return ok(w, out)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) error {
	var p store.Project
	if err := readJSON(r, &p); err != nil {
		return err
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return badRequest("name is required")
	}
	p.ID, p.CreatedAt = 0, store.Now()
	if _, err := store.Insert(r.Context(), s.db, "projects", &p); err != nil {
		return err
	}
	env := store.Environment{ProjectID: p.ID, Name: "production", CreatedAt: store.Now()}
	if _, err := store.Insert(r.Context(), s.db, "environments", &env); err != nil {
		return err
	}
	s.activity(r, "project.create", p.Name)
	return ok(w, projectDTO{Project: p, Environments: []store.Environment{env}})
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	p, err := store.ByID[store.Project](r.Context(), s.db, "projects", id)
	if err != nil {
		return err
	}
	envs, err := store.Select[store.Environment](r.Context(), s.db, "SELECT * FROM environments WHERE project_id = ? ORDER BY id", id)
	if err != nil {
		return err
	}
	return ok(w, projectDTO{Project: p, Environments: envs})
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	p, err := store.ByID[store.Project](r.Context(), s.db, "projects", id)
	if err != nil {
		return err
	}
	if err := readJSON(r, &p); err != nil {
		return err
	}
	p.ID = id
	if strings.TrimSpace(p.Name) == "" {
		return badRequest("name is required")
	}
	if err := store.Update(r.Context(), s.db, "projects", &p); err != nil {
		return err
	}
	return ok(w, p)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var n int
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM (
		SELECT environment_id FROM applications UNION ALL SELECT environment_id FROM databases UNION ALL SELECT environment_id FROM services
	) x JOIN environments e ON e.id = x.environment_id WHERE e.project_id = ?`, id).Scan(&n)
	if n > 0 {
		return badRequest("project still contains %d resource(s); delete them first", n)
	}
	if err := store.Delete(r.Context(), s.db, "projects", id); err != nil {
		return err
	}
	s.activity(r, "project.delete", "")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) createEnvironment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var e store.Environment
	if err := readJSON(r, &e); err != nil {
		return err
	}
	e.Name = strings.ToLower(strings.TrimSpace(e.Name))
	if e.Name == "" {
		return badRequest("name is required")
	}
	e.ID, e.ProjectID, e.CreatedAt = 0, id, store.Now()
	if _, err := store.Insert(r.Context(), s.db, "environments", &e); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return badRequest("environment %q already exists", e.Name)
		}
		return err
	}
	return ok(w, e)
}

func (s *Server) deleteEnvironment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var n int
	_ = s.db.QueryRowContext(r.Context(), `SELECT (SELECT COUNT(*) FROM applications WHERE environment_id = ?1) +
		(SELECT COUNT(*) FROM databases WHERE environment_id = ?1) + (SELECT COUNT(*) FROM services WHERE environment_id = ?1)`, id).Scan(&n)
	if n > 0 {
		return badRequest("environment still contains %d resource(s)", n)
	}
	if err := store.Delete(r.Context(), s.db, "environments", id); err != nil {
		return err
	}
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) environmentResources(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	env, err := store.ByID[store.Environment](ctx, s.db, "environments", id)
	if err != nil {
		return err
	}
	apps, err := store.Select[store.Application](ctx, s.db, "SELECT * FROM applications WHERE environment_id = ? ORDER BY name", id)
	if err != nil {
		return err
	}
	dbs, err := store.Select[store.Database](ctx, s.db, "SELECT * FROM databases WHERE environment_id = ? ORDER BY name", id)
	if err != nil {
		return err
	}
	svcs, err := store.Select[store.Service](ctx, s.db, "SELECT * FROM services WHERE environment_id = ? ORDER BY name", id)
	if err != nil {
		return err
	}
	for i := range svcs {
		svcs[i].Compose = ""
	}
	return ok(w, map[string]any{"environment": env, "applications": apps, "databases": dbs, "services": svcs})
}
