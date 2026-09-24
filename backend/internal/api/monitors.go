package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/store"
	"github.com/portdock/portdock/internal/uptime"
)

// Uptime monitors and the public status page.

type recentCheck struct {
	OK         bool   `json:"ok"`
	ResponseMs int    `json:"response_ms"`
	CreatedAt  string `json:"created_at"`
}

type monitorOut struct {
	store.Monitor
	Uptime24h *float64      `json:"uptime_24h"`
	Uptime30d *float64      `json:"uptime_30d"`
	Recent    []recentCheck `json:"recent"`
}

func (s *Server) monitorOut(ctx context.Context, m store.Monitor) monitorOut {
	o := monitorOut{Monitor: m, Uptime24h: uptime.Uptime24h(ctx, s.db, m.ID), Uptime30d: uptime.Uptime(uptime.Days(ctx, s.db, m.ID, 30))}
	rows, _ := store.Select[store.MonitorCheck](ctx, s.db, "SELECT * FROM monitor_checks WHERE monitor_id = ? ORDER BY id DESC LIMIT 40", m.ID)
	o.Recent = make([]recentCheck, len(rows))
	for i, r := range rows {
		o.Recent[len(rows)-1-i] = recentCheck{OK: r.OK, ResponseMs: r.ResponseMs, CreatedAt: r.CreatedAt}
	}
	return o
}

func (s *Server) listMonitors(w http.ResponseWriter, r *http.Request) error {
	ms, err := store.Select[store.Monitor](r.Context(), s.db, "SELECT * FROM monitors ORDER BY name")
	if err != nil {
		return err
	}
	out := make([]monitorOut, 0, len(ms))
	for _, m := range ms {
		out = append(out, s.monitorOut(r.Context(), m))
	}
	return ok(w, out)
}

func (s *Server) getMonitor(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	m, err := store.ByID[store.Monitor](r.Context(), s.db, "monitors", id)
	if err != nil {
		return errStatus(404, "monitor not found")
	}
	ctx := r.Context()
	days := uptime.Days(ctx, s.db, m.ID, 90)
	checks, _ := store.Select[store.MonitorCheck](ctx, s.db, "SELECT * FROM monitor_checks WHERE monitor_id = ? AND created_at >= ? ORDER BY id",
		m.ID, time.Now().UTC().Add(-24*time.Hour).Format(time.RFC3339))
	incidents, _ := store.Select[store.MonitorIncident](ctx, s.db, "SELECT * FROM monitor_incidents WHERE monitor_id = ? ORDER BY id DESC LIMIT 20", m.ID)
	return ok(w, map[string]any{
		"monitor": s.monitorOut(ctx, m), "days": days, "checks": checks, "incidents": incidents,
		"uptime_7d": uptime.Uptime(days[len(days)-7:]), "uptime_90d": uptime.Uptime(days),
	})
}

type monitorIn struct {
	Name            *string `json:"name"`
	URL             *string `json:"url"`
	ApplicationID   *int64  `json:"application_id"`
	IntervalSeconds *int    `json:"interval_seconds"`
	TimeoutSeconds  *int    `json:"timeout_seconds"`
	ExpectedStatus  *string `json:"expected_status"`
	Keyword         *string `json:"keyword"`
	SSLWarnDays     *int    `json:"ssl_warn_days"`
	FailuresToAlert *int    `json:"failures_to_alert"`
	Enabled         *bool   `json:"enabled"`
	Public          *bool   `json:"public"`
}

func (s *Server) applyMonitor(ctx context.Context, m *store.Monitor, in monitorIn) error {
	if in.Name != nil {
		m.Name = strings.TrimSpace(*in.Name)
	}
	if in.URL != nil {
		m.URL = strings.TrimSpace(*in.URL)
	}
	if in.ApplicationID != nil {
		m.ApplicationID = *in.ApplicationID
	}
	if in.IntervalSeconds != nil {
		m.IntervalSeconds = *in.IntervalSeconds
	}
	if in.TimeoutSeconds != nil {
		m.TimeoutSeconds = *in.TimeoutSeconds
	}
	if in.ExpectedStatus != nil {
		m.ExpectedStatus = strings.ReplaceAll(strings.TrimSpace(*in.ExpectedStatus), " ", "")
	}
	if in.Keyword != nil {
		m.Keyword = strings.TrimSpace(*in.Keyword)
	}
	if in.SSLWarnDays != nil {
		m.SSLWarnDays = *in.SSLWarnDays
	}
	if in.FailuresToAlert != nil {
		m.FailuresToAlert = *in.FailuresToAlert
	}
	if in.Enabled != nil {
		m.Enabled = *in.Enabled
	}
	if in.Public != nil {
		m.Public = *in.Public
	}
	u, err := url.Parse(m.URL)
	switch {
	case m.Name == "":
		return badRequest("name is required")
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		return badRequest("enter an http:// or https:// URL")
	case m.IntervalSeconds < 20 || m.IntervalSeconds > 86400:
		return badRequest("the interval must be between 20 seconds and 24 hours")
	case m.TimeoutSeconds < 1 || m.TimeoutSeconds > 60 || m.TimeoutSeconds >= m.IntervalSeconds:
		return badRequest("the timeout must be between 1 and 60 seconds and shorter than the interval")
	case m.SSLWarnDays < 0 || m.SSLWarnDays > 90:
		return badRequest("the certificate warning must be between 0 and 90 days")
	case m.FailuresToAlert < 1 || m.FailuresToAlert > 10:
		return badRequest("failed checks before an alert must be between 1 and 10")
	case len(m.Keyword) > 500:
		return badRequest("the keyword is too long")
	}
	if m.ExpectedStatus == "" {
		m.ExpectedStatus = "200-399"
	}
	if err := uptime.ValidStatus(m.ExpectedStatus); err != nil {
		return badRequest("%v", err)
	}
	if m.ApplicationID != 0 {
		if _, err := store.ByID[store.Application](ctx, s.db, "applications", m.ApplicationID); err != nil {
			return badRequest("application not found")
		}
	}
	if !m.Enabled {
		m.Status = "paused"
	} else if m.Status == "paused" || m.Status == "" {
		m.Status, m.ConsecutiveFailures = "pending", 0
	}
	return nil
}

func (s *Server) createMonitor(w http.ResponseWriter, r *http.Request) error {
	var in monitorIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	m := store.Monitor{IntervalSeconds: 60, TimeoutSeconds: 10, ExpectedStatus: "200-399", SSLWarnDays: 14, FailuresToAlert: 2,
		Enabled: true, Public: true, Status: "pending", CreatedAt: store.Now()}
	if err := s.applyMonitor(r.Context(), &m, in); err != nil {
		return err
	}
	if _, err := store.Insert(r.Context(), s.db, "monitors", &m); err != nil {
		return err
	}
	s.activity(r, "monitor.create", m.Name+" ("+m.URL+")")
	if m.Enabled && s.uptime != nil {
		go s.uptime.CheckNow(context.WithoutCancel(r.Context()), m)
	}
	return ok(w, s.monitorOut(r.Context(), m))
}

func (s *Server) updateMonitor(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	m, err := store.ByID[store.Monitor](r.Context(), s.db, "monitors", id)
	if err != nil {
		return errStatus(404, "monitor not found")
	}
	var in monitorIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	oldURL := m.URL
	if err := s.applyMonitor(r.Context(), &m, in); err != nil {
		return err
	}
	if m.URL != oldURL && m.Enabled {
		m.Status, m.ConsecutiveFailures, m.LastCheckedAt = "pending", 0, ""
		_, _ = s.db.ExecContext(r.Context(), "UPDATE monitor_incidents SET resolved_at = ? WHERE monitor_id = ? AND resolved_at = ''", store.Now(), m.ID)
	}
	if err := store.Update(r.Context(), s.db, "monitors", &m); err != nil {
		return err
	}
	s.activity(r, "monitor.update", m.Name)
	return ok(w, s.monitorOut(r.Context(), m))
}

func (s *Server) deleteMonitor(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	m, err := store.ByID[store.Monitor](r.Context(), s.db, "monitors", id)
	if err != nil {
		return errStatus(404, "monitor not found")
	}
	for _, t := range []string{"monitor_checks", "monitor_days", "monitor_incidents"} {
		_, _ = s.db.ExecContext(r.Context(), "DELETE FROM "+t+" WHERE monitor_id = ?", id)
	}
	if err := store.Delete(r.Context(), s.db, "monitors", id); err != nil {
		return err
	}
	s.activity(r, "monitor.delete", m.Name)
	return ok(w, map[string]bool{"ok": true})
}

// checkMonitor runs a check right away and returns its result.
func (s *Server) checkMonitor(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	m, err := store.ByID[store.Monitor](r.Context(), s.db, "monitors", id)
	if err != nil {
		return errStatus(404, "monitor not found")
	}
	var res uptime.Result
	if m.Enabled && s.uptime != nil {
		res = s.uptime.CheckNow(r.Context(), m)
	} else {
		res = uptime.Probe(r.Context(), m) // paused: test only, nothing recorded
	}
	return ok(w, map[string]any{"ok": res.OK, "status_code": res.StatusCode, "response_ms": res.ResponseMs, "error": res.Error})
}

// ---- public status page ----

type publicMonitor struct {
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Uptime   *float64 `json:"uptime_90d"`
	Days     []pubDay `json:"days"`
	Response int      `json:"response_ms"`
}

type pubDay struct {
	Day    string   `json:"day"`
	Uptime *float64 `json:"uptime"`
}

type publicIncident struct {
	Monitor    string `json:"monitor"`
	StartedAt  string `json:"started_at"`
	ResolvedAt string `json:"resolved_at"`
}

// publicStatus serves the status page data without URLs or error details.
func (s *Server) publicStatus(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if s.db.Setting(ctx, "status_page_enabled") != "true" {
		return errStatus(404, "the status page is not enabled")
	}
	ms, err := store.Select[store.Monitor](ctx, s.db, "SELECT * FROM monitors WHERE public = 1 ORDER BY name")
	if err != nil {
		return err
	}
	overall := "operational"
	names := map[int64]string{}
	var ids []any
	out := make([]publicMonitor, 0, len(ms))
	down := 0
	for _, m := range ms {
		names[m.ID] = m.Name
		ids = append(ids, m.ID)
		days := uptime.Days(ctx, s.db, m.ID, 90)
		pd := make([]pubDay, len(days))
		for i, d := range days {
			pd[i] = pubDay{Day: d.Day, Uptime: d.Uptime}
		}
		st := m.Status
		if st == "pending" {
			st = "up"
		}
		if st == "down" {
			down++
		}
		out = append(out, publicMonitor{Name: m.Name, Status: st, Uptime: uptime.Uptime(days), Days: pd, Response: m.LastResponseMs})
	}
	if down > 0 {
		overall = "degraded"
		if down == len(out) {
			overall = "outage"
		}
	}
	incidents := []publicIncident{}
	if len(ids) > 0 {
		since := time.Now().UTC().AddDate(0, 0, -14).Format(time.RFC3339)
		q := "SELECT * FROM monitor_incidents WHERE started_at >= ? AND monitor_id IN (?" + strings.Repeat(",?", len(ids)-1) + ") ORDER BY started_at DESC LIMIT 50"
		rows, _ := store.Select[store.MonitorIncident](ctx, s.db, q, append([]any{since}, ids...)...)
		for _, in := range rows {
			incidents = append(incidents, publicIncident{Monitor: names[in.MonitorID], StartedAt: in.StartedAt, ResolvedAt: in.ResolvedAt})
		}
	}
	title := s.db.Setting(ctx, "status_page_title")
	if title == "" {
		title = s.db.Setting(ctx, "instance_name")
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	return ok(w, map[string]any{
		"title": title, "description": s.db.Setting(ctx, "status_page_description"),
		"status": overall, "monitors": out, "incidents": incidents, "updated_at": store.Now(),
	})
}
