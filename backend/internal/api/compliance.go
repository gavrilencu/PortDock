package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/compliance"
)

// complianceReport exports the evidence of a period for auditors:
// GET /api/compliance/report?from=2026-07-01&to=2026-09-30&project=3&format=html|zip|json
func (s *Server) complianceReport(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	to := time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	from := to.AddDate(0, 0, -30)
	var err error
	if v := q.Get("from"); v != "" {
		if from, err = time.Parse("2006-01-02", v); err != nil {
			return badRequest("from must be a date such as 2026-07-01")
		}
	}
	if v := q.Get("to"); v != "" {
		end, err := time.Parse("2006-01-02", v)
		if err != nil {
			return badRequest("to must be a date such as 2026-09-30")
		}
		to = end.Add(24 * time.Hour) // the last day is included
	}
	if !from.Before(to) {
		return badRequest("the period ends before it starts")
	}
	if to.Sub(from) > 3*366*24*time.Hour {
		return badRequest("the period is limited to three years")
	}
	var project int64
	if v := q.Get("project"); v != "" && v != "0" {
		if project, err = strconv.ParseInt(v, 10, 64); err != nil {
			return badRequest("invalid project")
		}
	}
	u := auth.UserFrom(r.Context())
	rep, err := compliance.Build(r.Context(), s.db, compliance.Options{
		From: from, To: to, ProjectID: project, Version: Version, GeneratedBy: u.Email,
		Instance: firstNonEmpty(s.db.Setting(r.Context(), "instance_name"), "PortDock"),
	})
	if err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "compliance.export", fmt.Sprintf("%s – %s (%s)", rep.From, rep.To, rep.Scope))
	name := fmt.Sprintf("portdock-compliance-%s-%s", rep.From, rep.To)
	switch q.Get("format") {
	case "zip":
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.zip"`)
		return rep.ZIP(w)
	case "json":
		return ok(w, rep)
	default:
		// No script runs in the report: everything is escaped by html/template.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", `inline; filename="`+name+`.html"`)
		return rep.HTML(w)
	}
}
