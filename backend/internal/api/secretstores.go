package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/secretstore"
	"github.com/portdock/portdock/internal/store"
)

// External secret stores (Settings → Secret managers): HashiCorp Vault and
// Azure Key Vault. Administrators manage them; credentials are stored
// encrypted and never returned. Variables reference a secret by store, path
// and key, and deployments read the value when they start.

var storeNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type secretStoreInput struct {
	ID            int64   `json:"id"` // test: the saved store the input changes (0: a new one)
	Name          *string `json:"name"`
	Type          *string `json:"type"`
	URL           *string `json:"url"`
	Auth          *string `json:"auth"`
	Token         *string `json:"token"` // empty: unchanged
	RoleID        *string `json:"role_id"`
	SecretID      *string `json:"secret_id"` // empty: unchanged
	AuthMount     *string `json:"auth_mount"`
	Namespace     *string `json:"namespace"`
	Mount         *string `json:"mount"`
	KVVersion     *int    `json:"kv_version"`
	TenantID      *string `json:"tenant_id"`
	ClientID      *string `json:"client_id"`
	ClientSecret  *string `json:"client_secret"` // empty: unchanged
	AuthorityHost *string `json:"authority_host"`
	CACert        *string `json:"ca_cert"`
	PathPrefix    *string `json:"path_prefix"`
	ProjectIDs    []int64 `json:"project_ids"`
	// TestPath is also read by a test, which returns its key names.
	TestPath string `json:"test_path"`
}

type secretStoreOut struct {
	store.SecretStore
	Auth            string  `json:"auth"`
	TokenSet        bool    `json:"token_set"`
	RoleID          string  `json:"role_id"`
	SecretIDSet     bool    `json:"secret_id_set"`
	AuthMount       string  `json:"auth_mount"`
	Namespace       string  `json:"namespace"`
	Mount           string  `json:"mount"`
	KVVersion       int     `json:"kv_version"`
	TenantID        string  `json:"tenant_id"`
	ClientID        string  `json:"client_id"`
	ClientSecretSet bool    `json:"client_secret_set"`
	AuthorityHost   string  `json:"authority_host"`
	CACert          string  `json:"ca_cert"`
	ProjectIDs      []int64 `json:"project_ids"`
	UsedBy          int     `json:"used_by"` // variables that reference it
}

func (s *Server) secretStoreOut(ctx context.Context, st store.SecretStore) secretStoreOut {
	c, _ := s.engine.SecretStoreConfig(st)
	out := secretStoreOut{SecretStore: st, Auth: c.Auth, TokenSet: c.Token != "", RoleID: c.RoleID, SecretIDSet: c.SecretID != "",
		AuthMount: c.AuthMount, Namespace: c.Namespace, Mount: c.Mount, KVVersion: c.KVVersion, TenantID: c.TenantID, ClientID: c.ClientID,
		ClientSecretSet: c.ClientSecret != "", AuthorityHost: c.AuthorityHost, CACert: c.CACert, ProjectIDs: st.Projects()}
	if out.ProjectIDs == nil {
		out.ProjectIDs = []int64{}
	}
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM env_vars WHERE ref_store_id = ?", st.ID).Scan(&out.UsedBy)
	return out
}

func (s *Server) listSecretStores(w http.ResponseWriter, r *http.Request) error {
	sts, err := store.Select[store.SecretStore](r.Context(), s.db, "SELECT * FROM secret_stores ORDER BY name")
	if err != nil {
		return err
	}
	out := make([]secretStoreOut, 0, len(sts))
	for _, st := range sts {
		out = append(out, s.secretStoreOut(r.Context(), st))
	}
	return ok(w, out)
}

// secretStoreNames lists the stores for the variable editors of every member:
// names and limits only.
func (s *Server) secretStoreNames(w http.ResponseWriter, r *http.Request) error {
	sts, err := store.Select[store.SecretStore](r.Context(), s.db, "SELECT * FROM secret_stores ORDER BY name")
	if err != nil {
		return err
	}
	type name struct {
		ID         int64   `json:"id"`
		Name       string  `json:"name"`
		Type       string  `json:"type"`
		PathPrefix string  `json:"path_prefix"`
		ProjectIDs []int64 `json:"project_ids"`
	}
	out := make([]name, 0, len(sts))
	for _, st := range sts {
		ids := st.Projects()
		if ids == nil {
			ids = []int64{}
		}
		out = append(out, name{st.ID, st.Name, st.Type, st.PathPrefix, ids})
	}
	return ok(w, out)
}

// applySecretStore merges the input into st and its configuration, then checks them.
func (s *Server) applySecretStore(r *http.Request, st *store.SecretStore, in secretStoreInput) error {
	c, err := s.engine.SecretStoreConfig(*st)
	if err != nil {
		return err
	}
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	secret := func(dst *string, v *string) {
		if v != nil && strings.TrimSpace(*v) != "" {
			*dst = strings.TrimSpace(*v)
		}
	}
	if in.Type != nil && strings.TrimSpace(*in.Type) != st.Type {
		// Another kind of store: nothing of the old configuration applies.
		st.Type, c = strings.TrimSpace(*in.Type), secretstore.Config{}
	}
	set(&st.Name, in.Name)
	set(&st.URL, in.URL)
	st.URL = strings.TrimRight(st.URL, "/")
	set(&c.Auth, in.Auth)
	secret(&c.Token, in.Token)
	set(&c.RoleID, in.RoleID)
	secret(&c.SecretID, in.SecretID)
	set(&c.AuthMount, in.AuthMount)
	set(&c.Namespace, in.Namespace)
	set(&c.Mount, in.Mount)
	c.Mount = strings.Trim(c.Mount, "/")
	if in.KVVersion != nil {
		c.KVVersion = *in.KVVersion
	}
	set(&c.TenantID, in.TenantID)
	set(&c.ClientID, in.ClientID)
	secret(&c.ClientSecret, in.ClientSecret)
	set(&c.AuthorityHost, in.AuthorityHost)
	set(&c.CACert, in.CACert)
	if c.Auth == "approle" {
		c.Token = ""
	} else {
		c.RoleID, c.SecretID = "", ""
	}
	if c.Auth == "managed_identity" {
		c.TenantID, c.ClientSecret = "", ""
	}
	if in.PathPrefix != nil {
		st.PathPrefix = strings.Trim(strings.TrimSpace(*in.PathPrefix), "/")
	}
	if in.ProjectIDs != nil {
		ids := make([]string, 0, len(in.ProjectIDs))
		for _, id := range in.ProjectIDs {
			if _, err := store.ByID[store.Project](r.Context(), s.db, "projects", id); err != nil {
				return badRequest("project %d does not exist", id)
			}
			ids = append(ids, strconv.FormatInt(id, 10))
		}
		st.ProjectIDs = strings.Join(ids, ",")
	}
	if !storeNameRe.MatchString(st.Name) {
		return badRequest("the name must be lowercase letters, digits and dashes (it is used in references, e.g. vault-prod)")
	}
	c.Defaults(st.Type)
	if err := secretstore.Validate(st.Type, st.URL, c); err != nil {
		return badRequest("%v", err)
	}
	if st.PathPrefix != "" {
		if err := secretstore.ValidPath(st.Type, st.PathPrefix); err != nil {
			return badRequest("path prefix: %v", err)
		}
	}
	b, _ := json.Marshal(c)
	st.Config = s.box.Encrypt(string(b))
	return nil
}

func (s *Server) createSecretStore(w http.ResponseWriter, r *http.Request) error {
	var in secretStoreInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	st := store.SecretStore{CreatedAt: store.Now()}
	if err := s.applySecretStore(r, &st, in); err != nil {
		return err
	}
	if _, err := store.Insert(r.Context(), s.db, "secret_stores", &st); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return badRequest("a secret store named %s already exists", st.Name)
		}
		return err
	}
	s.activity(r, "secret_store.create", st.Name+" ("+st.Type+")")
	return ok(w, s.secretStoreOut(r.Context(), st))
}

func (s *Server) updateSecretStore(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	st, err := store.ByID[store.SecretStore](r.Context(), s.db, "secret_stores", id)
	if err != nil {
		return err
	}
	var in secretStoreInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.Type != nil && *in.Type != st.Type && s.secretStoreOut(r.Context(), st).UsedBy > 0 {
		return badRequest("variables reference this store: its type cannot change")
	}
	if err := s.applySecretStore(r, &st, in); err != nil {
		return err
	}
	if err := store.Update(r.Context(), s.db, "secret_stores", &st); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return badRequest("a secret store named %s already exists", st.Name)
		}
		return err
	}
	s.activity(r, "secret_store.update", st.Name)
	return ok(w, s.secretStoreOut(r.Context(), st))
}

func (s *Server) deleteSecretStore(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	st, err := store.ByID[store.SecretStore](r.Context(), s.db, "secret_stores", id)
	if err != nil {
		return err
	}
	var keys []string
	rows, err := s.db.QueryContext(r.Context(), "SELECT key FROM env_vars WHERE ref_store_id = ? ORDER BY key LIMIT 6", id)
	if err == nil {
		for rows.Next() {
			var k string
			_ = rows.Scan(&k)
			keys = append(keys, k)
		}
		rows.Close()
	}
	if len(keys) > 0 {
		if len(keys) > 5 {
			keys = append(keys[:5], "…")
		}
		return badRequest("variables reference this store (%s): remove them or point them elsewhere first", strings.Join(keys, ", "))
	}
	if err := store.Delete(r.Context(), s.db, "secret_stores", id); err != nil {
		return err
	}
	s.activity(r, "secret_store.delete", st.Name)
	return ok(w, map[string]bool{"ok": true})
}

// testSecretStore signs in to a store, saved or as edited, and optionally
// reads a path. A saved store keeps the result.
func (s *Server) testSecretStore(w http.ResponseWriter, r *http.Request) error {
	var in secretStoreInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	st := store.SecretStore{}
	if in.ID == 0 && (in.Name == nil || strings.TrimSpace(*in.Name) == "") {
		name := "new-store" // a store being created may have no name yet
		in.Name = &name
	}
	if in.ID > 0 {
		var err error
		if st, err = store.ByID[store.SecretStore](r.Context(), s.db, "secret_stores", in.ID); err != nil {
			return err
		}
	}
	if err := s.applySecretStore(r, &st, in); err != nil {
		return err
	}
	c, err := s.engine.SecretStoreClient(st)
	if err != nil {
		return badRequest("%v", err)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res := map[string]any{"ok": true}
	err = c.Check(ctx)
	if err == nil && in.TestPath != "" {
		path := strings.Trim(strings.TrimSpace(in.TestPath), "/")
		if !st.AllowsPath(path) {
			err = fmt.Errorf("%s is outside the path prefix %s", path, st.PathPrefix)
		} else {
			var vals map[string]string
			if vals, err = c.Read(ctx, path); err == nil {
				res["keys"] = secretstore.Keys(vals)
			}
		}
	}
	if err != nil {
		res["ok"], res["error"] = false, err.Error()
	}
	if in.ID > 0 {
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		_, _ = s.db.ExecContext(r.Context(), "UPDATE secret_stores SET checked_at = ?, check_error = ? WHERE id = ?", store.Now(), msg, in.ID)
	}
	return ok(w, res)
}

// validRef checks the secret store reference of a variable of owner typ/id.
func (s *Server) validRef(ctx context.Context, typ string, id int64, v *store.EnvVar) error {
	if typ == "database" {
		return badRequest("database variables cannot come from a secret store")
	}
	st, err := store.ByID[store.SecretStore](ctx, s.db, "secret_stores", v.RefStoreID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return badRequest("unknown secret store")
		}
		return err
	}
	v.RefPath = strings.Trim(strings.TrimSpace(v.RefPath), "/")
	v.RefKey = strings.TrimSpace(v.RefKey)
	if err := secretstore.ValidPath(st.Type, v.RefPath); err != nil {
		return badRequest("%v", err)
	}
	if st.Type == secretstore.Vault && v.RefKey == "" {
		return badRequest("name the key of the Vault secret")
	}
	if len(v.RefKey) > 256 || strings.ContainsAny(v.RefKey, "\x00\n\r") {
		return badRequest("invalid key")
	}
	project := int64(0)
	if typ != "organization" {
		if project, err = s.engine.ProjectOf(ctx, typ, id); err != nil {
			return err
		}
	}
	if !st.AllowsProject(project) {
		if project == 0 {
			return badRequest("the secret store %s is limited to some projects: organization variables cannot use it", st.Name)
		}
		return badRequest("the secret store %s is not available to this project", st.Name)
	}
	if !st.AllowsPath(v.RefPath) {
		return badRequest("the secret store %s only allows paths under %s/", st.Name, st.PathPrefix)
	}
	return nil
}

// checkSecretRef reads a reference for the variable editor: whether it
// resolves, and the key names at its path (never the values).
func (s *Server) checkSecretRef(w http.ResponseWriter, r *http.Request) error {
	typ, id, err := s.envOwner(r)
	if err != nil {
		return err
	}
	var v store.EnvVar
	if err := readJSON(r, &v); err != nil {
		return err
	}
	if v.RefStoreID == 0 {
		return badRequest("choose a secret store")
	}
	project := int64(0)
	if typ != "organization" {
		if project, err = s.engine.ProjectOf(r.Context(), typ, id); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	keys, err := s.engine.CheckSecretRef(ctx, project, v.RefStoreID, strings.Trim(strings.TrimSpace(v.RefPath), "/"), strings.TrimSpace(v.RefKey))
	if keys == nil {
		keys = []string{}
	}
	if err != nil {
		return ok(w, map[string]any{"ok": false, "error": err.Error(), "keys": keys})
	}
	return ok(w, map[string]any{"ok": true, "keys": keys})
}

// storeNames maps each secret store to its name.
func (s *Server) storeNames(ctx context.Context) map[int64]string {
	names := map[int64]string{}
	sts, _ := store.Select[store.SecretStore](ctx, s.db, "SELECT * FROM secret_stores")
	for _, st := range sts {
		names[st.ID] = st.Name
	}
	return names
}

// refLabel names the reference of a variable, e.g. "vault-prod: shop/db#password".
func refLabel(names map[int64]string, v store.EnvVar) string {
	name := names[v.RefStoreID]
	if name == "" {
		name = "(deleted store)"
	}
	label := name + ": " + v.RefPath
	if v.RefKey != "" {
		label += "#" + v.RefKey
	}
	return label
}
