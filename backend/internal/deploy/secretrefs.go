package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/portdock/portdock/internal/secretstore"
	"github.com/portdock/portdock/internal/store"
)

// Variables that reference an external secret store (HashiCorp Vault, Azure
// Key Vault) get their value when they are resolved, at the start of each
// deployment or job run: PortDock never stores it.

// SecretStoreConfig decrypts the configuration of a store.
func (e *Engine) SecretStoreConfig(st store.SecretStore) (secretstore.Config, error) {
	var c secretstore.Config
	if st.Config != "" {
		plain, err := e.box.Decrypt(st.Config)
		if err != nil {
			return c, fmt.Errorf("secret store %s: %w", st.Name, err)
		}
		if err := json.Unmarshal([]byte(plain), &c); err != nil {
			return c, fmt.Errorf("secret store %s: %w", st.Name, err)
		}
	}
	c.Defaults(st.Type)
	return c, nil
}

// SecretStoreClient returns the client of a store.
func (e *Engine) SecretStoreClient(st store.SecretStore) (secretstore.Client, error) {
	c, err := e.SecretStoreConfig(st)
	if err != nil {
		return nil, err
	}
	return secretstore.New(st.Type, st.URL, c)
}

// refResolver reads the referenced secrets of one resolution, signing in to
// each store and reading each path once.
type refResolver struct {
	e       *Engine
	project int64
	stores  map[int64]store.SecretStore
	clients map[int64]secretstore.Client
	paths   map[string]map[string]string
}

func (e *Engine) newRefResolver(projectID int64) *refResolver {
	return &refResolver{e: e, project: projectID, stores: map[int64]store.SecretStore{},
		clients: map[int64]secretstore.Client{}, paths: map[string]map[string]string{}}
}

// store loads a store and checks the project may use it for path.
func (r *refResolver) store(ctx context.Context, id int64, path string) (store.SecretStore, error) {
	st, ok := r.stores[id]
	if !ok {
		var err error
		if st, err = store.ByID[store.SecretStore](ctx, r.e.db, "secret_stores", id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return st, errors.New("its secret store no longer exists")
			}
			return st, err
		}
		r.stores[id] = st
	}
	if !st.AllowsProject(r.project) {
		if r.project == 0 {
			return st, fmt.Errorf("the secret store %s is limited to some projects: reference it from a project or environment", st.Name)
		}
		return st, fmt.Errorf("the secret store %s is not available to this project", st.Name)
	}
	if !st.AllowsPath(path) {
		return st, fmt.Errorf("%s is outside the paths the secret store %s allows (%s/…)", path, st.Name, st.PathPrefix)
	}
	return st, nil
}

// values returns the values at a path of a store.
func (r *refResolver) values(ctx context.Context, id int64, path string) (store.SecretStore, map[string]string, error) {
	st, err := r.store(ctx, id, path)
	if err != nil {
		return st, nil, err
	}
	k := strconv.FormatInt(id, 10) + "\x00" + path
	if vals, ok := r.paths[k]; ok {
		return st, vals, nil
	}
	c, ok := r.clients[id]
	if !ok {
		if c, err = r.e.SecretStoreClient(st); err != nil {
			return st, nil, err
		}
		r.clients[id] = c
	}
	vals, err := c.Read(ctx, path)
	if err != nil {
		return st, nil, fmt.Errorf("%s: %w", st.Name, err)
	}
	r.paths[k] = vals
	return st, vals, nil
}

// value returns the value of a variable that references a secret store.
func (r *refResolver) value(ctx context.Context, v store.EnvVar) (string, error) {
	st, vals, err := r.values(ctx, v.RefStoreID, v.RefPath)
	if err != nil {
		return "", err
	}
	val, err := secretstore.Lookup(vals, v.RefKey)
	if err != nil {
		return "", fmt.Errorf("%s: %s: %w", st.Name, v.RefPath, err)
	}
	return val, nil
}

// CheckSecretRef reads a reference for a variable of the project (0: an
// organization variable) and returns the key names at its path, never the
// values.
func (e *Engine) CheckSecretRef(ctx context.Context, projectID, storeID int64, path, key string) ([]string, error) {
	r := e.newRefResolver(projectID)
	st, err := r.store(ctx, storeID, path)
	if err != nil {
		return nil, err
	}
	if err := secretstore.ValidPath(st.Type, path); err != nil {
		return nil, err
	}
	_, vals, err := r.values(ctx, storeID, path)
	if err != nil {
		return nil, err
	}
	keys := secretstore.Keys(vals)
	if _, err := secretstore.Lookup(vals, key); err != nil {
		return keys, err
	}
	return keys, nil
}

// ProjectOf returns the project of an environment variable owner (0 for
// the organization).
func (e *Engine) ProjectOf(ctx context.Context, ownerType string, ownerID int64) (int64, error) {
	q := map[string]string{
		"application": "SELECT e.project_id FROM applications r JOIN environments e ON e.id = r.environment_id WHERE r.id = ?",
		"service":     "SELECT e.project_id FROM services r JOIN environments e ON e.id = r.environment_id WHERE r.id = ?",
		"database":    "SELECT e.project_id FROM databases r JOIN environments e ON e.id = r.environment_id WHERE r.id = ?",
		"environment": "SELECT project_id FROM environments WHERE id = ?",
		"project":     "SELECT id FROM projects WHERE id = ?",
	}[ownerType]
	if q == "" {
		return 0, nil
	}
	var id int64
	err := e.db.QueryRowContext(ctx, q, ownerID).Scan(&id)
	return id, err
}
