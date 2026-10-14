// Package compliance gathers, for a period, the evidence auditors ask for:
// who can access what (accounts, two-factor authentication, roles, API
// tokens), what changed (deployments with their approvals, the audit log),
// how the software was checked (vulnerability, SAST and DAST scans), whether
// data can be recovered (backups and their test restores, point-in-time
// recovery) and how available the services were (incidents, alerts).
package compliance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/store"
)

// Report is the evidence of one period, for every project or one.
type Report struct {
	Instance    string `json:"instance"`
	Version     string `json:"version"`
	GeneratedAt string `json:"generated_at"`
	GeneratedBy string `json:"generated_by"`
	From        string `json:"from"`
	To          string `json:"to"`
	Scope       string `json:"scope"` // "All projects" or the project's name
	ProjectID   int64  `json:"project_id,omitempty"`

	Summary  Summary  `json:"summary"`
	Settings Settings `json:"settings"`

	Users        []UserRow       `json:"users"`
	Tokens       []TokenRow      `json:"tokens"`
	Environments []EnvRow        `json:"environments"`
	Deployments  []DeploymentRow `json:"deployments"`
	Scans        []ScanRow       `json:"scans"`
	Backups      []BackupRow     `json:"backups"`
	Incidents    []IncidentRow   `json:"incidents"`
	Alerts       []AlertRow      `json:"alerts"`
	Audit        []AuditRow      `json:"audit"`
}

// Summary counts what the report contains.
type Summary struct {
	Users, UsersWith2FA, Admins, ServiceAccounts int
	ActiveTokens, TokensWithoutExpiry            int
	Deployments, DeploymentsFailed, Approved     int
	ProtectedEnvironments                        int
	Scans, ScansWithCritical                     int
	Databases, DatabasesBackedUp                 int
	BackupRuns, BackupFailures                   int
	TestRestores, TestRestoreFailures            int
	Incidents                                    int
	AuditEvents                                  int
}

// Settings are the organization-wide security settings.
type Settings struct {
	Require2FA      bool   `json:"require_2fa"`
	PasswordLogin   bool   `json:"password_login"`
	SSOProviders    int    `json:"sso_providers"`
	LDAPDirectories int    `json:"ldap_directories"`
	AuditStreaming  bool   `json:"audit_streaming"`
	UpdateSignature string `json:"update_signature"`
	// SecretStores lists the external secret managers, e.g. "vault-prod (vault)".
	SecretStores []string `json:"secret_stores"`
	// SecretRefs counts the variables read from them.
	SecretRefs int `json:"secret_refs"`
}

type UserRow struct {
	Name           string `db:"name" json:"name"`
	Email          string `db:"email" json:"email"`
	Role           string `db:"role" json:"role"`
	TwoFA          bool   `db:"totp_enabled" json:"two_factor"`
	Source         string `db:"auth_source" json:"source"`
	Disabled       bool   `db:"disabled" json:"disabled"`
	ServiceAccount bool   `db:"service_account" json:"service_account"`
	LastLogin      string `db:"last_login_at" json:"last_login"`
	CreatedAt      string `db:"created_at" json:"created_at"`
}

type TokenRow struct {
	Name      string `db:"name" json:"name"`
	Owner     string `db:"owner" json:"owner"`
	Scope     string `db:"scope" json:"scope"`
	Projects  string `db:"project_ids" json:"projects"`
	ExpiresAt string `db:"expires_at" json:"expires_at"`
	LastUsed  string `db:"last_used_at" json:"last_used"`
	CreatedAt string `db:"created_at" json:"created_at"`
}

type EnvRow struct {
	Project         string `db:"project" json:"project"`
	Environment     string `db:"name" json:"environment"`
	Protected       bool   `db:"protected" json:"protected"`
	RequireApproval bool   `db:"require_approval" json:"require_approval"`
}

type DeploymentRow struct {
	ID          int64  `db:"id" json:"id"`
	Project     string `db:"project" json:"project"`
	Environment string `db:"environment" json:"environment"`
	Application string `db:"application" json:"application"`
	Status      string `db:"status" json:"status"`
	Trigger     string `db:"trigger" json:"trigger"`
	Commit      string `db:"commit_sha" json:"commit"`
	Branch      string `db:"branch" json:"branch"`
	PR          int    `db:"pr_number" json:"pr"`
	RequestedBy string `db:"requested_by" json:"requested_by"`
	ReviewedBy  string `db:"reviewed_by" json:"reviewed_by"`
	ReviewedAt  string `db:"reviewed_at" json:"reviewed_at"`
	Comment     string `db:"review_comment" json:"review_comment"`
	CreatedAt   string `db:"created_at" json:"created_at"`
	FinishedAt  string `db:"finished_at" json:"finished_at"`
}

type ScanRow struct {
	Project     string `db:"project" json:"project"`
	Application string `db:"application" json:"application"`
	Kind        string `db:"kind" json:"kind"`
	Trigger     string `db:"trigger" json:"trigger"`
	Status      string `db:"status" json:"status"`
	Critical    int    `db:"critical" json:"critical"`
	High        int    `db:"high" json:"high"`
	Medium      int    `db:"medium" json:"medium"`
	Low         int    `db:"low" json:"low"`
	Target      string `db:"target" json:"target"`
	CreatedAt   string `db:"created_at" json:"created_at"`
}

type BackupRow struct {
	Project       string `db:"project" json:"project"`
	Environment   string `db:"environment" json:"environment"`
	Database      string `db:"name" json:"database"`
	Engine        string `db:"engine" json:"engine"`
	Schedules     int    `db:"schedules" json:"schedules"`
	Runs          int    `db:"runs" json:"runs"`
	Failed        int    `db:"failed" json:"failed"`
	LastSuccess   string `db:"last_success" json:"last_success"`
	TestRestores  int    `db:"verified" json:"test_restores"`
	TestFailures  int    `db:"verify_failed" json:"test_restore_failures"`
	LastTest      string `db:"last_verified" json:"last_test_restore"`
	PITR          bool   `db:"pitr" json:"point_in_time_recovery"`
	PITRArchiveOK bool   `db:"pitr_ok" json:"pitr_archiving_ok"`
}

type IncidentRow struct {
	Monitor    string `db:"monitor" json:"monitor"`
	URL        string `db:"url" json:"url"`
	StartedAt  string `db:"started_at" json:"started_at"`
	ResolvedAt string `db:"resolved_at" json:"resolved_at"`
	Error      string `db:"error" json:"error"`
}

type AlertRow struct {
	Rule       string `db:"rule" json:"rule"`
	Severity   string `db:"severity" json:"severity"`
	Target     string `db:"target_name" json:"target"`
	Message    string `db:"message" json:"message"`
	StartedAt  string `db:"started_at" json:"started_at"`
	ResolvedAt string `db:"resolved_at" json:"resolved_at"`
}

type AuditRow struct {
	Time   string `db:"created_at" json:"time"`
	User   string `db:"user" json:"user"`
	Action string `db:"action" json:"action"`
	Target string `db:"target" json:"target"`
	IP     string `db:"ip" json:"ip"`
}

// Options select the report.
type Options struct {
	From, To    time.Time // To is exclusive
	ProjectID   int64     // 0: every project
	Instance    string
	Version     string
	GeneratedBy string
}

// Build gathers the report from the database.
func Build(ctx context.Context, db *store.DB, o Options) (*Report, error) {
	from, to := o.From.UTC().Format(time.RFC3339), o.To.UTC().Format(time.RFC3339)
	r := &Report{Instance: o.Instance, Version: o.Version, GeneratedAt: store.Now(), GeneratedBy: o.GeneratedBy,
		From: o.From.UTC().Format("2006-01-02"), To: o.To.UTC().Add(-time.Second).Format("2006-01-02"), Scope: "All projects", ProjectID: o.ProjectID}
	// pf filters a query on the project through its environment column.
	pf, args := "", []any{}
	if o.ProjectID != 0 {
		p, err := store.ByID[store.Project](ctx, db, "projects", o.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("unknown project")
		}
		r.Scope, pf, args = p.Name, " AND p.id = ?", []any{o.ProjectID}
	}
	var err error
	r.Users = load[UserRow](ctx, db, &err, `SELECT name, email, role, totp_enabled, auth_source, disabled, service_account, last_login_at, created_at FROM users ORDER BY role, email`)
	r.Tokens = load[TokenRow](ctx, db, &err, `SELECT t.name, COALESCE(u.email, '') AS owner, t.scope, t.project_ids, t.expires_at, t.last_used_at, t.created_at
		FROM api_tokens t LEFT JOIN users u ON u.id = t.user_id ORDER BY t.created_at`)
	r.Environments = load[EnvRow](ctx, db, &err, `SELECT p.name AS project, e.name, e.protected, e.require_approval FROM environments e JOIN projects p ON p.id = e.project_id WHERE 1 = 1`+pf+` ORDER BY p.name, e.name`, args...)
	r.Deployments = load[DeploymentRow](ctx, db, &err, `SELECT d.id, p.name AS project, e.name AS environment, a.name AS application, d.status, d.trigger, d.commit_sha, d.branch, d.pr_number,
		COALESCE(ru.email, '') AS requested_by, COALESCE(vu.email, '') AS reviewed_by, d.reviewed_at, d.review_comment, d.created_at, d.finished_at
		FROM deployments d JOIN applications a ON a.id = d.application_id JOIN environments e ON e.id = a.environment_id JOIN projects p ON p.id = e.project_id
		LEFT JOIN users ru ON ru.id = d.requested_by LEFT JOIN users vu ON vu.id = d.reviewed_by
		WHERE d.created_at >= ? AND d.created_at < ?`+pf+` ORDER BY d.created_at`, append([]any{from, to}, args...)...)
	r.Scans = load[ScanRow](ctx, db, &err, `SELECT p.name AS project, a.name AS application, s.kind, s.trigger, s.status, s.critical, s.high, s.medium, s.low, s.target, s.created_at
		FROM image_scans s JOIN applications a ON a.id = s.application_id JOIN environments e ON e.id = a.environment_id JOIN projects p ON p.id = e.project_id
		WHERE s.created_at >= ? AND s.created_at < ?`+pf+` ORDER BY s.created_at`, append([]any{from, to}, args...)...)
	r.Backups = load[BackupRow](ctx, db, &err, `SELECT p.name AS project, e.name AS environment, d.name, d.engine,
		(SELECT COUNT(*) FROM backups b WHERE b.database_id = d.id AND b.schedule != '' AND b.enabled = 1) AS schedules,
		(SELECT COUNT(*) FROM backup_runs x JOIN backups b ON b.id = x.backup_id WHERE b.database_id = d.id AND x.created_at >= ? AND x.created_at < ? AND x.status IN ('success', 'failed')) AS runs,
		(SELECT COUNT(*) FROM backup_runs x JOIN backups b ON b.id = x.backup_id WHERE b.database_id = d.id AND x.created_at >= ? AND x.created_at < ? AND x.status = 'failed') AS failed,
		COALESCE((SELECT MAX(x.created_at) FROM backup_runs x JOIN backups b ON b.id = x.backup_id WHERE b.database_id = d.id AND x.status = 'success' AND x.created_at < ?), '') AS last_success,
		(SELECT COUNT(*) FROM backup_runs x JOIN backups b ON b.id = x.backup_id WHERE b.database_id = d.id AND x.verified_at >= ? AND x.verified_at < ? AND x.verify_status IN ('success', 'failed')) AS verified,
		(SELECT COUNT(*) FROM backup_runs x JOIN backups b ON b.id = x.backup_id WHERE b.database_id = d.id AND x.verified_at >= ? AND x.verified_at < ? AND x.verify_status = 'failed') AS verify_failed,
		COALESCE((SELECT MAX(x.verified_at) FROM backup_runs x JOIN backups b ON b.id = x.backup_id WHERE b.database_id = d.id AND x.verify_status = 'success' AND x.verified_at < ?), '') AS last_verified,
		d.pitr, (d.pitr = 1 AND d.pitr_archive_error = '') AS pitr_ok
		FROM databases d JOIN environments e ON e.id = d.environment_id JOIN projects p ON p.id = e.project_id WHERE 1 = 1`+pf+` ORDER BY p.name, e.name, d.name`,
		append([]any{from, to, from, to, to, from, to, from, to, to}, args...)...)
	r.Incidents = load[IncidentRow](ctx, db, &err, `SELECT m.name AS monitor, m.url, i.started_at, i.resolved_at, i.error FROM monitor_incidents i JOIN monitors m ON m.id = i.monitor_id
		WHERE i.started_at >= ? AND i.started_at < ? ORDER BY i.started_at`, from, to)
	r.Alerts = load[AlertRow](ctx, db, &err, `SELECT r.name AS rule, r.severity, e.target_name, e.message, e.started_at, e.resolved_at FROM alert_events e JOIN alert_rules r ON r.id = e.rule_id
		WHERE e.started_at >= ? AND e.started_at < ? ORDER BY e.started_at`, from, to)
	r.Audit = []AuditRow{}
	if o.ProjectID == 0 { // the audit log is organization-wide
		r.Audit = load[AuditRow](ctx, db, &err, `SELECT a.created_at, COALESCE(u.email, 'system') AS user, a.action, a.target, a.ip FROM activity a LEFT JOIN users u ON u.id = a.user_id
			WHERE a.created_at >= ? AND a.created_at < ? ORDER BY a.created_at`, from, to)
	}
	if err != nil {
		return nil, err
	}

	r.Settings = Settings{
		Require2FA:      db.Setting(ctx, "require_2fa") == "true",
		PasswordLogin:   db.Setting(ctx, "password_login") != "false",
		AuditStreaming:  db.Setting(ctx, "audit_webhook_url") != "" || db.Setting(ctx, "audit_syslog") != "",
		UpdateSignature: firstNonEmpty(db.Setting(ctx, "update_verify"), "required"),
	}
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sso_providers WHERE enabled = 1").Scan(&r.Settings.SSOProviders)
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM ldap_directories WHERE enabled = 1").Scan(&r.Settings.LDAPDirectories)
	r.Settings.SecretStores = []string{}
	if rows, err := db.QueryContext(ctx, "SELECT name, type FROM secret_stores ORDER BY name"); err == nil {
		for rows.Next() {
			var name, typ string
			if rows.Scan(&name, &typ) == nil {
				r.Settings.SecretStores = append(r.Settings.SecretStores, name+" ("+typ+")")
			}
		}
		rows.Close()
	}
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM env_vars WHERE ref_store_id > 0").Scan(&r.Settings.SecretRefs)
	r.summarize(o.To)
	return r, nil
}

func (r *Report) summarize(end time.Time) {
	s := &r.Summary
	for _, u := range r.Users {
		if u.Disabled {
			continue
		}
		s.Users++
		if u.TwoFA {
			s.UsersWith2FA++
		}
		if u.Role == "owner" || u.Role == "admin" {
			s.Admins++
		}
		if u.ServiceAccount {
			s.ServiceAccounts++
		}
	}
	endS := end.UTC().Format(time.RFC3339)
	for _, t := range r.Tokens {
		if t.ExpiresAt != "" && t.ExpiresAt < endS {
			continue
		}
		s.ActiveTokens++
		if t.ExpiresAt == "" {
			s.TokensWithoutExpiry++
		}
	}
	for _, e := range r.Environments {
		if e.Protected {
			s.ProtectedEnvironments++
		}
	}
	for _, d := range r.Deployments {
		s.Deployments++
		if d.Status == "failed" {
			s.DeploymentsFailed++
		}
		if d.ReviewedBy != "" {
			s.Approved++
		}
	}
	for _, sc := range r.Scans {
		s.Scans++
		if sc.Critical > 0 {
			s.ScansWithCritical++
		}
	}
	for _, b := range r.Backups {
		s.Databases++
		if b.Runs > 0 {
			s.DatabasesBackedUp++
		}
		s.BackupRuns += b.Runs
		s.BackupFailures += b.Failed
		s.TestRestores += b.TestRestores
		s.TestRestoreFailures += b.TestFailures
	}
	s.Incidents = len(r.Incidents)
	s.AuditEvents = len(r.Audit)
}

// load runs a query unless an earlier one failed (an empty list, never nil).
func load[T any](ctx context.Context, db *store.DB, err *error, query string, args ...any) []T {
	if *err != nil {
		return []T{}
	}
	v, e := store.Select[T](ctx, db, query, args...)
	if e != nil {
		*err = e
		return []T{}
	}
	if v == nil {
		v = []T{}
	}
	return v
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
