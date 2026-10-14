package instancebackup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/deploy"
	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
)

// S3Prefix is where backups go in the bucket: portdock/instance/.
const S3Prefix = "instance"

// Settings keys.
const (
	keyEnabled    = "instance_backup_enabled"
	keySchedule   = "instance_backup_schedule"
	keyRetention  = "instance_backup_retention"
	keyUploadS3   = "instance_backup_s3"
	keyPassphrase = "instance_backup_passphrase" // encrypted with secret.key
	keyLast       = "instance_backup_last"
)

// DefaultSchedule backs up every night.
const DefaultSchedule = "0 3 * * *"

// Settings of the instance backup.
type Settings struct {
	Enabled       bool   `json:"enabled"`
	Schedule      string `json:"schedule"`
	Retention     int    `json:"retention"` // backups kept, locally and in S3
	UploadS3      bool   `json:"upload_s3"`
	PassphraseSet bool   `json:"passphrase_set"`
}

// LastRun is the outcome of the latest backup.
type LastRun struct {
	At      string `json:"at"`
	Status  string `json:"status"` // success | failed
	File    string `json:"file,omitempty"`
	Message string `json:"message,omitempty"`
}

// File is a backup kept on the PortDock server.
type File struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"created_at"`
}

// Service makes, lists and restores instance backups.
type Service struct {
	DataDir      string
	EnvSecretKey string
	Version      string
	db           *store.DB
	box          *secrets.Box
	engine       *deploy.Engine
	notify       *notify.Notifier
	mu           sync.Mutex // one backup at a time
}

func New(dataDir, envSecretKey, version string, db *store.DB, box *secrets.Box, engine *deploy.Engine, n *notify.Notifier) *Service {
	return &Service{DataDir: dataDir, EnvSecretKey: envSecretKey, Version: version, db: db, box: box, engine: engine, notify: n}
}

// Dir holds the backups on the PortDock server.
func (s *Service) Dir() string { return filepath.Join(s.DataDir, "backups", "instance") }

func (s *Service) Settings(ctx context.Context) Settings {
	st := Settings{
		Enabled:       s.db.Setting(ctx, keyEnabled) == "true",
		Schedule:      s.db.Setting(ctx, keySchedule),
		UploadS3:      s.db.Setting(ctx, keyUploadS3) == "true",
		PassphraseSet: s.db.Setting(ctx, keyPassphrase) != "",
	}
	if st.Schedule == "" {
		st.Schedule = DefaultSchedule
	}
	st.Retention, _ = strconv.Atoi(s.db.Setting(ctx, keyRetention))
	if st.Retention <= 0 {
		st.Retention = 7
	}
	return st
}

// SaveSettings stores st; a non-empty passphrase replaces the stored one.
func (s *Service) SaveSettings(ctx context.Context, st Settings, passphrase string) error {
	if st.Retention < 1 || st.Retention > 365 {
		return errors.New("keep between 1 and 365 backups")
	}
	if passphrase != "" {
		if len(passphrase) < MinPassphrase {
			return fmt.Errorf("the backup passphrase must be at least %d characters", MinPassphrase)
		}
		if err := s.db.SetSetting(ctx, keyPassphrase, s.box.Encrypt(passphrase)); err != nil {
			return err
		}
	} else if st.Enabled && s.db.Setting(ctx, keyPassphrase) == "" {
		return errors.New("set a backup passphrase first: it is needed to restore the backups")
	}
	if st.UploadS3 && !s.engine.S3Configured(ctx) {
		return errors.New("configure the S3 backup destination first")
	}
	for k, v := range map[string]string{
		keyEnabled: strconv.FormatBool(st.Enabled), keySchedule: st.Schedule,
		keyRetention: strconv.Itoa(st.Retention), keyUploadS3: strconv.FormatBool(st.UploadS3),
	} {
		if err := s.db.SetSetting(ctx, k, v); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Last(ctx context.Context) *LastRun {
	var l LastRun
	if json.Unmarshal([]byte(s.db.Setting(ctx, keyLast)), &l) != nil || l.At == "" {
		return nil
	}
	return &l
}

// Run makes a backup now and applies the retention. kind is scheduled or manual.
func (s *Service) Run(ctx context.Context, kind string) (File, error) {
	if !s.mu.TryLock() {
		return File{}, errors.New("a backup of the instance is already running")
	}
	defer s.mu.Unlock()
	f, warning, err := s.run(ctx, kind)
	last := LastRun{At: store.Now(), Status: "success", File: f.Name, Message: warning}
	if err != nil {
		last.Status, last.Message = "failed", err.Error()
		slog.Error("instance backup failed", "err", err)
		if s.notify != nil {
			s.notify.Send(notify.Event{Name: "backup.failed", Title: "PortDock instance backup failed", Message: err.Error()})
		}
	} else if warning != "" && s.notify != nil {
		s.notify.Send(notify.Event{Name: "backup.failed", Title: "PortDock instance backup: upload failed", Message: warning})
	}
	b, _ := json.Marshal(last)
	_ = s.db.SetSetting(context.WithoutCancel(ctx), keyLast, string(b))
	return f, err
}

var fileRe = regexp.MustCompile(`^portdock-[0-9]{8}-[0-9]{6}(-[a-z]+)?\.tar\.gz\.age$`)

func (s *Service) run(ctx context.Context, kind string) (File, string, error) {
	passphrase := s.box.MustDecrypt(s.db.Setting(ctx, keyPassphrase))
	if passphrase == "" {
		return File{}, "", errors.New("no backup passphrase is set")
	}
	dir := s.Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return File{}, "", err
	}
	now := time.Now().UTC()
	name := fmt.Sprintf("portdock-%s.tar.gz.age", now.Format("20060102-150405"))
	tmp := filepath.Join(dir, "."+name+".partial")
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return File{}, "", err
	}
	defer os.Remove(tmp)
	m := Manifest{Version: s.Version, CreatedAt: now.Format(time.RFC3339), Instance: s.db.Setting(ctx, "instance_name"), DataDir: s.DataDir, Kind: kind}
	err = Write(ctx, out, s.db, s.box.Key(), passphrase, m, dir)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return File{}, "", err
	}
	path := filepath.Join(dir, name)
	if err := os.Rename(tmp, path); err != nil {
		return File{}, "", err
	}
	st := s.Settings(ctx)
	s.pruneLocal(st.Retention)
	f := File{Name: name, CreatedAt: now.Format(time.RFC3339)}
	if info, err := os.Stat(path); err == nil {
		f.Size = info.Size()
	}
	var warning string
	if st.UploadS3 {
		if err := s.engine.UploadS3(ctx, path, S3Prefix); err != nil {
			warning = "saved on the server, but the upload to S3 failed: " + err.Error()
		} else if err := s.pruneS3(ctx, st.Retention); err != nil {
			slog.Warn("instance backup: S3 retention", "err", err)
		}
	}
	return f, warning, nil
}

// List returns the backups on the PortDock server, newest first.
func (s *Service) List() []File {
	entries, _ := os.ReadDir(s.Dir())
	out := []File{}
	for _, e := range entries {
		if !fileRe.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, File{Name: e.Name(), Size: info.Size(), CreatedAt: info.ModTime().UTC().Format(time.RFC3339)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out
}

// Path returns the file of a local backup, refusing any other name.
func (s *Service) Path(name string) (string, error) {
	if !fileRe.MatchString(name) {
		return "", errors.New("invalid backup name")
	}
	p := filepath.Join(s.Dir(), name)
	if _, err := os.Stat(p); err != nil {
		return "", errors.New("backup not found")
	}
	return p, nil
}

// ValidName reports whether name is the name of an instance backup.
func ValidName(name string) bool { return fileRe.MatchString(name) }

func (s *Service) Delete(name string) error {
	p, err := s.Path(name)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

func (s *Service) pruneLocal(keep int) {
	files := s.List()
	for _, f := range files[min(keep, len(files)):] {
		_ = os.Remove(filepath.Join(s.Dir(), f.Name))
	}
}

func (s *Service) pruneS3(ctx context.Context, keep int) error {
	objs, err := s.engine.ListS3(ctx, S3Prefix)
	if err != nil {
		return err
	}
	var ours []deploy.S3Object
	for _, o := range objs {
		if fileRe.MatchString(o.Name) {
			ours = append(ours, o)
		}
	}
	// ListS3 sorts by name, which starts with the time: oldest first.
	for i := 0; i < len(ours)-keep; i++ {
		if err := s.engine.DeleteS3(ctx, S3Prefix, ours[i].Name); err != nil {
			return err
		}
	}
	return nil
}

// ListS3 returns the instance backups in the bucket, newest first.
func (s *Service) ListS3(ctx context.Context) ([]deploy.S3Object, error) {
	objs, err := s.engine.ListS3(ctx, S3Prefix)
	if err != nil {
		return nil, err
	}
	out := []deploy.S3Object{}
	for i := len(objs) - 1; i >= 0; i-- {
		if fileRe.MatchString(objs[i].Name) {
			out = append(out, objs[i])
		}
	}
	return out, nil
}

// FetchS3 downloads a backup from the bucket into the local backup directory.
func (s *Service) FetchS3(ctx context.Context, name string) (string, error) {
	if !fileRe.MatchString(name) {
		return "", errors.New("invalid backup name")
	}
	if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
		return "", err
	}
	if err := s.engine.DownloadS3(ctx, S3Prefix, name, s.Dir()); err != nil {
		return "", err
	}
	return s.Path(name)
}

// StageFile prepares the restore of a local backup. An empty passphrase uses
// the stored one.
func (s *Service) StageFile(ctx context.Context, name, passphrase string) (Manifest, error) {
	p, err := s.Path(name)
	if err != nil {
		return Manifest{}, err
	}
	if passphrase == "" {
		passphrase = s.box.MustDecrypt(s.db.Setting(ctx, keyPassphrase))
	}
	f, err := os.Open(p)
	if err != nil {
		return Manifest{}, err
	}
	defer f.Close()
	return Stage(f, passphrase, s.DataDir, s.EnvSecretKey)
}
