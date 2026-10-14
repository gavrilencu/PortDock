package api

import (
	"net/http"
	"sort"

	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/store"
)

// Shared variables (organization, project, environment) and the comparison of
// variables between applications, e.g. staging and production.

// guardEnvVar checks p on the owner of the variable {id}, whatever its scope.
func (s *Server) guardEnvVar(p perm, h handlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r, "id")
		if err != nil {
			return err
		}
		v, err := store.ByID[store.EnvVar](r.Context(), s.db, "env_vars", id)
		if err != nil {
			return err
		}
		ctx := r.Context()
		switch v.OwnerType {
		case "organization":
			if !auth.UserFrom(ctx).IsAdmin() {
				return errNoAccess
			}
		case "project":
			role := s.projectRole(ctx, auth.UserFrom(ctx), v.OwnerID)
			if roleRank(role) == 0 {
				return errNoAccess
			}
			if !tokenAllows(ctx, p) {
				return tokenDenied(ctx)
			}
			if !allowedIn(role, store.Environment{}, p) {
				return denied(p, store.Environment{})
			}
		case "environment":
			if _, _, err := s.authorizeEnv(ctx, v.OwnerID, p); err != nil {
				return err
			}
		default:
			envID, err := s.envOf(ctx, "env_var", id)
			if err != nil {
				return err
			}
			if _, _, err := s.authorizeEnv(ctx, envID, p); err != nil {
				return err
			}
		}
		return h(w, r)
	}
}

// inheritedVar is a shared variable an application or service receives.
type inheritedVar struct {
	Key        string `json:"key"`
	Value      string `json:"value"` // masked for secrets
	Scope      string `json:"scope"` // organization | project | environment
	IsSecret   bool   `json:"is_secret"`
	IsBuild    bool   `json:"is_build"`
	IsPreview  bool   `json:"is_preview"`
	Overridden bool   `json:"overridden"` // a variable of the same name is set at a narrower scope
	// Ref names the secret store reference of the variable ("" = a value).
	Ref string `json:"ref"`
}

// inheritedEnvs lists the shared variables of an application or service, by scope.
func (s *Server) inheritedEnvs(w http.ResponseWriter, r *http.Request) error {
	typ, id, err := s.envOwner(r)
	if err != nil {
		return err
	}
	vs, err := s.engine.SharedEnvOf(r.Context(), typ, id)
	if err != nil {
		return err
	}
	own, _ := store.Select[store.EnvVar](r.Context(), s.db, "SELECT * FROM env_vars WHERE owner_type = ? AND owner_id = ? AND service = ''", typ, id)
	set := map[string]bool{}
	for _, v := range own {
		set[v.Key] = true
	}
	rank := map[string]int{"organization": 0, "project": 1, "environment": 2}
	names := s.storeNames(r.Context())
	out := []inheritedVar{}
	for i, v := range vs {
		iv := inheritedVar{Key: v.Key, Scope: v.OwnerType, IsSecret: v.IsSecret, IsBuild: v.IsBuild, IsPreview: v.IsPreview, Overridden: set[v.Key]}
		for _, n := range vs[i+1:] {
			if n.Key == v.Key && n.IsPreview == v.IsPreview && rank[n.OwnerType] > rank[v.OwnerType] {
				iv.Overridden = true
			}
		}
		iv.Value = s.box.MustDecrypt(v.Value)
		if v.IsSecret {
			iv.Value = ""
		}
		if v.RefStoreID > 0 {
			iv.Ref = refLabel(names, v)
		}
		out = append(out, iv)
	}
	return ok(w, out)
}

// envDiff is one variable compared between two applications, as their
// deployments receive it (shared variables included).
type envDiff struct {
	Key    string `json:"key"`
	Status string `json:"status"` // same | different | only_here | only_there
	Here   string `json:"here"`   // values, masked when either side is a secret
	There  string `json:"there"`
	// Where each value comes from: application | environment | project | organization.
	HereFrom  string `json:"here_from"`
	ThereFrom string `json:"there_from"`
	Secret    bool   `json:"secret"`
}

// appEnvMap returns the variables a production deployment of the application
// receives (decrypted), with the scope each one comes from in OwnerType.
func (s *Server) appEnvMap(r *http.Request, appID int64) (map[string]store.EnvVar, error) {
	shared, err := s.engine.SharedEnvOf(r.Context(), "application", appID)
	if err != nil {
		return nil, err
	}
	own, err := store.Select[store.EnvVar](r.Context(), s.db, "SELECT * FROM env_vars WHERE owner_type = 'application' AND owner_id = ? AND service = ''", appID)
	if err != nil {
		return nil, err
	}
	m := map[string]store.EnvVar{}
	names := s.storeNames(r.Context())
	for _, v := range append(shared, own...) {
		if v.IsPreview {
			continue
		}
		v.Value = s.box.MustDecrypt(v.Value)
		if v.RefStoreID > 0 {
			// Compared by reference: the reference is not secret, the value is never read.
			v.Value, v.IsSecret = "↗ "+refLabel(names, v), false
		}
		m[v.Key] = v
	}
	return m, nil
}

// otherApp loads the application to compare with and checks the caller may change it.
func (s *Server) otherApp(r *http.Request, idText string) (store.Application, error) {
	var id int64
	for _, c := range idText {
		if c < '0' || c > '9' {
			return store.Application{}, badRequest("invalid application")
		}
		id = id*10 + int64(c-'0')
	}
	other, err := store.ByID[store.Application](r.Context(), s.db, "applications", id)
	if err != nil {
		return other, err
	}
	if _, _, err := s.authorizeEnv(r.Context(), other.EnvironmentID, permWrite); err != nil {
		return other, err
	}
	return other, nil
}

// compareEnvs compares the variables of an application with another one's.
func (s *Server) compareEnvs(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	other, err := s.otherApp(r, r.URL.Query().Get("with"))
	if err != nil {
		return err
	}
	here, err := s.appEnvMap(r, app.ID)
	if err != nil {
		return err
	}
	there, err := s.appEnvMap(r, other.ID)
	if err != nil {
		return err
	}
	keys := map[string]bool{}
	for k := range here {
		keys[k] = true
	}
	for k := range there {
		keys[k] = true
	}
	out := make([]envDiff, 0, len(keys))
	for k := range keys {
		h, inHere := here[k]
		t, inThere := there[k]
		d := envDiff{Key: k, Here: h.Value, There: t.Value, HereFrom: h.OwnerType, ThereFrom: t.OwnerType, Secret: h.IsSecret || t.IsSecret}
		switch {
		case !inThere:
			d.Status = "only_here"
		case !inHere:
			d.Status = "only_there"
		case h.Value == t.Value:
			d.Status = "same"
		default:
			d.Status = "different"
		}
		if d.Secret {
			d.Here, d.There = "", ""
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return ok(w, map[string]any{"with": map[string]any{"id": other.ID, "name": other.Name}, "variables": out})
}

// promoteEnvs copies variables of an application to another one (e.g. from
// staging to production), creating or replacing them there.
func (s *Server) promoteEnvs(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	var in struct {
		To   string   `json:"to"`
		Keys []string `json:"keys"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	other, err := s.otherApp(r, in.To)
	if err != nil {
		return err
	}
	if other.ID == app.ID {
		return badRequest("choose another application")
	}
	here, err := s.appEnvMap(r, app.ID)
	if err != nil {
		return err
	}
	copied := 0
	for _, k := range in.Keys {
		v, found := here[k]
		if !found {
			return badRequest("%s is not a variable of %s", k, app.Name)
		}
		value := v.Value
		if v.RefStoreID > 0 {
			// The reference is copied; the other project must be allowed the store.
			if err := s.validRef(r.Context(), "application", other.ID, &v); err != nil {
				return err
			}
			value, v.IsSecret = "", true
		}
		if _, err := s.db.ExecContext(r.Context(), `INSERT INTO env_vars(owner_type, owner_id, service, key, value, is_build, is_preview, is_secret, created_at, ref_store_id, ref_path, ref_key)
			VALUES('application', ?, '', ?, ?, ?, 0, ?, ?, ?, ?, ?)
			ON CONFLICT(owner_type, owner_id, service, key, is_preview) DO UPDATE SET value = excluded.value, is_build = excluded.is_build, is_secret = excluded.is_secret,
				ref_store_id = excluded.ref_store_id, ref_path = excluded.ref_path, ref_key = excluded.ref_key`,
			other.ID, k, s.box.Encrypt(value), v.IsBuild, v.IsSecret, store.Now(), v.RefStoreID, v.RefPath, v.RefKey); err != nil {
			return err
		}
		copied++
	}
	s.activity(r, "env.promote", app.Name+" → "+other.Name)
	return ok(w, map[string]int{"copied": copied})
}
