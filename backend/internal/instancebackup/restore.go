package instancebackup

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/portdock/portdock/internal/secrets"
)

// A restore cannot replace the database under the running process (SQLite in
// WAL mode keeps it open), so it happens in two steps: Stage decrypts and
// checks the backup into <data>/restore/pending, then PortDock restarts and
// ApplyPending moves the files into place before the database is opened.

func restoreDir(dataDir string) string { return filepath.Join(dataDir, "restore") }
func pendingDir(dataDir string) string { return filepath.Join(restoreDir(dataDir), "pending") }

// Stage decrypts a backup and prepares it to replace the instance's data at
// the next start. envSecretKey is PORTDOCK_SECRET_KEY: when it is set it wins
// over secret.key, so it must match the key of the backup.
func Stage(r io.Reader, passphrase, dataDir, envSecretKey string) (Manifest, error) {
	work := filepath.Join(restoreDir(dataDir), fmt.Sprintf("staging-%d", time.Now().UnixNano()))
	defer os.RemoveAll(work)
	m, err := Extract(r, passphrase, work)
	if err != nil {
		return m, err
	}
	if envSecretKey != "" {
		kb, _ := os.ReadFile(filepath.Join(work, keyName))
		backupKey, _ := secrets.ParseKeyFile(kb)
		envBox, err := secrets.LoadOrCreate(envSecretKey, "")
		if err != nil || !bytes.Equal(envBox.Key(), backupKey) {
			return m, errors.New("PORTDOCK_SECRET_KEY is set and is not the key of this backup: remove it (the restored secret.key is used) or set it to the old instance's value")
		}
	}
	pending := pendingDir(dataDir)
	if err := os.RemoveAll(pending); err != nil {
		return m, err
	}
	if err := os.Rename(work, pending); err != nil {
		return m, err
	}
	return m, nil
}

// HasPending reports whether a staged restore waits for the next start.
func HasPending(dataDir string) bool {
	_, err := os.Stat(filepath.Join(pendingDir(dataDir), manifestName))
	return err == nil
}

// ApplyPending replaces the database and the key with a staged backup. It runs
// at startup, before the database is opened. The replaced files are kept in
// <data>/restore/replaced-<time>.
func ApplyPending(dataDir, dbPath string) (bool, error) {
	if !HasPending(dataDir) {
		return false, nil
	}
	pending := pendingDir(dataDir)
	keep := filepath.Join(restoreDir(dataDir), "replaced-"+time.Now().UTC().Format("20060102-150405"))
	if err := os.MkdirAll(keep, 0o700); err != nil {
		return false, err
	}
	keyPath := filepath.Join(dataDir, keyName)
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm", keyPath} {
		if _, err := os.Stat(p); err == nil {
			if err := os.Rename(p, filepath.Join(keep, filepath.Base(p))); err != nil {
				return false, fmt.Errorf("set aside %s: %w", p, err)
			}
		}
	}
	if err := os.Rename(filepath.Join(pending, dbName), dbPath); err != nil {
		return false, err
	}
	if err := os.Rename(filepath.Join(pending, keyName), keyPath); err != nil {
		return false, err
	}
	_ = os.Chmod(keyPath, 0o600)
	_ = os.Rename(filepath.Join(pending, manifestName), filepath.Join(keep, "restored-"+manifestName))
	return true, os.RemoveAll(pending)
}
