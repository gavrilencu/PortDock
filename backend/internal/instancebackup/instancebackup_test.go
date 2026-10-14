package instancebackup

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
)

const pass = "correct horse battery staple"

// oldInstance makes a data directory with a database holding one user.
func oldInstance(t *testing.T) (*store.DB, *secrets.Box, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "portdock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	box, err := secrets.LoadOrCreate("", dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO users(email, name, password_hash, role, created_at) VALUES ('owner@corp.com', 'Owner', 'x', 'owner', '')"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(context.Background(), "s3_secret_key", box.Encrypt("s3-secret")); err != nil {
		t.Fatal(err)
	}
	return db, box, dir
}

func backup(t *testing.T, db *store.DB, box *secrets.Box, dir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(context.Background(), &buf, db, box.Key(), pass, Manifest{Version: "v1.13.1", Kind: "manual"}, dir); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBackupAndRestoreOnNewServer(t *testing.T) {
	db, box, oldDir := oldInstance(t)
	b := backup(t, db, box, oldDir)
	if bytes.Contains(b, []byte("owner@corp.com")) || bytes.Contains(b, secrets.KeyFile(box.Key())) {
		t.Fatal("the backup is not encrypted")
	}

	// A new server: an empty data directory with its own fresh key.
	newDir := t.TempDir()
	dbPath := filepath.Join(newDir, "portdock.db")
	fresh, _ := store.Open(dbPath)
	fresh.Close()
	if _, err := secrets.LoadOrCreate("", newDir); err != nil {
		t.Fatal(err)
	}

	m, err := Stage(bytes.NewReader(b), pass, newDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "v1.13.1" || m.Format != FormatVersion {
		t.Fatalf("manifest %+v", m)
	}
	if !HasPending(newDir) {
		t.Fatal("nothing staged")
	}
	applied, err := ApplyPending(newDir, dbPath)
	if err != nil || !applied {
		t.Fatalf("apply: %v %v", applied, err)
	}
	if HasPending(newDir) {
		t.Fatal("still pending after apply")
	}

	restored, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var email string
	if err := restored.QueryRow("SELECT email FROM users").Scan(&email); err != nil || email != "owner@corp.com" {
		t.Fatalf("restored users: %q %v", email, err)
	}
	newBox, err := secrets.LoadOrCreate("", newDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := newBox.MustDecrypt(restored.Setting(context.Background(), "s3_secret_key")); got != "s3-secret" {
		t.Fatalf("secrets not readable after restore: %q", got)
	}
	replaced, _ := filepath.Glob(filepath.Join(newDir, "restore", "replaced-*", "portdock.db"))
	if len(replaced) != 1 {
		t.Fatalf("the replaced database is not kept: %v", replaced)
	}
}

func TestRestoreRefusesBadInput(t *testing.T) {
	db, box, dir := oldInstance(t)
	b := backup(t, db, box, dir)
	target := t.TempDir()

	if _, err := Stage(bytes.NewReader(b), "wrong passphrase!", target, ""); !errors.Is(err, ErrPassphrase) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	tampered := append([]byte(nil), b...)
	tampered[len(tampered)/2] ^= 0xff
	if _, err := Stage(bytes.NewReader(tampered), pass, target, ""); err == nil {
		t.Fatal("tampered backup accepted")
	}
	if _, err := Stage(strings.NewReader("not a backup"), pass, target, ""); err == nil {
		t.Fatal("garbage accepted")
	}
	// PORTDOCK_SECRET_KEY wins over secret.key: it must be the backup's key.
	other := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	if _, err := Stage(bytes.NewReader(b), pass, target, other); err == nil || !strings.Contains(err.Error(), "PORTDOCK_SECRET_KEY") {
		t.Fatalf("env key mismatch: %v", err)
	}
	same := base64.StdEncoding.EncodeToString(box.Key())
	if _, err := Stage(bytes.NewReader(b), pass, target, same); err != nil {
		t.Fatalf("env key matching the backup: %v", err)
	}
	if err := Write(context.Background(), &bytes.Buffer{}, db, box.Key(), "short", Manifest{}, dir); err == nil {
		t.Fatal("short passphrase accepted")
	}
	if entries, _ := os.ReadDir(filepath.Join(target, "restore")); len(entries) != 1 || entries[0].Name() != "pending" {
		t.Fatalf("staging left behind: %v", entries)
	}
}
