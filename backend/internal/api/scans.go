package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/portdock/portdock/internal/deploy"
	"github.com/portdock/portdock/internal/store"
)

// Vulnerability scans (Trivy) of the images of an application.

func (s *Server) listScans(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	scans, err := store.Select[store.ImageScan](r.Context(), s.db,
		"SELECT * FROM image_scans WHERE application_id = ? ORDER BY id DESC LIMIT 50", app.ID)
	if err != nil {
		return err
	}
	running := false
	for _, sc := range scans {
		running = running || sc.Status == "running"
	}
	scanner := s.db.Setting(r.Context(), "trivy_image")
	if scanner == "" {
		scanner = deploy.DefaultTrivyImage
	}
	return ok(w, map[string]any{"scans": scans, "running": running, "scanner": scanner})
}

func (s *Server) getScan(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	id, err := strconv.ParseInt(r.PathValue("scan"), 10, 64)
	if err != nil {
		return badRequest("invalid scan")
	}
	sc, err := store.ByID[store.ImageScan](r.Context(), s.db, "image_scans", id)
	if err != nil || sc.ApplicationID != app.ID {
		return errStatus(404, "scan not found")
	}
	return ok(w, map[string]any{"scan": sc, "findings": deploy.ScanFindings(sc)})
}

func (s *Server) scanApplication(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	if err := s.engine.ScanApp(r.Context(), app, 0, "manual"); err != nil {
		if errors.Is(err, deploy.ErrScanRunning) {
			return errStatus(409, "%v", err)
		}
		return badRequest("%v", err)
	}
	s.activity(r, "application.scan", app.Name)
	return ok(w, map[string]bool{"ok": true})
}
