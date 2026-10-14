package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/store"
)

// Project-level access control.
//
// Owners and admins can do everything. Other users get a role per project:
// their explicit membership, or the project's default role.
//
//	            read  deploy  write        manage
//	viewer       ✓
//	developer    ✓     ✓*     ✓ (unprotected)
//	maintainer   ✓     ✓      ✓            ✓
//
// * deployments to protected environments become approval requests.
type perm int

const (
	permRead   perm = iota + 1 // view resources, logs, deployments, metrics
	permDeploy                 // trigger or cancel deployments
	permWrite                  // change configuration, env vars, domains; start/stop; terminals
	permManage                 // delete resources, manage members and environments, approve deployments
)

var projectRoles = []string{"none", "viewer", "developer", "maintainer"}

func roleRank(role string) int {
	switch role {
	case "viewer":
		return 1
	case "developer":
		return 2
	case "maintainer":
		return 3
	case "admin":
		return 4
	}
	return 0
}

func validProjectRole(r string, allowNone bool) bool {
	return r == "viewer" || r == "developer" || r == "maintainer" || (allowNone && r == "none")
}

// projectRole returns the effective role of u in a project ("none" = no access).
// An API token limited to some projects sees no other project.
func (s *Server) projectRole(ctx context.Context, u store.User, projectID int64) string {
	if t := auth.TokenFrom(ctx); t != nil && u.ID == auth.UserFrom(ctx).ID {
		if ids := t.Projects(); ids != nil && !slices.Contains(ids, projectID) {
			return "none"
		}
		if r := auth.RealRoleFrom(ctx); r == "owner" || r == "admin" {
			return "admin"
		}
	}
	if u.IsAdmin() {
		return "admin"
	}
	var role string
	err := s.db.QueryRowContext(ctx, "SELECT role FROM project_members WHERE project_id = ? AND user_id = ?", projectID, u.ID).Scan(&role)
	if err == nil {
		return role
	}
	if err := s.db.QueryRowContext(ctx, "SELECT default_role FROM projects WHERE id = ?", projectID).Scan(&role); err != nil {
		return "none"
	}
	if role == "" {
		role = "developer"
	}
	return role
}

// allowedIn reports whether role may perform p in env.
func allowedIn(role string, env store.Environment, p perm) bool {
	rank := roleRank(role)
	switch p {
	case permRead:
		return rank >= 1
	case permDeploy:
		return rank >= 2
	case permWrite:
		if env.Protected {
			return rank >= 3
		}
		return rank >= 2
	case permManage:
		return rank >= 3
	}
	return false
}

// Access describes what the current user may do with a resource (sent to the UI).
type Access struct {
	Role                string `json:"role"`
	CanDeploy           bool   `json:"can_deploy"`
	CanWrite            bool   `json:"can_write"`
	CanManage           bool   `json:"can_manage"`
	DeployNeedsApproval bool   `json:"deploy_needs_approval"`
	Protected           bool   `json:"protected"`
}

func accessFor(role string, env store.Environment) Access {
	return Access{
		Role: role, CanDeploy: allowedIn(role, env, permDeploy), CanWrite: allowedIn(role, env, permWrite), CanManage: allowedIn(role, env, permManage),
		DeployNeedsApproval: needsApproval(role, env), Protected: env.Protected,
	}
}

// needsApproval: every deployment in a require_approval environment, and
// non-maintainer deployments in protected environments.
func needsApproval(role string, env store.Environment) bool {
	return env.RequireApproval || (env.Protected && roleRank(role) < 3)
}

var errNoAccess = errStatus(404, "not found")

func denied(p perm, env store.Environment) error {
	switch p {
	case permWrite:
		if env.Protected {
			return errStatus(403, "%s is a protected environment; only project maintainers can change it", env.Name)
		}
		return errStatus(403, "your project role does not allow changes")
	case permDeploy:
		return errStatus(403, "your project role does not allow deployments")
	case permManage:
		return errStatus(403, "project maintainer role required")
	}
	return errNoAccess
}

// authorizeEnv checks p for the current user in an environment.
func (s *Server) authorizeEnv(ctx context.Context, envID int64, p perm) (store.Environment, string, error) {
	env, err := store.ByID[store.Environment](ctx, s.db, "environments", envID)
	if err != nil {
		return env, "", errNoAccess
	}
	role := s.projectRole(ctx, auth.UserFrom(ctx), env.ProjectID)
	if roleRank(role) == 0 {
		return env, role, errNoAccess // hide projects the user can't see
	}
	if !tokenAllows(ctx, p) {
		return env, role, tokenDenied(ctx)
	}
	if !allowedIn(role, env, p) {
		return env, role, denied(p, env)
	}
	return env, role, nil
}

// envOf resolves the environment of a resource.
func (s *Server) envOf(ctx context.Context, kind string, id int64) (int64, error) {
	q := map[string]string{
		"application": "SELECT environment_id FROM applications WHERE id = ?",
		"database":    "SELECT environment_id FROM databases WHERE id = ?",
		"service":     "SELECT environment_id FROM services WHERE id = ?",
		"environment": "SELECT id FROM environments WHERE id = ?",
		"deployment":  "SELECT a.environment_id FROM deployments d JOIN applications a ON a.id = d.application_id WHERE d.id = ?",
		"domain":      "SELECT a.environment_id FROM app_domains x JOIN applications a ON a.id = x.application_id WHERE x.id = ?",
		"volume":      "SELECT a.environment_id FROM app_volumes x JOIN applications a ON a.id = x.application_id WHERE x.id = ?",
		"job":         "SELECT a.environment_id FROM app_jobs x JOIN applications a ON a.id = x.application_id WHERE x.id = ?",
		"job_run":     "SELECT a.environment_id FROM job_runs x JOIN applications a ON a.id = x.application_id WHERE x.id = ?",
		"backup":      "SELECT d.environment_id FROM backups b JOIN databases d ON d.id = b.database_id WHERE b.id = ?",
		"replica":     "SELECT d.environment_id FROM db_replicas x JOIN databases d ON d.id = x.database_id WHERE x.id = ?",
		"backup_run":  "SELECT d.environment_id FROM backup_runs r JOIN backups b ON b.id = r.backup_id JOIN databases d ON d.id = b.database_id WHERE r.id = ?",
		"env_var": `SELECT COALESCE(
			(SELECT environment_id FROM applications WHERE v.owner_type = 'application' AND id = v.owner_id),
			(SELECT environment_id FROM databases WHERE v.owner_type = 'database' AND id = v.owner_id),
			(SELECT environment_id FROM services WHERE v.owner_type = 'service' AND id = v.owner_id)) FROM env_vars v WHERE v.id = ?`,
	}[kind]
	if q == "" {
		return 0, errors.New("unknown resource kind " + kind)
	}
	var env sql.NullInt64
	if err := s.db.QueryRowContext(ctx, q, id).Scan(&env); err != nil || !env.Valid {
		return 0, errNoAccess
	}
	return env.Int64, nil
}

// guard wraps a handler whose {id} path value is a resource of kind.
// kind "owner" reads the resource type from the path (/api/{applications|databases|services}/{id}/...).
func (s *Server) guard(kind string, p perm, h handlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r, "id")
		if err != nil {
			return err
		}
		k := kind
		if k == "owner" {
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			k = map[string]string{"applications": "application", "databases": "database", "services": "service", "environments": "environment"}[parts[1]]
		}
		envID, err := s.envOf(r.Context(), k, id)
		if err != nil {
			return err
		}
		if _, _, err := s.authorizeEnv(r.Context(), envID, p); err != nil {
			return err
		}
		return h(w, r)
	}
}

// guardProject wraps a handler whose {id} path value is a project id.
func (s *Server) guardProject(p perm, h handlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r, "id")
		if err != nil {
			return err
		}
		role := s.projectRole(r.Context(), auth.UserFrom(r.Context()), id)
		if roleRank(role) == 0 {
			return errNoAccess
		}
		if !tokenAllows(r.Context(), p) {
			return tokenDenied(r.Context())
		}
		if !allowedIn(role, store.Environment{}, p) {
			return denied(p, store.Environment{})
		}
		return h(w, r)
	}
}

// requireEnv checks p on an environment given in a request body (create handlers).
func (s *Server) requireEnv(r *http.Request, envID int64, p perm) error {
	_, _, err := s.authorizeEnv(r.Context(), envID, p)
	if errors.Is(err, errNoAccess) {
		return badRequest("environment not found")
	}
	return err
}

// envVisibility returns a predicate for filtering lists by environment.
func (s *Server) envVisibility(ctx context.Context) func(envID int64) bool {
	u := auth.UserFrom(ctx)
	if u.IsAdmin() {
		return func(int64) bool { return true }
	}
	envs, _ := store.Select[store.Environment](ctx, s.db, "SELECT * FROM environments")
	projectOf := map[int64]int64{}
	for _, e := range envs {
		projectOf[e.ID] = e.ProjectID
	}
	roles := map[int64]string{}
	return func(envID int64) bool {
		pid, ok := projectOf[envID]
		if !ok {
			return false
		}
		role, cached := roles[pid]
		if !cached {
			role = s.projectRole(ctx, u, pid)
			roles[pid] = role
		}
		return roleRank(role) > 0
	}
}

func filterBy[T any](items []T, keep func(T) bool) []T {
	out := items[:0]
	for _, it := range items {
		if keep(it) {
			out = append(out, it)
		}
	}
	return out
}

// authorizeContainer lets non-admins act only on containers of resources they
// may access (resolved from the portdock.* labels of the container).
func (s *Server) authorizeContainer(r *http.Request, x executor.Executor, cid string, p perm) error {
	if auth.UserFrom(r.Context()).IsAdmin() {
		return nil
	}
	raw, err := docker.Inspect(r.Context(), x, cid)
	if err != nil {
		return errNoAccess
	}
	var info []struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if json.Unmarshal(raw, &info) != nil || len(info) == 0 {
		return errNoAccess
	}
	labels := docker.NormalizeLabels(info[0].Config.Labels)
	kind, uuid := labels[docker.LabelKind], labels[docker.LabelResource]
	table := map[string]string{"application": "applications", "database": "databases", "service": "services"}[kind]
	if table == "" || uuid == "" {
		return errStatus(403, "only administrators can manage this container")
	}
	var envID int64
	if err := s.db.QueryRowContext(r.Context(), "SELECT environment_id FROM "+table+" WHERE uuid = ?", uuid).Scan(&envID); err != nil {
		return errNoAccess
	}
	_, _, err = s.authorizeEnv(r.Context(), envID, p)
	return err
}

// resourceAccess computes the Access block for a resource DTO.
func (s *Server) resourceAccess(ctx context.Context, envID int64) Access {
	env, _ := store.ByID[store.Environment](ctx, s.db, "environments", envID)
	a := accessFor(s.projectRole(ctx, auth.UserFrom(ctx), env.ProjectID), env)
	a.CanDeploy = a.CanDeploy && tokenAllows(ctx, permDeploy)
	a.CanWrite = a.CanWrite && tokenAllows(ctx, permWrite)
	a.CanManage = a.CanManage && tokenAllows(ctx, permManage)
	return a
}

// visibleProjectIDs lists the projects the current user can see.
func (s *Server) visibleProjectIDs(ctx context.Context) []int64 {
	// The ids are read completely first: with SQLite's single connection, a
	// query issued while a cursor is still open would wait for it forever.
	type row struct {
		ID int64 `db:"id"`
	}
	all, err := store.Select[row](ctx, s.db, "SELECT id FROM projects")
	if err != nil {
		return nil
	}
	u := auth.UserFrom(ctx)
	var ids []int64
	for _, p := range all {
		if roleRank(s.projectRole(ctx, u, p.ID)) > 0 {
			ids = append(ids, p.ID)
		}
	}
	return ids
}
