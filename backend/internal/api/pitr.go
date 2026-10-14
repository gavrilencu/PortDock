package api

import (
	"net/http"
	"time"

	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/scheduler"
)

// Point-in-time recovery of PostgreSQL databases (deploy/pitr.go).

func (s *Server) getPITR(w http.ResponseWriter, r *http.Request) error {
	db, err := s.loadDB(r)
	if err != nil {
		return err
	}
	st, err := s.engine.PITRStatusOf(r.Context(), db)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{
		"status": st, "supported": db.Engine == "postgres", "schedule": db.PITRSchedule, "retention": db.PITRRetention,
		"s3": db.PITRS3, "s3_configured": s.engine.S3Configured(r.Context()), "synced_at": db.PITRSyncedAt, "archive_error": db.PITRArchiveError,
	})
}

func (s *Server) updatePITR(w http.ResponseWriter, r *http.Request) error {
	db, err := s.loadDB(r)
	if err != nil {
		return err
	}
	in := struct {
		Enabled   bool   `json:"enabled"`
		Schedule  string `json:"schedule"`
		Retention int    `json:"retention"`
		S3        bool   `json:"s3"`
	}{Enabled: db.PITR, Schedule: db.PITRSchedule, Retention: db.PITRRetention, S3: db.PITRS3}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := scheduler.Validate(in.Schedule); err != nil {
		return badRequest("invalid base backup schedule: %v", err)
	}
	if in.Retention < 1 || in.Retention > 365 {
		return badRequest("keep between 1 and 365 base backups")
	}
	if in.S3 && !s.engine.S3Configured(r.Context()) {
		return badRequest("configure the S3 destination in Settings → Backup storage first")
	}
	was := db.PITR
	db, err = s.engine.SetPITR(r.Context(), db, in.Enabled, in.Schedule, in.Retention, in.S3)
	if err != nil {
		return badRequest("%v", err)
	}
	s.sched.Reload(r.Context())
	switch {
	case in.Enabled && !was:
		s.activity(r, "database.pitr_on", db.Name)
	case !in.Enabled && was:
		s.activity(r, "database.pitr_off", db.Name)
	}
	return ok(w, db)
}

func (s *Server) pitrBackupNow(w http.ResponseWriter, r *http.Request) error {
	db, err := s.loadDB(r)
	if err != nil {
		return err
	}
	b, err := s.engine.EnqueueBaseBackup(r.Context(), db)
	if err != nil {
		return badRequest("%v", err)
	}
	return ok(w, b)
}

// pitrRestore restores the database as it was at target_time (RFC 3339).
func (s *Server) pitrRestore(w http.ResponseWriter, r *http.Request) error {
	db, err := s.loadDB(r)
	if err != nil {
		return err
	}
	var in struct {
		TargetTime string `json:"target_time"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	target, err := time.Parse(time.RFC3339, in.TargetTime)
	if err != nil {
		return badRequest("target_time must be an RFC 3339 time, e.g. 2026-10-13T09:30:00Z")
	}
	res, err := s.engine.EnqueuePITRRestore(r.Context(), db, target, auth.UserFrom(r.Context()).ID)
	if err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "database.pitr_restore", db.Name+" → "+res.TargetTime)
	return ok(w, res)
}
