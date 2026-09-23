package api

import (
	"net/http"
	"slices"
	"strings"

	"github.com/shipyard/shipyard/internal/auth"
	"github.com/shipyard/shipyard/internal/monitor"
	"github.com/shipyard/shipyard/internal/store"
)

var scopeTables = map[string]string{"server": "servers", "application": "applications", "database": "databases", "service": "services"}

// metricsSeries returns a time series: GET /api/metrics/{scope}/{id}?range=24h
func (s *Server) metricsSeries(w http.ResponseWriter, r *http.Request) error {
	scope := r.PathValue("scope")
	table, known := scopeTables[scope]
	if !known {
		return badRequest("unknown scope %q", scope)
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var exists int
	if err := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM "+table+" WHERE id = ?", id).Scan(&exists); err != nil || exists == 0 {
		return errStatus(404, "not found")
	}
	if scope != "server" {
		envID, err := s.envOf(r.Context(), scope, id)
		if err != nil {
			return err
		}
		if _, _, err := s.authorizeEnv(r.Context(), envID, permRead); err != nil {
			return err
		}
	}
	rg := r.URL.Query().Get("range")
	if rg == "" {
		rg = "24h"
	}
	pts, err := s.monitor.Series(r.Context(), scope, id, rg)
	if err != nil {
		return badRequest("%v", err)
	}
	return ok(w, map[string]any{"range": rg, "bucket": monitor.Ranges[rg].Bucket, "points": pts})
}

type alertOut struct {
	store.AlertEvent
	RuleName string `db:"rule_name" json:"rule_name"`
	Severity string `db:"severity" json:"severity"`
	Metric   string `db:"metric" json:"metric"`
	AckName  string `db:"ack_name" json:"acknowledged_by_name"`
}

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) error {
	q := `SELECT e.*, r.name AS rule_name, r.severity, r.metric, COALESCE(u.name, '') AS ack_name
		FROM alert_events e JOIN alert_rules r ON r.id = e.rule_id LEFT JOIN users u ON u.id = e.acknowledged_by`
	var args []any
	if st := r.URL.Query().Get("status"); st == "firing" || st == "resolved" {
		q += " WHERE e.status = ?"
		args = append(args, st)
	}
	q += " ORDER BY CASE e.status WHEN 'firing' THEN 0 ELSE 1 END, e.id DESC LIMIT 300"
	rows, err := store.Select[alertOut](r.Context(), s.db, q, args...)
	if err != nil {
		return err
	}
	if !auth.UserFrom(r.Context()).IsAdmin() {
		visible := s.envVisibility(r.Context())
		rows = filterBy(rows, func(a alertOut) bool {
			if a.Scope == "server" {
				return true
			}
			env, err := s.envOf(r.Context(), a.Scope, a.RefID)
			return err == nil && visible(env)
		})
	}
	return ok(w, rows)
}

func (s *Server) ackAlert(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	u := auth.UserFrom(r.Context())
	if _, err := s.db.ExecContext(r.Context(), "UPDATE alert_events SET acknowledged_by = ? WHERE id = ? AND status = 'firing'", u.ID, id); err != nil {
		return err
	}
	s.activity(r, "alert.acknowledge", "#"+r.PathValue("id"))
	s.hub.Event("alert", id, "acknowledged")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) listAlertRules(w http.ResponseWriter, r *http.Request) error {
	rules, err := store.Select[store.AlertRule](r.Context(), s.db, "SELECT * FROM alert_rules ORDER BY scope, name")
	if err != nil {
		return err
	}
	type channel struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		Type    string `json:"type"`
		Enabled bool   `json:"enabled"`
		Alerts  bool   `json:"alerts"`
	}
	chans, _ := store.Select[store.NotificationChannel](r.Context(), s.db, "SELECT * FROM notification_channels ORDER BY id")
	out := make([]channel, 0, len(chans))
	for _, c := range chans {
		out = append(out, channel{c.ID, c.Name, c.Type, c.Enabled, slices.Contains(store.SplitList(c.Events), "alert.firing")})
	}
	return ok(w, map[string]any{"rules": rules, "metrics": monitor.Metrics, "channels": out})
}

type alertRuleIn struct {
	Name      *string  `json:"name"`
	Scope     *string  `json:"scope"`
	TargetID  *int64   `json:"target_id"`
	Metric    *string  `json:"metric"`
	Threshold *float64 `json:"threshold"`
	Duration  *int     `json:"duration"`
	Severity  *string  `json:"severity"`
	Enabled   *bool    `json:"enabled"`
}

func (s *Server) applyAlertRule(r *http.Request, rule *store.AlertRule, in alertRuleIn) error {
	if in.Name != nil {
		rule.Name = strings.TrimSpace(*in.Name)
	}
	if in.Scope != nil {
		rule.Scope = *in.Scope
	}
	if in.TargetID != nil {
		rule.TargetID = *in.TargetID
	}
	if in.Metric != nil {
		rule.Metric = *in.Metric
	}
	if in.Threshold != nil {
		rule.Threshold = *in.Threshold
	}
	if in.Duration != nil {
		rule.Duration = *in.Duration
	}
	if in.Severity != nil {
		rule.Severity = *in.Severity
	}
	if in.Enabled != nil {
		rule.Enabled = *in.Enabled
	}
	metrics, known := monitor.Metrics[rule.Scope]
	if !known {
		return badRequest("scope must be server, application, database or service")
	}
	if !slices.Contains(metrics, rule.Metric) {
		return badRequest("metric %q is not available for %s rules", rule.Metric, rule.Scope)
	}
	if rule.Duration < 1 || rule.Duration > 1440 {
		return badRequest("duration must be between 1 and 1440 minutes")
	}
	switch rule.Metric {
	case "memory", "disk":
		if rule.Threshold <= 0 || rule.Threshold >= 100 {
			return badRequest("threshold must be a percentage between 0 and 100")
		}
	case "cpu", "load", "restarts":
		if rule.Threshold < 0 {
			return badRequest("threshold must be positive")
		}
	}
	if rule.Severity != "critical" {
		rule.Severity = "warning"
	}
	if rule.TargetID != 0 {
		var n int
		_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM "+scopeTables[rule.Scope]+" WHERE id = ?", rule.TargetID).Scan(&n)
		if n == 0 {
			return badRequest("target not found")
		}
	}
	if rule.Name == "" {
		return badRequest("name is required")
	}
	return nil
}

func (s *Server) createAlertRule(w http.ResponseWriter, r *http.Request) error {
	var in alertRuleIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	rule := store.AlertRule{Enabled: true, Duration: 5, Severity: "warning", CreatedAt: store.Now()}
	if err := s.applyAlertRule(r, &rule, in); err != nil {
		return err
	}
	if _, err := store.Insert(r.Context(), s.db, "alert_rules", &rule); err != nil {
		return err
	}
	s.activity(r, "alert_rule.create", rule.Name)
	return ok(w, rule)
}

func (s *Server) updateAlertRule(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	rule, err := store.ByID[store.AlertRule](r.Context(), s.db, "alert_rules", id)
	if err != nil {
		return err
	}
	var in alertRuleIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.applyAlertRule(r, &rule, in); err != nil {
		return err
	}
	if err := store.Update(r.Context(), s.db, "alert_rules", &rule); err != nil {
		return err
	}
	s.activity(r, "alert_rule.update", rule.Name)
	return ok(w, rule)
}

func (s *Server) deleteAlertRule(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	rule, err := store.ByID[store.AlertRule](r.Context(), s.db, "alert_rules", id)
	if err != nil {
		return err
	}
	if err := store.Delete(r.Context(), s.db, "alert_rules", id); err != nil {
		return err
	}
	s.activity(r, "alert_rule.delete", rule.Name)
	return ok(w, map[string]bool{"ok": true})
}

// setChannelAlerts subscribes a notification channel to alert events.
func (s *Server) setChannelAlerts(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Alerts bool `json:"alerts"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	c, err := store.ByID[store.NotificationChannel](r.Context(), s.db, "notification_channels", id)
	if err != nil {
		return err
	}
	events := slices.DeleteFunc(store.SplitList(c.Events), func(e string) bool { return strings.HasPrefix(e, "alert.") })
	if in.Alerts {
		events = append(events, "alert.firing", "alert.resolved")
	}
	c.Events = strings.Join(events, ",")
	if err := store.Update(r.Context(), s.db, "notification_channels", &c); err != nil {
		return err
	}
	return ok(w, map[string]bool{"ok": true})
}
