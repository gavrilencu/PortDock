package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/store"
)

type projectDTO struct {
	store.Project
	Environments []store.Environment `json:"environments"`
	Resources    int                 `json:"resources"`
	// Role is the current user's effective role (admin | maintainer | developer | viewer).
	Role string `json:"role"`
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
	u := auth.UserFrom(ctx)
	out := make([]projectDTO, 0, len(ps))
	for _, p := range ps {
		role := s.projectRole(ctx, u, p.ID)
		if roleRank(role) == 0 {
			continue
		}
		e := byProject[p.ID]
		if e == nil {
			e = []store.Environment{}
		}
		out = append(out, projectDTO{Project: p, Environments: e, Resources: nres[p.ID], Role: role})
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
	if !validProjectRole(p.DefaultRole, true) {
		p.DefaultRole = "developer"
	}
	if _, err := store.Insert(r.Context(), s.db, "projects", &p); err != nil {
		return err
	}
	env := store.Environment{ProjectID: p.ID, Name: "production", CreatedAt: store.Now()}
	if _, err := store.Insert(r.Context(), s.db, "environments", &env); err != nil {
		return err
	}
	u := auth.UserFrom(r.Context())
	if !u.IsAdmin() {
		// The creator maintains the project, whatever its default role.
		_, _ = s.db.ExecContext(r.Context(), "INSERT OR REPLACE INTO project_members(project_id, user_id, role, created_at) VALUES(?,?,?,?)", p.ID, u.ID, "maintainer", store.Now())
	}
	s.activity(r, "project.create", p.Name)
	return ok(w, projectDTO{Project: p, Environments: []store.Environment{env}, Role: s.projectRole(r.Context(), u, p.ID)})
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
	return ok(w, projectDTO{Project: p, Environments: envs, Role: s.projectRole(r.Context(), auth.UserFrom(r.Context()), id)})
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
	before := p.DefaultRole
	if err := readJSON(r, &p); err != nil {
		return err
	}
	p.ID = id
	if strings.TrimSpace(p.Name) == "" {
		return badRequest("name is required")
	}
	if !validProjectRole(p.DefaultRole, true) {
		return badRequest("default role must be none, viewer, developer or maintainer")
	}
	if err := store.Update(r.Context(), s.db, "projects", &p); err != nil {
		return err
	}
	if p.DefaultRole != before {
		s.activity(r, "project.default_role", p.Name+": "+before+" → "+p.DefaultRole)
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
	if e.RequireApproval {
		e.Protected = true
	}
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

// updateEnvironment changes protection settings (and the name) of an environment.
func (s *Server) updateEnvironment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	env, err := store.ByID[store.Environment](r.Context(), s.db, "environments", id)
	if err != nil {
		return err
	}
	var in struct {
		Name            *string `json:"name"`
		Protected       *bool   `json:"protected"`
		RequireApproval *bool   `json:"require_approval"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.Name != nil {
		env.Name = strings.ToLower(strings.TrimSpace(*in.Name))
		if env.Name == "" {
			return badRequest("name is required")
		}
	}
	if in.Protected != nil {
		env.Protected = *in.Protected
	}
	if in.RequireApproval != nil {
		env.RequireApproval = *in.RequireApproval
	}
	if env.RequireApproval {
		env.Protected = true // approvals only make sense on protected environments
	}
	if err := store.Update(r.Context(), s.db, "environments", &env); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return badRequest("environment %q already exists", env.Name)
		}
		return err
	}
	s.activity(r, "environment.update", fmt.Sprintf("%s (protected=%v, approvals=%v)", env.Name, env.Protected, env.RequireApproval))
	return ok(w, env)
}

type memberOut struct {
	UserID    int64  `db:"user_id" json:"user_id"`
	Name      string `db:"name" json:"name"`
	Email     string `db:"email" json:"email"`
	UserRole  string `db:"user_role" json:"user_role"` // global role
	Role      string `db:"role" json:"role"`           // project role
	CreatedAt string `db:"created_at" json:"created_at"`
}

func (s *Server) listProjectMembers(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	rows, err := store.Select[memberOut](r.Context(), s.db, `SELECT m.user_id, u.name, u.email, u.role AS user_role, m.role, m.created_at
		FROM project_members m JOIN users u ON u.id = m.user_id WHERE m.project_id = ? ORDER BY u.name`, id)
	if err != nil {
		return err
	}
	p, err := store.ByID[store.Project](r.Context(), s.db, "projects", id)
	if err != nil {
		return err
	}
	if p.DefaultRole == "" {
		p.DefaultRole = "developer"
	}
	return ok(w, map[string]any{"members": rows, "default_role": p.DefaultRole, "roles": projectRoles})
}

// setProjectMembers replaces the explicit memberships of a project.
func (s *Server) setProjectMembers(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Members []struct {
			UserID int64  `json:"user_id"`
			Role   string `json:"role"`
		} `json:"members"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	me := auth.UserFrom(ctx)
	keepsManage := me.IsAdmin()
	for _, m := range in.Members {
		if !validProjectRole(m.Role, false) {
			return badRequest("role must be viewer, developer or maintainer")
		}
		if _, err := store.ByID[store.User](ctx, s.db, "users", m.UserID); err != nil {
			return badRequest("user #%d not found", m.UserID)
		}
		if m.UserID == me.ID && m.Role == "maintainer" {
			keepsManage = true
		}
	}
	if !keepsManage {
		var def string
		_ = s.db.QueryRowContext(ctx, "SELECT default_role FROM projects WHERE id = ?", id).Scan(&def)
		keepsManage = def == "maintainer"
	}
	if !keepsManage {
		return badRequest("you would remove your own maintainer access; keep yourself as a maintainer")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM project_members WHERE project_id = ?", id); err != nil {
		return err
	}
	for _, m := range in.Members {
		if _, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO project_members(project_id, user_id, role, created_at) VALUES(?,?,?,?)", id, m.UserID, m.Role, store.Now()); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.activity(r, "project.members", fmt.Sprintf("project #%d: %d member(s)", id, len(in.Members)))
	return s.listProjectMembers(w, r)
}


// memberCandidates lists active users a maintainer can add to the project.
func (s *Server) memberCandidates(w http.ResponseWriter, r *http.Request) error {
	type row struct {
		ID    int64  `db:"id" json:"id"`
		Name  string `db:"name" json:"name"`
		Email string `db:"email" json:"email"`
		Role  string `db:"role" json:"role"`
	}
	rows, err := store.Select[row](r.Context(), s.db, "SELECT id, name, email, role FROM users WHERE disabled = 0 ORDER BY name")
	if err != nil {
		return err
	}
	return ok(w, rows)
}
