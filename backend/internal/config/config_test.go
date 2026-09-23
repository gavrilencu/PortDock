package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvFallbackAndLegacyDB(t *testing.T) {
	t.Setenv("SHIPYARD_PUBLIC_URL", "https://old.example")
	if Env("PUBLIC_URL", "") != "https://old.example" {
		t.Fatal("legacy variable not honoured")
	}
	t.Setenv("PORTDOCK_PUBLIC_URL", "https://new.example")
	if Env("PUBLIC_URL", "") != "https://new.example" {
		t.Fatal("new variable must win")
	}
	dir := t.TempDir()
	for _, f := range []string{"shipyard.db", "shipyard.db-wal"} {
		_ = os.WriteFile(filepath.Join(dir, f), []byte(f), 0o600)
	}
	c := Config{DataDir: dir}
	if err := c.MigrateLegacyFiles(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "portdock.db-wal")); string(b) != "shipyard.db-wal" {
		t.Fatal("WAL not moved with the database")
	}
	if _, err := os.Stat(filepath.Join(dir, "shipyard.db")); err == nil {
		t.Fatal("legacy database still present")
	}
	// An existing portdock.db is never overwritten.
	_ = os.WriteFile(filepath.Join(dir, "shipyard.db"), []byte("old"), 0o600)
	_ = c.MigrateLegacyFiles()
	if b, _ := os.ReadFile(filepath.Join(dir, "portdock.db")); string(b) != "shipyard.db" {
		t.Fatal("portdock.db overwritten")
	}
}
