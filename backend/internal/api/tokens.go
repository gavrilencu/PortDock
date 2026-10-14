package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
)

// API tokens act for a user (a person or a service account) and can be
// limited three ways: a scope caps the permission, a list of projects hides
// every other project, and an expiry date ends them.
//
//	scope    allows                                   project permission
//	read     GET requests                              permRead
//	deploy   + deployments, rollbacks, approvals asks  permDeploy
//	write    + configuration, variables, domains…      permWrite
//	full     everything the user may do                permManage, organization
//
// A token never manages accounts or tokens, whatever its scope.

var tokenScopes = map[string]perm{"read": permRead, "deploy": permDeploy, "write": permWrite, "full": permManage}

// tokenAllows reports whether the request's token (if any) allows p.
func tokenAllows(ctx context.Context, p perm) bool {
	t := auth.TokenFrom(ctx)
	if t == nil {
		return true
	}
	max, ok := tokenScopes[t.Scope]
	return ok && p <= max
}

func tokenDenied(ctx context.Context) error {
	if t := auth.TokenFrom(ctx); t != nil {
		return errStatus(403, "this API token only has the %s scope", t.Scope)
	}
	return errStatus(403, "forbidden")
}

func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// tokenRouteAllowed refuses what no token may do and what its scope forbids
// whatever the route.
func tokenRouteAllowed(r *http.Request, t store.APIToken) error {
	p := r.URL.Path
	if strings.HasPrefix(p, "/api/tokens") || strings.HasPrefix(p, "/api/service-accounts") {
		return errStatus(403, "API tokens cannot manage API tokens or service accounts")
	}
	if strings.HasPrefix(p, "/api/auth/") && !(r.Method == http.MethodGet && (p == "/api/auth/me" || p == "/api/auth/status")) {
		return errStatus(403, "API tokens cannot manage accounts")
	}
	// MCP messages are posted, but each tool calls the API again with the
	// same token, where the scope applies.
	if t.Scope == "read" && unsafeMethod(r.Method) && p != "/api/mcp" {
		return errStatus(403, "this API token only has the read scope")
	}
	return nil
}

// tokenWrite guards the routes without a project guard: those that change
// something need a token with the write scope.
func tokenWrite(h handlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if unsafeMethod(r.Method) && !tokenAllows(r.Context(), permWrite) {
			return tokenDenied(r.Context())
		}
		return h(w, r)
	}
}

type tokenInput struct {
	Name          string  `json:"name"`
	Scope         string  `json:"scope"`
	ProjectIDs    []int64 `json:"project_ids"`
	ExpiresInDays int     `json:"expires_in_days"` // 0 = never
}

// newToken validates in into a token for userID. visible lists the projects
// the token may be limited to.
func newToken(in tokenInput, userID, createdBy int64, visible []int64) (store.APIToken, error) {
	t := store.APIToken{UserID: userID, CreatedBy: createdBy, Name: strings.TrimSpace(in.Name), Scope: in.Scope}
	if t.Name == "" {
		return t, badRequest("name is required")
	}
	if t.Scope == "" {
		t.Scope = "deploy"
	}
	if _, ok := tokenScopes[t.Scope]; !ok {
		return t, badRequest("scope must be read, deploy, write or full")
	}
	ids := make([]string, 0, len(in.ProjectIDs))
	for _, id := range in.ProjectIDs {
		if !slices.Contains(visible, id) {
			return t, badRequest("project #%d not found", id)
		}
		if s := strconv.FormatInt(id, 10); !slices.Contains(ids, s) {
			ids = append(ids, s)
		}
	}
	t.ProjectIDs = strings.Join(ids, ",")
	switch {
	case in.ExpiresInDays < 0 || in.ExpiresInDays > 3650:
		return t, badRequest("the expiry must be between 1 and 3650 days (0 = never)")
	case in.ExpiresInDays > 0:
		t.ExpiresAt = time.Now().UTC().AddDate(0, 0, in.ExpiresInDays).Format(time.RFC3339)
	}
	return t, nil
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) error {
	ts, err := store.Select[store.APIToken](r.Context(), s.db, "SELECT * FROM api_tokens WHERE user_id = ? ORDER BY id DESC", auth.UserFrom(r.Context()).ID)
	if err != nil {
		return err
	}
	return ok(w, ts)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) error {
	var in tokenInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	u := auth.UserFrom(r.Context())
	t, err := newToken(in, u.ID, u.ID, s.visibleProjectIDs(r.Context()))
	if err != nil {
		return err
	}
	plain, t, err := s.auth.CreateToken(r.Context(), t)
	if err != nil {
		return err
	}
	s.activity(r, "token.create", fmt.Sprintf("%s (%s)", t.Name, t.Scope))
	return ok(w, map[string]any{"token": plain, "api_token": t})
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(r.Context(), "DELETE FROM api_tokens WHERE id = ? AND user_id = ?", id, auth.UserFrom(r.Context()).ID)
	if err != nil {
		return err
	}
	s.activity(r, "token.revoke", strconv.FormatInt(id, 10))
	return ok(w, map[string]bool{"ok": true})
}

// ---- service accounts (administrators) ----

type serviceAccountDTO struct {
	store.User
	Tokens []store.APIToken `json:"tokens"`
}

func (s *Server) loadServiceAccount(r *http.Request) (store.User, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.User{}, err
	}
	u, err := store.ByID[store.User](r.Context(), s.db, "users", id)
	if err != nil || !u.ServiceAccount {
		return u, errStatus(404, "service account not found")
	}
	return u, nil
}

func (s *Server) listServiceAccounts(w http.ResponseWriter, r *http.Request) error {
	users, err := store.Select[store.User](r.Context(), s.db, "SELECT * FROM users WHERE service_account = 1 ORDER BY name")
	if err != nil {
		return err
	}
	out := make([]serviceAccountDTO, 0, len(users))
	for _, u := range users {
		ts, _ := store.Select[store.APIToken](r.Context(), s.db, "SELECT * FROM api_tokens WHERE user_id = ? ORDER BY id DESC", u.ID)
		out = append(out, serviceAccountDTO{User: u, Tokens: ts})
	}
	return ok(w, out)
}

type serviceAccountInput struct {
	Name     *string `json:"name"`
	Role     *string `json:"role"`
	Disabled *bool   `json:"disabled"`
}

func (in serviceAccountInput) apply(u *store.User) error {
	if in.Name != nil {
		u.Name = strings.TrimSpace(*in.Name)
	}
	if u.Name == "" {
		return badRequest("name is required")
	}
	if in.Role != nil {
		if *in.Role != "member" && *in.Role != "admin" {
			return badRequest("the role of a service account is member or admin")
		}
		u.Role = *in.Role
	}
	if in.Disabled != nil {
		u.Disabled = *in.Disabled
	}
	return nil
}

func (s *Server) createServiceAccount(w http.ResponseWriter, r *http.Request) error {
	var in serviceAccountInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	// A unique address that no identity provider or directory can claim.
	u := store.User{Role: "member", ServiceAccount: true, CreatedAt: store.Now(),
		Email: "sa-" + secrets.RandomToken(6) + "@service-account.invalid"}
	if err := in.apply(&u); err != nil {
		return err
	}
	if _, err := store.Insert(r.Context(), s.db, "users", &u); err != nil {
		return err
	}
	s.activity(r, "service_account.create", u.Name)
	return ok(w, serviceAccountDTO{User: u, Tokens: []store.APIToken{}})
}

func (s *Server) updateServiceAccount(w http.ResponseWriter, r *http.Request) error {
	u, err := s.loadServiceAccount(r)
	if err != nil {
		return err
	}
	var in serviceAccountInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := in.apply(&u); err != nil {
		return err
	}
	if err := store.Update(r.Context(), s.db, "users", &u); err != nil {
		return err
	}
	s.activity(r, "service_account.update", u.Name)
	return s.listServiceAccounts(w, r)
}

func (s *Server) deleteServiceAccount(w http.ResponseWriter, r *http.Request) error {
	u, err := s.loadServiceAccount(r)
	if err != nil {
		return err
	}
	if err := store.Delete(r.Context(), s.db, "users", u.ID); err != nil {
		return err
	}
	s.activity(r, "service_account.delete", u.Name)
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) createServiceAccountToken(w http.ResponseWriter, r *http.Request) error {
	u, err := s.loadServiceAccount(r)
	if err != nil {
		return err
	}
	var in tokenInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	type row struct {
		ID int64 `db:"id"`
	}
	all, err := store.Select[row](r.Context(), s.db, "SELECT id FROM projects")
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(all))
	for _, p := range all {
		ids = append(ids, p.ID)
	}
	t, err := newToken(in, u.ID, auth.UserFrom(r.Context()).ID, ids)
	if err != nil {
		return err
	}
	plain, t, err := s.auth.CreateToken(r.Context(), t)
	if err != nil {
		return err
	}
	s.activity(r, "token.create", fmt.Sprintf("%s: %s (%s)", u.Name, t.Name, t.Scope))
	return ok(w, map[string]any{"token": plain, "api_token": t})
}

func (s *Server) deleteServiceAccountToken(w http.ResponseWriter, r *http.Request) error {
	u, err := s.loadServiceAccount(r)
	if err != nil {
		return err
	}
	tid, err := pathID(r, "tid")
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(r.Context(), "DELETE FROM api_tokens WHERE id = ? AND user_id = ?", tid, u.ID); err != nil {
		return err
	}
	s.activity(r, "token.revoke", u.Name)
	return ok(w, map[string]bool{"ok": true})
}
