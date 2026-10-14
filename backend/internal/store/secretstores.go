package store

import (
	"slices"
	"strings"
)

// SecretStore is an external secret manager (HashiCorp Vault or Azure Key
// Vault) that environment variables can reference.
type SecretStore struct {
	ID   int64  `db:"id" json:"id"`
	Name string `db:"name" json:"name"`
	// Type is vault or azure_keyvault.
	Type string `db:"type" json:"type"`
	URL  string `db:"url" json:"url"`
	// Config is the encrypted JSON of secretstore.Config (credentials, options).
	Config     string `db:"config" json:"-"`
	ProjectIDs string `db:"project_ids" json:"-"`
	// PathPrefix limits the paths variables may reference ("" = any).
	PathPrefix string `db:"path_prefix" json:"path_prefix"`
	CheckedAt  string `db:"checked_at" json:"checked_at"`
	CheckError string `db:"check_error" json:"check_error"`
	CreatedAt  string `db:"created_at" json:"created_at"`
}

// Projects returns the projects the store is limited to (none: every project).
func (s SecretStore) Projects() []int64 {
	return LogDrain{ProjectIDs: s.ProjectIDs}.Projects()
}

// AllowsProject reports whether variables of the project may use the store.
// Project 0 (organization variables) needs a store open to every project.
func (s SecretStore) AllowsProject(id int64) bool {
	ids := s.Projects()
	return len(ids) == 0 || id != 0 && slices.Contains(ids, id)
}

// AllowsPath reports whether path is under the store's path prefix.
func (s SecretStore) AllowsPath(path string) bool {
	p := strings.Trim(s.PathPrefix, "/")
	return p == "" || path == p || strings.HasPrefix(path, p+"/")
}
