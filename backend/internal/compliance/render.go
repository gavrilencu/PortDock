package compliance

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"sort"
	"strconv"
	"strings"
)

// htmlAuditLimit keeps the printable report readable; the CSV has every event.
const htmlAuditLimit = 500

var page = template.Must(template.New("report").Funcs(template.FuncMap{
	"yes":  func(b bool) string { return map[bool]string{true: "yes", false: "no"}[b] },
	"join": strings.Join,
	"dash": func(s string) string { return map[bool]string{true: "—", false: s}[s == ""] },
	"day":  func(s string) string { return strings.Replace(strings.TrimSuffix(s, "Z"), "T", " ", 1) },
	"short": func(s string) string {
		if len(s) > 12 {
			return s[:12]
		}
		return s
	},
	"pct": func(a, b int) string {
		if b == 0 {
			return "—"
		}
		return strconv.Itoa(a*100/b) + " %"
	},
	"first": func(n int, rows []AuditRow) []AuditRow {
		if len(rows) > n {
			return rows[len(rows)-n:]
		}
		return rows
	},
}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Compliance report · {{.Instance}} · {{.From}} – {{.To}}</title>
<style>
:root{--fg:#111;--muted:#555;--line:#ddd;--soft:#f6f6f6;--bad:#b42318;--ok:#067647}
body{font:13px/1.5 system-ui,-apple-system,Segoe UI,Roboto,sans-serif;color:var(--fg);margin:0;background:#fff}
main{max-width:1100px;margin:0 auto;padding:32px 24px}
h1{font-size:24px;margin:0 0 4px}h2{font-size:17px;margin:32px 0 8px;border-bottom:2px solid var(--fg);padding-bottom:4px}
.meta{color:var(--muted)}.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(190px,1fr));gap:8px;margin-top:16px}
.card{border:1px solid var(--line);border-radius:8px;padding:10px 12px}.card b{display:block;font-size:20px}.card span{color:var(--muted);font-size:12px}
table{width:100%;border-collapse:collapse;margin-top:6px;font-size:12px}th,td{text-align:left;padding:5px 6px;border-bottom:1px solid var(--line);vertical-align:top}
th{background:var(--soft);font-weight:600}td.num{text-align:right;font-variant-numeric:tabular-nums}.bad{color:var(--bad);font-weight:600}.ok{color:var(--ok)}
.note{color:var(--muted);font-size:12px}code{font-size:11px}
@media print{main{padding:0}h2{break-after:avoid}tr{break-inside:avoid}.noprint{display:none}}
</style></head><body><main>
<h1>Compliance report</h1>
<div class="meta">{{.Instance}} · PortDock {{.Version}} · {{.Scope}} · period {{.From}} – {{.To}}<br>Generated {{day .GeneratedAt}} UTC by {{.GeneratedBy}}</div>
<p class="noprint note">Print this page or save it as PDF. The ZIP export also holds every table as CSV and a SHA-256 checksum of each file.</p>

<h2>Summary</h2>
<div class="grid">
<div class="card"><b>{{.Summary.Users}}</b><span>active accounts, {{.Summary.Admins}} administrators, {{.Summary.ServiceAccounts}} service accounts</span></div>
<div class="card"><b>{{pct .Summary.UsersWith2FA .Summary.Users}}</b><span>of the accounts use two-factor authentication</span></div>
<div class="card"><b>{{.Summary.ActiveTokens}}</b><span>active API tokens, {{.Summary.TokensWithoutExpiry}} without an expiry date</span></div>
<div class="card"><b>{{.Summary.Deployments}}</b><span>deployments, {{.Summary.DeploymentsFailed}} failed, {{.Summary.Approved}} approved by a reviewer</span></div>
<div class="card"><b>{{.Summary.ProtectedEnvironments}}</b><span>protected environments</span></div>
<div class="card"><b>{{.Summary.Scans}}</b><span>security scans, {{.Summary.ScansWithCritical}} with critical findings</span></div>
<div class="card"><b>{{.Summary.DatabasesBackedUp}} / {{.Summary.Databases}}</b><span>databases backed up in the period</span></div>
<div class="card"><b>{{.Summary.BackupRuns}}</b><span>backups, {{.Summary.BackupFailures}} failed</span></div>
<div class="card"><b>{{.Summary.TestRestores}}</b><span>test restores, {{.Summary.TestRestoreFailures}} failed</span></div>
<div class="card"><b>{{.Summary.Incidents}}</b><span>availability incidents</span></div>
<div class="card"><b>{{.Summary.AuditEvents}}</b><span>audit log events</span></div>
</div>

<h2>Security settings</h2>
<table><tr><th>Setting</th><th>Value</th></tr>
<tr><td>Two-factor authentication required for everyone</td><td>{{yes .Settings.Require2FA}}</td></tr>
<tr><td>Password sign-in</td><td>{{yes .Settings.PasswordLogin}}</td></tr>
<tr><td>Single sign-on providers (OIDC)</td><td>{{.Settings.SSOProviders}}</td></tr>
<tr><td>LDAP / Active Directory directories</td><td>{{.Settings.LDAPDirectories}}</td></tr>
<tr><td>Audit log streamed to a SIEM</td><td>{{yes .Settings.AuditStreaming}}</td></tr>
<tr><td>Signature check of PortDock updates</td><td>{{.Settings.UpdateSignature}}</td></tr>
<tr><td>External secret managers</td><td>{{if .Settings.SecretStores}}{{join .Settings.SecretStores ", "}}; {{.Settings.SecretRefs}} variables read from them{{else}}none{{end}}</td></tr>
</table>

<h2>Accounts</h2>
<table><tr><th>Name</th><th>E-mail</th><th>Role</th><th>2FA</th><th>Sign-in</th><th>Last sign-in</th><th>Status</th></tr>
{{range .Users}}<tr><td>{{.Name}}</td><td>{{.Email}}</td><td>{{.Role}}{{if .ServiceAccount}} (service account){{end}}</td><td class="{{if .TwoFA}}ok{{end}}">{{yes .TwoFA}}</td><td>{{dash .Source}}</td><td>{{dash (day .LastLogin)}}</td><td>{{if .Disabled}}disabled{{else}}active{{end}}</td></tr>{{end}}
</table>

<h2>API tokens</h2>
{{if .Tokens}}<table><tr><th>Name</th><th>Owner</th><th>Scope</th><th>Projects</th><th>Expires</th><th>Last used</th><th>Created</th></tr>
{{range .Tokens}}<tr><td>{{.Name}}</td><td>{{.Owner}}</td><td>{{.Scope}}</td><td>{{if .Projects}}{{.Projects}}{{else}}all{{end}}</td><td class="{{if not .ExpiresAt}}bad{{end}}">{{if .ExpiresAt}}{{day .ExpiresAt}}{{else}}never{{end}}</td><td>{{dash (day .LastUsed)}}</td><td>{{day .CreatedAt}}</td></tr>{{end}}
</table>{{else}}<p class="note">No API tokens.</p>{{end}}

<h2>Environments and approvals</h2>
<table><tr><th>Project</th><th>Environment</th><th>Protected</th><th>Every deployment approved</th></tr>
{{range .Environments}}<tr><td>{{.Project}}</td><td>{{.Environment}}</td><td>{{yes .Protected}}</td><td>{{yes .RequireApproval}}</td></tr>{{end}}
</table>

<h2>Deployments</h2>
{{if .Deployments}}<table><tr><th>#</th><th>Application</th><th>Status</th><th>Trigger</th><th>Commit</th><th>Requested by</th><th>Approved by</th><th>Started</th></tr>
{{range .Deployments}}<tr><td>{{.ID}}</td><td>{{.Project}} / {{.Environment}} / {{.Application}}{{if .PR}} (PR #{{.PR}}){{end}}</td><td class="{{if eq .Status "failed"}}bad{{end}}">{{.Status}}</td><td>{{.Trigger}}</td><td><code>{{short .Commit}}</code></td><td>{{dash .RequestedBy}}</td><td>{{if .ReviewedBy}}{{.ReviewedBy}}<br><span class="note">{{day .ReviewedAt}}{{if .Comment}} · {{.Comment}}{{end}}</span>{{else}}—{{end}}</td><td>{{day .CreatedAt}}</td></tr>{{end}}
</table>{{else}}<p class="note">No deployments in the period.</p>{{end}}

<h2>Security scans</h2>
{{if .Scans}}<table><tr><th>Application</th><th>Kind</th><th>Trigger</th><th>Status</th><th>Critical</th><th>High</th><th>Medium</th><th>Low</th><th>Date</th></tr>
{{range .Scans}}<tr><td>{{.Project}} / {{.Application}}</td><td>{{.Kind}}</td><td>{{.Trigger}}</td><td>{{.Status}}</td><td class="num {{if .Critical}}bad{{end}}">{{.Critical}}</td><td class="num">{{.High}}</td><td class="num">{{.Medium}}</td><td class="num">{{.Low}}</td><td>{{day .CreatedAt}}</td></tr>{{end}}
</table>{{else}}<p class="note">No scans in the period.</p>{{end}}

<h2>Backups and recovery</h2>
{{if .Backups}}<table><tr><th>Database</th><th>Engine</th><th>Schedules</th><th>Backups</th><th>Failed</th><th>Last success</th><th>Test restores</th><th>Last test restore</th><th>Point-in-time recovery</th></tr>
{{range .Backups}}<tr><td>{{.Project}} / {{.Environment}} / {{.Database}}</td><td>{{.Engine}}</td><td class="num {{if not .Schedules}}bad{{end}}">{{.Schedules}}</td><td class="num">{{.Runs}}</td><td class="num {{if .Failed}}bad{{end}}">{{.Failed}}</td><td>{{dash (day .LastSuccess)}}</td><td class="num">{{.TestRestores}}{{if .TestFailures}} <span class="bad">({{.TestFailures}} failed)</span>{{end}}</td><td>{{dash (day .LastTest)}}</td><td>{{if .PITR}}{{if .PITRArchiveOK}}<span class="ok">on</span>{{else}}<span class="bad">archiving failing</span>{{end}}{{else}}off{{end}}</td></tr>{{end}}
</table>{{else}}<p class="note">No databases.</p>{{end}}

<h2>Availability incidents</h2>
{{if .Incidents}}<table><tr><th>Monitor</th><th>URL</th><th>Started</th><th>Resolved</th><th>Error</th></tr>
{{range .Incidents}}<tr><td>{{.Monitor}}</td><td>{{.URL}}</td><td>{{day .StartedAt}}</td><td>{{dash (day .ResolvedAt)}}</td><td>{{.Error}}</td></tr>{{end}}
</table>{{else}}<p class="note">No incidents in the period.</p>{{end}}

<h2>Alerts</h2>
{{if .Alerts}}<table><tr><th>Rule</th><th>Severity</th><th>Target</th><th>Message</th><th>Started</th><th>Resolved</th></tr>
{{range .Alerts}}<tr><td>{{.Rule}}</td><td>{{.Severity}}</td><td>{{.Target}}</td><td>{{.Message}}</td><td>{{day .StartedAt}}</td><td>{{dash (day .ResolvedAt)}}</td></tr>{{end}}
</table>{{else}}<p class="note">No alerts in the period.</p>{{end}}

<h2>Audit log</h2>
{{if .ProjectID}}<p class="note">The audit log covers the whole organization: export the report for all projects to include it.</p>
{{else if .Audit}}{{if gt (len .Audit) 500}}<p class="note">The last 500 of {{len .Audit}} events; audit.csv in the ZIP export has all of them.</p>{{end}}
<table><tr><th>Time (UTC)</th><th>User</th><th>Action</th><th>Target</th><th>IP</th></tr>
{{range (first 500 .Audit)}}<tr><td>{{day .Time}}</td><td>{{.User}}</td><td><code>{{.Action}}</code></td><td>{{.Target}}</td><td>{{dash .IP}}</td></tr>{{end}}
</table>{{else}}<p class="note">No audit events in the period.</p>{{end}}
</main></body></html>
`))

// HTML renders the printable report.
func (r *Report) HTML(w io.Writer) error { return page.Execute(w, r) }

// csvSafe neutralises spreadsheet formula injection (=, +, -, @ prefixes).
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

func writeCSV(header []string, rows [][]string) []byte {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write(header)
	for _, r := range rows {
		for i := range r {
			r[i] = csvSafe(r[i])
		}
		_ = w.Write(r)
	}
	w.Flush()
	return b.Bytes()
}

func b2s(b bool) string { return strconv.FormatBool(b) }
func itoa(n int) string { return strconv.Itoa(n) }

// tables returns every section as CSV files.
func (r *Report) tables() map[string][]byte {
	out := map[string][]byte{}
	var rows [][]string
	for _, u := range r.Users {
		rows = append(rows, []string{u.Name, u.Email, u.Role, b2s(u.TwoFA), u.Source, b2s(u.Disabled), b2s(u.ServiceAccount), u.LastLogin, u.CreatedAt})
	}
	out["users.csv"] = writeCSV([]string{"name", "email", "role", "two_factor", "source", "disabled", "service_account", "last_login", "created_at"}, rows)
	rows = nil
	for _, t := range r.Tokens {
		rows = append(rows, []string{t.Name, t.Owner, t.Scope, t.Projects, t.ExpiresAt, t.LastUsed, t.CreatedAt})
	}
	out["tokens.csv"] = writeCSV([]string{"name", "owner", "scope", "projects", "expires_at", "last_used", "created_at"}, rows)
	rows = nil
	for _, e := range r.Environments {
		rows = append(rows, []string{e.Project, e.Environment, b2s(e.Protected), b2s(e.RequireApproval)})
	}
	out["environments.csv"] = writeCSV([]string{"project", "environment", "protected", "require_approval"}, rows)
	rows = nil
	for _, d := range r.Deployments {
		rows = append(rows, []string{strconv.FormatInt(d.ID, 10), d.Project, d.Environment, d.Application, d.Status, d.Trigger, d.Commit, d.Branch, itoa(d.PR), d.RequestedBy, d.ReviewedBy, d.ReviewedAt, d.Comment, d.CreatedAt, d.FinishedAt})
	}
	out["deployments.csv"] = writeCSV([]string{"id", "project", "environment", "application", "status", "trigger", "commit", "branch", "pr", "requested_by", "reviewed_by", "reviewed_at", "review_comment", "created_at", "finished_at"}, rows)
	rows = nil
	for _, s := range r.Scans {
		rows = append(rows, []string{s.Project, s.Application, s.Kind, s.Trigger, s.Status, itoa(s.Critical), itoa(s.High), itoa(s.Medium), itoa(s.Low), s.Target, s.CreatedAt})
	}
	out["scans.csv"] = writeCSV([]string{"project", "application", "kind", "trigger", "status", "critical", "high", "medium", "low", "target", "created_at"}, rows)
	rows = nil
	for _, b := range r.Backups {
		rows = append(rows, []string{b.Project, b.Environment, b.Database, b.Engine, itoa(b.Schedules), itoa(b.Runs), itoa(b.Failed), b.LastSuccess, itoa(b.TestRestores), itoa(b.TestFailures), b.LastTest, b2s(b.PITR), b2s(b.PITRArchiveOK)})
	}
	out["backups.csv"] = writeCSV([]string{"project", "environment", "database", "engine", "schedules", "backups", "failed", "last_success", "test_restores", "test_restore_failures", "last_test_restore", "pitr", "pitr_archiving_ok"}, rows)
	rows = nil
	for _, i := range r.Incidents {
		rows = append(rows, []string{i.Monitor, i.URL, i.StartedAt, i.ResolvedAt, i.Error})
	}
	out["incidents.csv"] = writeCSV([]string{"monitor", "url", "started_at", "resolved_at", "error"}, rows)
	rows = nil
	for _, a := range r.Alerts {
		rows = append(rows, []string{a.Rule, a.Severity, a.Target, a.Message, a.StartedAt, a.ResolvedAt})
	}
	out["alerts.csv"] = writeCSV([]string{"rule", "severity", "target", "message", "started_at", "resolved_at"}, rows)
	rows = nil
	for _, a := range r.Audit {
		rows = append(rows, []string{a.Time, a.User, a.Action, a.Target, a.IP})
	}
	out["audit.csv"] = writeCSV([]string{"time", "user", "action", "target", "ip"}, rows)
	return out
}

// ZIP writes the report (HTML and JSON), every table as CSV and the
// SHA-256 checksums of the files, so a copy can be checked later.
func (r *Report) ZIP(w io.Writer) error {
	files := r.tables()
	var h bytes.Buffer
	if err := r.HTML(&h); err != nil {
		return err
	}
	files["report.html"] = h.Bytes()
	j, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	files["report.json"] = j
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var sums strings.Builder
	z := zip.NewWriter(w)
	for _, n := range names {
		sum := sha256.Sum256(files[n])
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), n)
		f, err := z.Create(n)
		if err != nil {
			return err
		}
		if _, err := f.Write(files[n]); err != nil {
			return err
		}
	}
	f, err := z.Create("SHA256SUMS")
	if err != nil {
		return err
	}
	if _, err := io.WriteString(f, sums.String()); err != nil {
		return err
	}
	return z.Close()
}
