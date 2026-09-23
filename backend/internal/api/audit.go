package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shipyard/shipyard/internal/audit"
	"github.com/shipyard/shipyard/internal/auth"
	"github.com/shipyard/shipyard/internal/license"
	"github.com/shipyard/shipyard/internal/store"
)

type activityRow struct {
	store.Activity
	UserName  string `db:"user_name" json:"user_name"`
	UserEmail string `db:"user_email" json:"user_email"`
}

// activityQuery builds the filtered audit query. Non-admins only see their own entries.
func (s *Server) activityQuery(r *http.Request) (string, []any, error) {
	q := r.URL.Query()
	where := []string{"1 = 1"}
	var args []any
	u := auth.UserFrom(r.Context())
	if !u.IsAdmin() {
		where = append(where, "a.user_id = ?")
		args = append(args, u.ID)
	} else if uid := q.Get("user_id"); uid != "" {
		id, err := strconv.ParseInt(uid, 10, 64)
		if err != nil {
			return "", nil, badRequest("invalid user_id")
		}
		where = append(where, "a.user_id = ?")
		args = append(args, id)
	}
	if act := strings.TrimSpace(q.Get("action")); act != "" {
		where = append(where, "a.action LIKE ?")
		args = append(args, strings.TrimSuffix(act, "*")+"%")
	}
	if term := strings.TrimSpace(q.Get("q")); term != "" {
		where = append(where, "(a.action LIKE ? OR a.target LIKE ? OR a.ip LIKE ? OR u.email LIKE ?)")
		like := "%" + term + "%"
		args = append(args, like, like, like, like)
	}
	for _, f := range []struct{ key, op string }{{"from", ">="}, {"to", "<"}} {
		if v := q.Get(f.key); v != "" {
			t, err := time.Parse("2006-01-02", v)
			if err != nil {
				if t, err = time.Parse(time.RFC3339, v); err != nil {
					return "", nil, badRequest("%s must be a date (YYYY-MM-DD)", f.key)
				}
			}
			if f.key == "to" && len(v) == 10 {
				t = t.Add(24 * time.Hour) // inclusive end date
			}
			where = append(where, "a.created_at "+f.op+" ?")
			args = append(args, t.UTC().Format(time.RFC3339))
		}
	}
	sql := `SELECT a.*, COALESCE(u.name, 'system') AS user_name, COALESCE(u.email, '') AS user_email
		FROM activity a LEFT JOIN users u ON u.id = a.user_id WHERE ` + strings.Join(where, " AND ") + " ORDER BY a.id DESC"
	return sql, args, nil
}

func (s *Server) listActivity(w http.ResponseWriter, r *http.Request) error {
	q, args, err := s.activityQuery(r)
	if err != nil {
		return err
	}
	limit, offset := 100, 0
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 500 {
		limit = l
	}
	if o, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && o >= 0 {
		offset = o
	}
	rows, err := store.Select[activityRow](r.Context(), s.db, q+" LIMIT ? OFFSET ?", append(args, limit+1, offset)...)
	if err != nil {
		return err
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	return ok(w, map[string]any{"entries": rows, "has_more": more})
}

// exportActivity downloads the (filtered) audit log as CSV or JSON.
func (s *Server) exportActivity(w http.ResponseWriter, r *http.Request) error {
	if err := s.requireFeature(r.Context(), license.FeatureAuditExport); err != nil {
		return err
	}
	q, args, err := s.activityQuery(r)
	if err != nil {
		return err
	}
	rows, err := store.Select[activityRow](r.Context(), s.db, q+" LIMIT 100000", args...)
	if err != nil {
		return err
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	s.activity(r, "audit.export", fmt.Sprintf("%d entries", len(rows)))
	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="shipyard-audit-`+stamp+`.json"`)
		out := make([]audit.Entry, 0, len(rows))
		for _, a := range rows {
			out = append(out, audit.Entry{ID: a.ID, Time: a.CreatedAt, Action: a.Action, Target: a.Target, UserID: a.UserID, UserEmail: a.UserEmail, UserName: a.UserName, IP: a.IP})
		}
		return json.NewEncoder(w).Encode(out)
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="shipyard-audit-`+stamp+`.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "time", "user_id", "user_email", "user_name", "action", "target", "ip"})
	for _, a := range rows {
		_ = cw.Write([]string{strconv.FormatInt(a.ID, 10), a.CreatedAt, strconv.FormatInt(a.UserID, 10), a.UserEmail, a.UserName, a.Action, csvSafe(a.Target), a.IP})
	}
	cw.Flush()
	return cw.Error()
}

// csvSafe neutralises spreadsheet formula injection (=, +, -, @ prefixes).
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

// ---- audit streaming settings ----

func (s *Server) getAuditSettings(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	lastErr, lastSent, dropped := s.audit.Status()
	sent := ""
	if !lastSent.IsZero() {
		sent = lastSent.UTC().Format(time.RFC3339)
	}
	return ok(w, map[string]any{
		"webhook_url":        s.db.Setting(ctx, "audit_webhook_url"),
		"webhook_secret_set": s.db.Setting(ctx, "audit_webhook_secret") != "",
		"syslog":             s.db.Setting(ctx, "audit_syslog"),
		"last_error":         lastErr,
		"last_sent_at":       sent,
		"dropped":            dropped,
	})
}

func (s *Server) updateAuditSettings(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		WebhookURL    *string `json:"webhook_url"`
		WebhookSecret *string `json:"webhook_secret"`
		Syslog        *string `json:"syslog"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	if (in.WebhookURL != nil && strings.TrimSpace(*in.WebhookURL) != "") || (in.Syslog != nil && strings.TrimSpace(*in.Syslog) != "") {
		if err := s.requireFeature(ctx, license.FeatureAuditStreaming); err != nil {
			return err
		}
	}
	if in.WebhookURL != nil {
		v := strings.TrimSpace(*in.WebhookURL)
		if v != "" {
			u, err := url.Parse(v)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				return badRequest("webhook URL must be an http(s) URL")
			}
		}
		_ = s.db.SetSetting(ctx, "audit_webhook_url", v)
	}
	if in.WebhookSecret != nil && *in.WebhookSecret != "" {
		_ = s.db.SetSetting(ctx, "audit_webhook_secret", s.box.Encrypt(strings.TrimSpace(*in.WebhookSecret)))
	}
	if in.Syslog != nil {
		v := strings.TrimSpace(*in.Syslog)
		if v != "" {
			if _, _, err := audit.ParseSyslogTarget(v); err != nil {
				return badRequest("%v", err)
			}
		}
		_ = s.db.SetSetting(ctx, "audit_syslog", v)
	}
	s.audit.Invalidate()
	s.activity(r, "audit.stream.update", "")
	return s.getAuditSettings(w, r)
}

func (s *Server) testAuditStream(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	cfg := audit.Config{
		WebhookURL:    s.db.Setting(ctx, "audit_webhook_url"),
		WebhookSecret: s.box.MustDecrypt(s.db.Setting(ctx, "audit_webhook_secret")),
		Syslog:        s.db.Setting(ctx, "audit_syslog"),
	}
	if !cfg.Enabled() {
		return badRequest("configure a webhook URL or syslog target first")
	}
	u := auth.UserFrom(ctx)
	e := audit.Entry{Time: store.Now(), Action: "audit.test", Target: "Test event from Shipyard", UserID: u.ID, UserEmail: u.Email, UserName: u.Name, IP: auth.ClientIP(r)}
	if err := s.audit.Deliver(ctx, cfg, e); err != nil {
		return badRequest("delivery failed: %v", err)
	}
	return ok(w, map[string]bool{"ok": true})
}
