// Package instancebackup backs up PortDock itself: its database and the key
// that encrypts the secrets in it, so that an instance can be rebuilt on a new
// server after the old one is lost.
//
// A backup is an age file (https://age-encryption.org, scrypt passphrase)
// holding a gzipped tar with manifest.json, portdock.db and secret.key. The
// passphrase is the only thing needed to restore it: through PortDock (the
// first-run page or `portdock -restore`) or by hand with `age -d`.
package instancebackup

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"filippo.io/age"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
)

// FormatVersion is written to the manifest; newer formats are refused.
const FormatVersion = 1

// MinPassphrase is the shortest passphrase accepted for backups.
const MinPassphrase = 12

const (
	manifestName = "manifest.json"
	dbName       = "portdock.db"
	keyName      = "secret.key"
)

// Manifest describes a backup.
type Manifest struct {
	Format    int    `json:"format"`
	Version   string `json:"version"`
	CreatedAt string `json:"created_at"`
	Instance  string `json:"instance"`
	DataDir   string `json:"data_dir"`
	Kind      string `json:"kind"` // scheduled | manual
}

// Write streams an encrypted backup of db and key to w. The database is copied
// with VACUUM INTO into tmpDir first, which gives a consistent snapshot while
// PortDock keeps running.
func Write(ctx context.Context, w io.Writer, db *store.DB, key []byte, passphrase string, m Manifest, tmpDir string) error {
	if len(passphrase) < MinPassphrase {
		return fmt.Errorf("the backup passphrase must be at least %d characters", MinPassphrase)
	}
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return err
	}
	snap := filepath.Join(tmpDir, fmt.Sprintf(".snapshot-%d.db", time.Now().UnixNano()))
	defer os.Remove(snap)
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", snap); err != nil {
		return fmt.Errorf("database snapshot: %w", err)
	}
	f, err := os.Open(snap)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}

	rcpt, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return err
	}
	enc, err := age.Encrypt(w, rcpt)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(enc)
	tw := tar.NewWriter(gz)
	m.Format = FormatVersion
	mb, _ := json.MarshalIndent(m, "", "  ")
	now := time.Now()
	add := func(name string, size int64, r io.Reader) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: size, ModTime: now, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := io.Copy(tw, r)
		return err
	}
	kb := secrets.KeyFile(key)
	for _, step := range []func() error{
		func() error { return add(manifestName, int64(len(mb)), bytes.NewReader(mb)) },
		func() error { return add(dbName, st.Size(), f) },
		func() error { return add(keyName, int64(len(kb)), bytes.NewReader(kb)) },
		tw.Close, gz.Close, enc.Close,
	} {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// ErrPassphrase means the passphrase does not open the backup.
var ErrPassphrase = errors.New("the passphrase does not match this backup")

// Extract decrypts a backup into dir and checks its contents.
func Extract(r io.Reader, passphrase, dir string) (Manifest, error) {
	var m Manifest
	id, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return m, err
	}
	dec, err := age.Decrypt(bufio.NewReader(r), id)
	if err != nil {
		var noMatch *age.NoIdentityMatchError
		if errors.As(err, &noMatch) {
			return m, ErrPassphrase
		}
		return m, fmt.Errorf("not a PortDock instance backup: %w", err)
	}
	gz, err := gzip.NewReader(dec)
	if err != nil {
		return m, fmt.Errorf("damaged backup: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return m, err
	}
	seen := map[string]bool{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, fmt.Errorf("damaged backup: %w", err)
		}
		switch h.Name {
		case manifestName, dbName, keyName:
		default:
			return m, fmt.Errorf("unexpected file %q in the backup", h.Name)
		}
		if h.Typeflag != tar.TypeReg || seen[h.Name] {
			return m, fmt.Errorf("unexpected entry %q in the backup", h.Name)
		}
		seen[h.Name] = true
		out, err := os.OpenFile(filepath.Join(dir, h.Name), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return m, err
		}
		_, err = io.Copy(out, tr)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			// The age stream authenticates every chunk: a modified file fails here.
			return m, fmt.Errorf("damaged backup: %w", err)
		}
	}
	for _, n := range []string{manifestName, dbName, keyName} {
		if !seen[n] {
			return m, fmt.Errorf("the backup has no %s", n)
		}
	}
	mb, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(mb, &m); err != nil {
		return m, fmt.Errorf("invalid manifest: %w", err)
	}
	if m.Format < 1 || m.Format > FormatVersion {
		return m, fmt.Errorf("backup format %d is not supported by this version of PortDock", m.Format)
	}
	kb, err := os.ReadFile(filepath.Join(dir, keyName))
	if err != nil {
		return m, err
	}
	if _, err := secrets.ParseKeyFile(kb); err != nil {
		return m, err
	}
	head := make([]byte, 16)
	if f, err := os.Open(filepath.Join(dir, dbName)); err == nil {
		_, _ = io.ReadFull(f, head)
		f.Close()
	}
	if string(head) != "SQLite format 3\x00" {
		return m, errors.New("the backup's database is not a SQLite file")
	}
	return m, nil
}
