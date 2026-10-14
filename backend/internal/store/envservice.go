package store

import (
	"context"
	"database/sql"
	"fmt"
)

// migrateEnvService adds env_vars.service (the Docker Compose service a variable
// belongs to, "" = the whole application). The unique key must include the
// service so two services can both define e.g. PORT, which needs a table rebuild
// in SQLite; it runs once, on databases created before the column existed.
func migrateEnvService(ctx context.Context, db *sql.DB) error {
	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_table_info('env_vars') WHERE name = 'service'").Scan(&n); err != nil || n > 0 {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`CREATE TABLE env_vars_new (
  id          INTEGER PRIMARY KEY,
  owner_type  TEXT NOT NULL,
  owner_id    INTEGER NOT NULL,
  service     TEXT NOT NULL DEFAULT '',
  key         TEXT NOT NULL,
  value       TEXT NOT NULL DEFAULT '',
  is_build    INTEGER NOT NULL DEFAULT 0,
  is_preview  INTEGER NOT NULL DEFAULT 0,
  is_secret   INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT '',
  ref_store_id INTEGER NOT NULL DEFAULT 0,
  ref_path    TEXT NOT NULL DEFAULT '',
  ref_key     TEXT NOT NULL DEFAULT '',
  UNIQUE(owner_type, owner_id, service, key, is_preview)
)`,
		`INSERT INTO env_vars_new (id, owner_type, owner_id, key, value, is_build, is_preview, is_secret, created_at)
  SELECT id, owner_type, owner_id, key, value, is_build, is_preview, is_secret, created_at FROM env_vars`,
		`DROP TABLE env_vars`,
		`ALTER TABLE env_vars_new RENAME TO env_vars`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("add env_vars.service: %w", err)
		}
	}
	return tx.Commit()
}
