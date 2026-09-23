package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/store"
)

// Metrics a rule can watch, per scope.
var Metrics = map[string][]string{
	"server":      {"cpu", "memory", "disk", "load"},
	"application": {"cpu", "memory", "restarts", "down"},
	"database":    {"cpu", "memory", "restarts", "down"},
	"service":     {"cpu", "memory", "restarts", "down"},
}

var metricLabels = map[string]string{"cpu": "CPU", "memory": "Memory", "disk": "Disk", "load": "Load", "restarts": "Restarts", "down": "Down"}

func valueExpr(metric string) string {
	switch metric {
	case "memory":
		return "CASE WHEN mem_total > 0 THEN mem * 100.0 / mem_total ELSE 0 END"
	case "disk":
		return "CASE WHEN disk_total > 0 THEN disk * 100.0 / disk_total ELSE 0 END"
	case "load":
		return "load"
	case "restarts":
		return "restarts"
	case "down":
		return "up"
	}
	return "cpu"
}

type window struct {
	RefID, ServerID int64
	Count           int
	Min, Max, Avg   float64
	Sum             float64
}

// windows aggregates the rule's metric per (resource, server) over its duration.
func (m *Monitor) windows(ctx context.Context, r store.AlertRule) ([]window, error) {
	since := m.now().Add(-time.Duration(r.Duration)*time.Minute - 30*time.Second).Unix()
	q := fmt.Sprintf(`SELECT ref_id, server_id, count(*), min(v), max(v), avg(v), sum(v) FROM
		(SELECT ref_id, server_id, %s AS v FROM metrics WHERE scope = ? AND ts >= ?`, valueExpr(r.Metric))
	args := []any{r.Scope, since}
	if r.TargetID != 0 {
		q += " AND ref_id = ?"
		args = append(args, r.TargetID)
	}
	q += ") GROUP BY ref_id, server_id"
	rows, err := m.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []window
	for rows.Next() {
		var w window
		if err := rows.Scan(&w.RefID, &w.ServerID, &w.Count, &w.Min, &w.Max, &w.Avg, &w.Sum); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// breached decides whether a window violates the rule and the value to report.
// Gauges must stay above the threshold for the whole duration (every sample),
// with at least ~80% of the expected samples present.
func breached(r store.AlertRule, w window) (bool, float64) {
	need := max(1, r.Duration*8/10)
	switch r.Metric {
	case "restarts":
		return w.Sum > r.Threshold, w.Sum
	case "down":
		return w.Count >= need && w.Max == 0, 0
	default:
		return w.Count >= need && w.Min > r.Threshold, w.Avg
	}
}

// intentionallyStopped skips "down" alerts for resources a user stopped.
func (m *Monitor) intentionallyStopped(ctx context.Context, scope string, id int64) bool {
	table := map[string]string{"application": "applications", "database": "databases", "service": "services"}[scope]
	if table == "" {
		return false
	}
	var status string
	if err := m.db.QueryRowContext(ctx, "SELECT status FROM "+table+" WHERE id = ?", id).Scan(&status); err != nil {
		return true // deleted
	}
	switch status {
	case "stopped", "exited", "deploying", "queued":
		return true
	}
	return false
}

// TargetName describes a resource for alert messages ("api on node-2").
func (m *Monitor) TargetName(ctx context.Context, scope string, id, serverID int64) string {
	var name string
	table := map[string]string{"server": "servers", "application": "applications", "database": "databases", "service": "services"}[scope]
	_ = m.db.QueryRowContext(ctx, "SELECT name FROM "+table+" WHERE id = ?", id).Scan(&name)
	if name == "" {
		name = fmt.Sprintf("%s #%d", scope, id)
	}
	if scope != "server" {
		var multi int
		_ = m.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM application_servers WHERE application_id = ?", id).Scan(&multi)
		if multi > 0 && scope == "application" {
			var srv string
			_ = m.db.QueryRowContext(ctx, "SELECT name FROM servers WHERE id = ?", serverID).Scan(&srv)
			if srv != "" {
				name += " on " + srv
			}
		}
	}
	return name
}

func describe(r store.AlertRule, value float64) string {
	unit := "%"
	switch r.Metric {
	case "load":
		unit = ""
	case "restarts":
		return fmt.Sprintf("%.0f restart(s) in the last %d min (threshold %.0f)", value, r.Duration, r.Threshold)
	case "down":
		return fmt.Sprintf("no healthy container for %d min", r.Duration)
	}
	return fmt.Sprintf("%s %.1f%s is above %.0f%s for %d min", metricLabels[r.Metric], value, unit, r.Threshold, unit, r.Duration)
}

type eventKey struct {
	rule, ref, server int64
}

// Evaluate checks every enabled rule, opening and resolving alert events.
func (m *Monitor) Evaluate(ctx context.Context) {
	rules, err := store.Select[store.AlertRule](ctx, m.db, "SELECT * FROM alert_rules WHERE enabled = 1")
	if err != nil {
		return
	}
	open, err := store.Select[store.AlertEvent](ctx, m.db, "SELECT * FROM alert_events WHERE status = 'firing'")
	if err != nil {
		return
	}
	openBy := map[eventKey]store.AlertEvent{}
	for _, e := range open {
		openBy[eventKey{e.RuleID, e.RefID, e.ServerID}] = e
	}
	seen := map[eventKey]bool{}
	enabled := map[int64]bool{}
	for _, r := range rules {
		enabled[r.ID] = true
		ws, err := m.windows(ctx, r)
		if err != nil {
			slog.Warn("alert rule", "rule", r.Name, "err", err)
			continue
		}
		for _, w := range ws {
			k := eventKey{r.ID, w.RefID, w.ServerID}
			bad, value := breached(r, w)
			if bad && r.Metric == "down" && m.intentionallyStopped(ctx, r.Scope, w.RefID) {
				bad = false
			}
			ev, isOpen := openBy[k]
			switch {
			case bad && !isOpen:
				m.fire(ctx, r, w, value)
				seen[k] = true
			case bad:
				seen[k] = true
				_, _ = m.db.ExecContext(ctx, "UPDATE alert_events SET value = ?, message = ? WHERE id = ?", value, describe(r, value), ev.ID)
			}
		}
	}
	// Resolve events whose condition cleared, whose target vanished or whose rule was disabled.
	for k, ev := range openBy {
		if seen[k] {
			continue
		}
		m.resolve(ctx, ev, !enabled[ev.RuleID])
	}
}

func (m *Monitor) fire(ctx context.Context, r store.AlertRule, w window, value float64) {
	ev := store.AlertEvent{RuleID: r.ID, Scope: r.Scope, RefID: w.RefID, ServerID: w.ServerID, Status: "firing", Value: value,
		TargetName: m.TargetName(ctx, r.Scope, w.RefID, w.ServerID), Message: describe(r, value), StartedAt: store.Now()}
	if _, err := store.Insert(ctx, m.db, "alert_events", &ev); err != nil {
		slog.Warn("alert insert", "err", err)
		return
	}
	m.db.LogActivity(ctx, 0, "alert.firing", r.Name+": "+ev.TargetName)
	m.hub.Event("alert", ev.ID, "firing")
	m.notify.Send(notify.Event{
		Name:    "alert.firing",
		Title:   fmt.Sprintf("[%s] %s — %s", strings.ToUpper(r.Severity), r.Name, ev.TargetName),
		Message: ev.Message,
		Fields:  map[string]string{"Severity": r.Severity, "Target": ev.TargetName, "Rule": r.Name},
	})
}

func (m *Monitor) resolve(ctx context.Context, ev store.AlertEvent, silent bool) {
	_, _ = m.db.ExecContext(ctx, "UPDATE alert_events SET status = 'resolved', resolved_at = ? WHERE id = ?", store.Now(), ev.ID)
	m.hub.Event("alert", ev.ID, "resolved")
	if silent {
		return
	}
	var rule string
	_ = m.db.QueryRowContext(ctx, "SELECT name FROM alert_rules WHERE id = ?", ev.RuleID).Scan(&rule)
	started, _ := time.Parse(time.RFC3339, ev.StartedAt)
	m.notify.Send(notify.Event{
		Name:    "alert.resolved",
		Title:   fmt.Sprintf("[RESOLVED] %s — %s", rule, ev.TargetName),
		Message: fmt.Sprintf("Recovered after %s.", time.Since(started).Round(time.Minute)),
		Success: true,
	})
}

// DefaultRules are created once so a fresh instance alerts out of the box.
var DefaultRules = []store.AlertRule{
	{Name: "High server CPU", Scope: "server", Metric: "cpu", Threshold: 90, Duration: 10, Severity: "warning"},
	{Name: "High server memory", Scope: "server", Metric: "memory", Threshold: 90, Duration: 5, Severity: "critical"},
	{Name: "Disk almost full", Scope: "server", Metric: "disk", Threshold: 85, Duration: 10, Severity: "warning"},
	{Name: "Application crash loop", Scope: "application", Metric: "restarts", Threshold: 3, Duration: 10, Severity: "critical"},
	{Name: "Application down", Scope: "application", Metric: "down", Duration: 3, Severity: "critical"},
	{Name: "Database down", Scope: "database", Metric: "down", Duration: 2, Severity: "critical"},
}

func (m *Monitor) SeedDefaultRules(ctx context.Context) {
	if m.db.Setting(ctx, "alerts_seeded") != "" {
		return
	}
	for _, r := range DefaultRules {
		r.Enabled, r.CreatedAt = true, store.Now()
		if _, err := store.Insert(ctx, m.db, "alert_rules", &r); err != nil {
			slog.Warn("seed alert rule", "err", err)
			return
		}
	}
	_ = m.db.SetSetting(ctx, "alerts_seeded", "1")
}
