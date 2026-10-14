package api

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/instancebackup"
	"github.com/portdock/portdock/internal/scheduler"
)

// Backup of PortDock itself (Settings → Backup storage → Instance backup) and
// its restore, on a running instance (owner) or on a new one (first-run page).

type instanceBackupDTO struct {
	instancebackup.Settings
	Last         *instancebackup.LastRun `json:"last"`
	Files        []instancebackup.File   `json:"files"`
	S3Configured bool                    `json:"s3_configured"`
}

func (s *Server) instanceBackupStatus(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	return ok(w, instanceBackupDTO{
		Settings: s.backups.Settings(ctx), Last: s.backups.Last(ctx), Files: s.backups.List(), S3Configured: s.engine.S3Configured(ctx),
	})
}

func (s *Server) saveInstanceBackup(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		instancebackup.Settings
		Passphrase string `json:"passphrase"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.Schedule == "" {
		in.Schedule = instancebackup.DefaultSchedule
	}
	if err := scheduler.Validate(in.Schedule); err != nil {
		return badRequest("invalid schedule: %v", err)
	}
	if err := s.backups.SaveSettings(r.Context(), in.Settings, in.Passphrase); err != nil {
		return badRequest("%v", err)
	}
	if s.sched != nil {
		s.sched.Reload(r.Context())
	}
	s.activity(r, "instance_backup.settings", "")
	return s.instanceBackupStatus(w, r)
}

func (s *Server) runInstanceBackup(w http.ResponseWriter, r *http.Request) error {
	f, err := s.backups.Run(r.Context(), "manual")
	if err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "instance_backup.run", f.Name)
	return s.instanceBackupStatus(w, r)
}

func (s *Server) listInstanceBackupsS3(w http.ResponseWriter, r *http.Request) error {
	objs, err := s.backups.ListS3(r.Context())
	if err != nil {
		return errStatus(502, "S3: %v", err)
	}
	return ok(w, objs)
}

// fetchInstanceBackupS3 copies a backup from the bucket to the server, where
// it can be downloaded or restored.
func (s *Server) fetchInstanceBackupS3(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.backups.FetchS3(r.Context(), r.PathValue("name")); err != nil {
		return errStatus(502, "S3: %v", err)
	}
	return s.instanceBackupStatus(w, r)
}

func (s *Server) downloadInstanceBackup(w http.ResponseWriter, r *http.Request) error {
	p, err := s.backups.Path(r.PathValue("name"))
	if err != nil {
		return errStatus(404, "%v", err)
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+r.PathValue("name")+`"`)
	s.activity(r, "instance_backup.download", r.PathValue("name"))
	_, err = io.Copy(w, f)
	return err
}

func (s *Server) deleteInstanceBackup(w http.ResponseWriter, r *http.Request) error {
	if err := s.backups.Delete(r.PathValue("name")); err != nil {
		return errStatus(404, "%v", err)
	}
	s.activity(r, "instance_backup.delete", r.PathValue("name"))
	return s.instanceBackupStatus(w, r)
}

// restoreInstanceBackup replaces the whole instance with a backup and restarts
// PortDock. Only the owner may do it: it brings back every account as it was.
func (s *Server) restoreInstanceBackup(w http.ResponseWriter, r *http.Request) error {
	if auth.UserFrom(r.Context()).Role != "owner" {
		return errStatus(403, "only the owner can restore the instance")
	}
	var in struct {
		Passphrase string `json:"passphrase"`
		Force      bool   `json:"force"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	var active int
	_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM deployments WHERE status IN ('running', 'queued')").Scan(&active)
	if active > 0 && !in.Force {
		return errStatus(409, "%d deployment(s) are running or queued; wait for them to finish (or force the restore)", active)
	}
	m, err := s.backups.StageFile(r.Context(), r.PathValue("name"), in.Passphrase)
	if err != nil {
		return restoreError(err)
	}
	s.activity(r, "instance_backup.restore", r.PathValue("name"))
	return s.restartAfterRestore(w, m)
}

// setupRestore restores a backup on a new instance, from the first-run page.
// It works only while the instance has no account, like the registration.
// The form sends the passphrase before the file, which is streamed.
func (s *Server) setupRestore(w http.ResponseWriter, r *http.Request) error {
	if s.userCount(r) > 0 {
		return errStatus(403, "this instance is already set up; the owner can restore a backup from Settings → Backup storage")
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return badRequest("send the backup as multipart/form-data")
	}
	var passphrase string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return badRequest("no backup file")
		}
		if err != nil {
			return badRequest("invalid upload: %v", err)
		}
		switch part.FormName() {
		case "passphrase":
			b, _ := io.ReadAll(io.LimitReader(part, 4096))
			passphrase = string(b)
		case "file":
			if passphrase == "" {
				return badRequest("the passphrase must come before the file")
			}
			m, err := instancebackup.Stage(part, passphrase, s.cfg.DataDir, s.cfg.SecretKey)
			if err != nil {
				return restoreError(err)
			}
			s.db.LogActivity(r.Context(), 0, "instance_backup.restore", "first-run restore of "+m.CreatedAt)
			return s.restartAfterRestore(w, m)
		}
	}
}

func restoreError(err error) error {
	if errors.Is(err, instancebackup.ErrPassphrase) {
		return badRequest("%v", err)
	}
	return badRequest("restore failed: %v", err)
}

// restartAfterRestore answers, then stops PortDock; Docker starts it again and
// the staged backup replaces the data before the database is opened.
func (s *Server) restartAfterRestore(w http.ResponseWriter, m instancebackup.Manifest) error {
	err := ok(w, map[string]any{"restarting": true, "backup": m})
	go func() {
		time.Sleep(time.Second)
		s.restart()
	}()
	return err
}
