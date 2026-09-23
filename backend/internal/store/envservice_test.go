package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrateEnvService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// env_vars as created before variables could belong to a compose service.
	if _, err := old.Exec(`CREATE TABLE env_vars (
  id INTEGER PRIMARY KEY, owner_type TEXT NOT NULL, owner_id INTEGER NOT NULL, key TEXT NOT NULL,
  value TEXT NOT NULL DEFAULT '', is_build INTEGER NOT NULL DEFAULT 0, is_preview INTEGER NOT NULL DEFAULT 0,
  is_secret INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL DEFAULT '', UNIQUE(owner_type, owner_id, key, is_preview));
INSERT INTO env_vars (owner_type, owner_id, key, value, is_secret) VALUES ('application', 1, 'PORT', 'enc', 1);`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var key, svc string
	var secret bool
	if err := db.QueryRow("SELECT key, service, is_secret FROM env_vars WHERE owner_id = 1").Scan(&key, &svc, &secret); err != nil || key != "PORT" || svc != "" || !secret {
		t.Fatalf("existing variable not kept: %s %q %v %v", key, svc, secret, err)
	}
	// The same key can now exist once per service.
	for _, s := range []string{"frontend", "backend"} {
		if _, err := db.Exec("INSERT INTO env_vars (owner_type, owner_id, service, key) VALUES ('application', 1, ?, 'PORT')", s); err != nil {
			t.Fatalf("PORT for %s: %v", s, err)
		}
	}
	if _, err := db.Exec("INSERT INTO env_vars (owner_type, owner_id, service, key) VALUES ('application', 1, 'backend', 'PORT')"); err == nil {
		t.Fatal("a key must stay unique within one service")
	}
	// Opening again is a no-op.
	db.Close()
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
}
