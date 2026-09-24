package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/portdock/portdock/internal/deploy"
	"github.com/portdock/portdock/internal/store"
)

// Security scans of an application: its image (Trivy), its source (SAST,
// Semgrep) and the running application (DAST, OWASP ZAP), with their reports.

var scanKinds = map[string]bool{"image": true, "sast": true, "dast": true}

type scanOut struct {
	store.ImageScan
	Reports []deploy.ReportFile `json:"reports"`
}

func (s *Server) scanOut(sc store.ImageScan) scanOut {
	return scanOut{ImageScan: sc, Reports: s.engine.Reports(sc)}
}

func (s *Server) listScans(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "image"
	}
	if !scanKinds[kind] {
		return badRequest("kind must be image, sast or dast")
	}
	scans, err := store.Select[store.ImageScan](r.Context(), s.db,
		"SELECT * FROM image_scans WHERE application_id = ? AND kind = ? ORDER BY id DESC LIMIT 50", app.ID, kind)
	if err != nil {
		return err
	}
	out := make([]scanOut, 0, len(scans))
	running := false
	for _, sc := range scans {
		running = running || sc.Status == "running"
		out = append(out, s.scanOut(sc))
	}
	res := map[string]any{"scans": out, "running": running}
	switch kind {
	case "image":
		res["scanner"] = orDefault(s.db.Setting(r.Context(), "trivy_image"), deploy.DefaultTrivyImage)
	case "sast":
		res["scanner"] = orDefault(s.db.Setting(r.Context(), "semgrep_image"), deploy.DefaultSemgrepImage)
		res["rules"] = orDefault(s.db.Setting(r.Context(), "semgrep_config"), deploy.DefaultSemgrepConfig)
		res["available"] = app.SourceType != "image" && app.BuildPack != "image"
	case "dast":
		res["scanner"] = orDefault(s.db.Setting(r.Context(), "zap_image"), deploy.DefaultZAPImage)
		target, err := s.engine.DASTTarget(r.Context(), app)
		res["target"] = target
		if err != nil {
			res["target_error"] = err.Error()
		}
	}
	return ok(w, res)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// loadScan loads a scan of the application in the path.
func (s *Server) loadScan(r *http.Request) (store.Application, store.ImageScan, error) {
	app, err := s.loadApp(r)
	if err != nil {
		return app, store.ImageScan{}, err
	}
	id, err := strconv.ParseInt(r.PathValue("scan"), 10, 64)
	if err != nil {
		return app, store.ImageScan{}, badRequest("invalid scan")
	}
	sc, err := store.ByID[store.ImageScan](r.Context(), s.db, "image_scans", id)
	if err != nil || sc.ApplicationID != app.ID {
		return app, sc, errStatus(404, "scan not found")
	}
	return app, sc, nil
}

func (s *Server) getScan(w http.ResponseWriter, r *http.Request) error {
	_, sc, err := s.loadScan(r)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"scan": s.scanOut(sc), "findings": deploy.ScanFindings(sc)})
}

func (s *Server) scanApplication(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	var in struct {
		Kind string `json:"kind"`
	}
	_ = readJSON(r, &in)
	if in.Kind == "" {
		in.Kind = r.URL.Query().Get("kind")
	}
	switch in.Kind {
	case "", "image":
		err = s.engine.ScanApp(r.Context(), app, 0, "manual")
	case "sast":
		err = s.engine.ScanSource(r.Context(), app)
	case "dast":
		err = s.engine.ScanRunning(r.Context(), app, 0, "manual")
	default:
		return badRequest("kind must be image, sast or dast")
	}
	if errors.Is(err, deploy.ErrScanRunning) {
		return errStatus(409, "%v", err)
	}
	if err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "application.scan", app.Name+" ("+orDefault(in.Kind, "image")+")")
	return ok(w, map[string]bool{"ok": true})
}

var reportTypes = map[string]string{".html": "text/html; charset=utf-8", ".csv": "text/csv; charset=utf-8", ".json": "application/json", ".sarif": "application/sarif+json"}

// scanReport downloads one report of a scan (?inline=1 opens HTML in the browser).
func (s *Server) scanReport(w http.ResponseWriter, r *http.Request) error {
	app, sc, err := s.loadScan(r)
	if err != nil {
		return err
	}
	name := r.PathValue("file")
	p := s.engine.ReportPath(sc, name)
	if p == "" {
		return errStatus(404, "report not found")
	}
	f, err := os.Open(p)
	if err != nil {
		return errStatus(404, "report not found")
	}
	defer f.Close()
	ext := filepath.Ext(name)
	w.Header().Set("Content-Type", reportTypes[ext])
	disp := "attachment"
	if r.URL.Query().Get("inline") == "1" && ext == ".html" {
		disp = "inline"
		// The report is served as a document of its own: no scripts, no requests.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox")
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s-%s-%d-%s"`, disp, safeFile(app.Name), sc.Kind, sc.ID, name))
	http.ServeContent(w, r, name, fileModTime(f), f)
	return nil
}

func (s *Server) scanReportsZip(w http.ResponseWriter, r *http.Request) error {
	app, sc, err := s.loadScan(r)
	if err != nil {
		return err
	}
	return s.writeZip(w, fmt.Sprintf("%s-%s-%d-reports.zip", safeFile(app.Name), sc.Kind, sc.ID), []store.ImageScan{sc})
}

// deploymentReports lists the security scans of a deployment.
func (s *Server) deploymentReports(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	scans, err := store.Select[store.ImageScan](r.Context(), s.db, "SELECT * FROM image_scans WHERE deployment_id = ? ORDER BY id", id)
	if err != nil {
		return err
	}
	out := make([]scanOut, 0, len(scans))
	for _, sc := range scans {
		out = append(out, s.scanOut(sc))
	}
	return ok(w, out)
}

// deploymentReportsZip downloads every report of the scans of a deployment.
func (s *Server) deploymentReportsZip(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	d, err := store.ByID[store.Deployment](r.Context(), s.db, "deployments", id)
	if err != nil {
		return err
	}
	app, _ := store.ByID[store.Application](r.Context(), s.db, "applications", d.ApplicationID)
	scans, err := store.Select[store.ImageScan](r.Context(), s.db, "SELECT * FROM image_scans WHERE deployment_id = ? AND status = 'success' ORDER BY id", id)
	if err != nil {
		return err
	}
	if len(scans) == 0 {
		return errStatus(404, "this deployment has no security reports")
	}
	return s.writeZip(w, fmt.Sprintf("%s-deployment-%d-security-reports.zip", safeFile(app.Name), id), scans)
}

func (s *Server) writeZip(w http.ResponseWriter, name string, scans []store.ImageScan) error {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	return s.engine.WriteReportsZip(w, scans)
}

func fileModTime(f *os.File) time.Time {
	if st, err := f.Stat(); err == nil {
		return st.ModTime()
	}
	return time.Time{}
}

func safeFile(s string) string {
	out := []rune{}
	for _, r := range s {
		if r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			out = append(out, r)
		} else {
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "app"
	}
	return string(out)
}
