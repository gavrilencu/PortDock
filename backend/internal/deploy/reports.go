package deploy

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/store"
)

// Every scan keeps its reports as files on the PortDock server: the report of
// the tool (trivy.json, semgrep.sarif, zap.json, zap.html) and PortDock's own
// report.html, report.csv and findings.json in the same format for every kind.

var kindTitle = map[string]string{
	"image": "Container image vulnerabilities (Trivy)",
	"sast":  "Static application security testing (Semgrep)",
	"dast":  "Dynamic application security testing (OWASP ZAP)",
}

// ReportFile is a downloadable report of a scan.
type ReportFile struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Size  int64  `json:"size"`
}

var reportTitles = map[string]string{
	"report.html":   "HTML report",
	"report.csv":    "CSV",
	"findings.json": "Findings (JSON)",
	"trivy.json":    "Trivy report (JSON)",
	"semgrep.sarif": "Semgrep report (SARIF)",
	"zap.json":      "ZAP report (JSON)",
	"zap.html":      "ZAP report (HTML)",
}

var reportName = regexp.MustCompile(`^[a-z]+\.(html|csv|json|sarif)$`)

func (e *Engine) reportDir(scanID int64) string {
	return filepath.Join(e.cfg.DataDir, "reports", strconv.FormatInt(scanID, 10))
}

// ReportPath returns the file of a report, or "" when the scan has no such report.
func (e *Engine) ReportPath(s store.ImageScan, name string) string {
	if !reportName.MatchString(name) {
		return ""
	}
	for _, n := range store.SplitList(s.Reports) {
		if n == name {
			return filepath.Join(e.reportDir(s.ID), name)
		}
	}
	return ""
}

// Reports lists the reports of a scan with their sizes.
func (e *Engine) Reports(s store.ImageScan) []ReportFile {
	out := []ReportFile{}
	for _, n := range store.SplitList(s.Reports) {
		f := ReportFile{Name: n, Title: reportTitles[n]}
		if st, err := os.Stat(filepath.Join(e.reportDir(s.ID), n)); err == nil {
			f.Size = st.Size()
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return reportOrder(out[i].Name) < reportOrder(out[j].Name) })
	return out
}

func reportOrder(n string) int {
	switch n {
	case "report.html":
		return 0
	case "report.csv":
		return 1
	case "findings.json":
		return 2
	}
	return 3
}

// saveReports writes the reports of a finished scan: the tool's own files and
// PortDock's HTML, CSV and JSON reports of the findings.
func (e *Engine) saveReports(ctx context.Context, app store.Application, s *store.ImageScan, fs []Finding, raw map[string][]byte) {
	dir := e.reportDir(s.ID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return
	}
	files := map[string][]byte{}
	for k, v := range raw {
		if len(v) > 0 {
			files[k] = v
		}
	}
	if b, err := json.MarshalIndent(map[string]any{"scan": scanMeta(app, *s), "findings": fs}, "", "  "); err == nil {
		files["findings.json"] = b
	}
	files["report.csv"] = findingsCSV(fs)
	if h, err := findingsHTML(app, *s, fs); err == nil {
		files["report.html"] = h
	}
	var names []string
	for n, b := range files {
		if os.WriteFile(filepath.Join(dir, n), b, 0o640) == nil {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	s.Reports = strings.Join(names, ",")
}

func scanMeta(app store.Application, s store.ImageScan) map[string]any {
	return map[string]any{
		"application": app.Name, "kind": s.Kind, "tool": toolOf(s.Kind), "status": s.Status,
		"image": s.Image, "service": s.Service, "target": s.Target, "commit": s.CommitSHA, "deployment_id": s.DeploymentID,
		"started_at": s.CreatedAt, "finished_at": s.FinishedAt,
		"summary": map[string]int{"critical": s.Critical, "high": s.High, "medium": s.Medium, "low": s.Low, "info": s.Unknown, "fixable": s.Fixable},
	}
}

func toolOf(kind string) string {
	return map[string]string{"image": "Trivy", "sast": "Semgrep", "dast": "OWASP ZAP"}[kind]
}

func findingsCSV(fs []Finding) []byte {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"severity", "id", "title", "location", "line", "package", "installed", "fixed", "cwe", "count", "url", "description", "solution"})
	for _, f := range fs {
		line, count := "", ""
		if f.Line > 0 {
			line = strconv.Itoa(f.Line)
		}
		if f.Count > 0 {
			count = strconv.Itoa(f.Count)
		}
		_ = w.Write([]string{f.Severity, f.ID, f.Title, f.Target, line, f.Package, f.Installed, f.Fixed, f.CWE, count, f.URL, f.Description, f.Solution})
	}
	w.Flush()
	return append([]byte("\xef\xbb\xbf"), b.Bytes()...) // BOM: Excel opens it as UTF-8
}

var reportTpl = template.Must(template.New("r").Funcs(template.FuncMap{
	"sevClass": func(s string) string { return strings.ToLower(s) },
	"sevLabel": func(s string) string {
		if s == "INFO" || s == "UNKNOWN" {
			return "Info"
		}
		return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
	},
}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} — {{.App}}</title>
<style>
:root{--fg:#18181b;--muted:#71717a;--border:#e4e4e7;--bg:#fff;--soft:#fafafa}
@media (prefers-color-scheme:dark){:root{--fg:#fafafa;--muted:#a1a1aa;--border:#27272a;--bg:#09090b;--soft:#18181b}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:1200px;margin:0 auto;padding:32px 16px}h1{font-size:22px;margin:0 0 4px}.muted{color:var(--muted)}
.meta{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:8px 24px;margin:20px 0;padding:16px;border:1px solid var(--border);border-radius:10px;background:var(--soft)}
.meta div span{display:block;font-size:12px;color:var(--muted)}.meta code{word-break:break-all}
.cards{display:flex;flex-wrap:wrap;gap:10px;margin:0 0 24px}.card{min-width:110px;padding:10px 14px;border:1px solid var(--border);border-radius:10px}
.card b{display:block;font-size:22px}.card.critical b{color:#dc2626}.card.high b{color:#ea580c}.card.medium b{color:#ca8a04}.card.low b{color:#2563eb}
table{width:100%;border-collapse:collapse}th,td{text-align:left;vertical-align:top;padding:8px 10px;border-bottom:1px solid var(--border)}
th{font-size:11px;text-transform:uppercase;letter-spacing:.04em;color:var(--muted)}td code{font-size:12px;word-break:break-all}
.sev{display:inline-block;padding:1px 6px;border-radius:4px;font-size:11px;font-weight:700;text-transform:uppercase;border:1px solid}
.sev.critical{color:#dc2626;border-color:#dc262655}.sev.high{color:#ea580c;border-color:#ea580c55}.sev.medium{color:#ca8a04;border-color:#ca8a0455}
.sev.low{color:#2563eb;border-color:#2563eb55}.sev.info,.sev.unknown{color:var(--muted);border-color:var(--border)}
.small{font-size:12px;color:var(--muted);margin-top:2px}.fixed{color:#16a34a}a{color:inherit}details summary{cursor:pointer;color:var(--muted);font-size:12px}
footer{margin-top:32px;font-size:12px;color:var(--muted)}@media print{a{text-decoration:none}}
</style></head><body><main>
<h1>{{.Title}}</h1><div class="muted">{{.App}}{{if .Scan.Service}} · {{.Scan.Service}}{{end}}</div>
<div class="meta">
{{if .Scan.Image}}<div><span>Image</span><code>{{.Scan.Image}}</code></div>{{end}}
{{if .Scan.Target}}<div><span>Target</span><code>{{.Scan.Target}}</code></div>{{end}}
{{if .Scan.CommitSHA}}<div><span>Commit</span><code>{{.Scan.CommitSHA}}</code></div>{{end}}
{{if .Scan.DeploymentID}}<div><span>Deployment</span>#{{.Scan.DeploymentID}}</div>{{end}}
<div><span>Started</span>{{.Scan.CreatedAt}}</div><div><span>Finished</span>{{.Scan.FinishedAt}}</div>
<div><span>Tool</span>{{.Tool}}</div><div><span>Trigger</span>{{.Scan.Trigger}}</div>
</div>
<div class="cards">
{{if ne .Scan.Kind "dast"}}<div class="card critical"><b>{{.Scan.Critical}}</b>Critical</div>{{end}}
<div class="card high"><b>{{.Scan.High}}</b>High</div><div class="card medium"><b>{{.Scan.Medium}}</b>Medium</div>
<div class="card low"><b>{{.Scan.Low}}</b>Low</div>{{if .Scan.Unknown}}<div class="card"><b>{{.Scan.Unknown}}</b>Info</div>{{end}}
{{if eq .Scan.Kind "image"}}<div class="card"><b>{{.Scan.Fixable}}</b>With a fix</div>{{end}}
</div>
{{if not .Findings}}<p>No findings.</p>{{else}}
<table><thead><tr><th>Severity</th><th>Finding</th>{{if eq .Scan.Kind "image"}}<th>Package</th><th>Installed</th><th>Fixed in</th>{{end}}<th>Location</th></tr></thead><tbody>
{{range .Findings}}<tr><td><span class="sev {{sevClass .Severity}}">{{sevLabel .Severity}}</span></td>
<td>{{if .URL}}<a href="{{.URL}}">{{.ID}}</a>{{else}}{{.ID}}{{end}}{{if .CWE}} · {{.CWE}}{{end}}{{if .Count}} · {{.Count}} instance(s){{end}}
<div>{{.Title}}</div>{{if .Description}}<details><summary>Details</summary><div class="small">{{.Description}}</div>{{if .Solution}}<div class="small"><b>Solution:</b> {{.Solution}}</div>{{end}}</details>{{end}}</td>
{{if eq $.Scan.Kind "image"}}<td><code>{{.Package}}</code></td><td><code>{{.Installed}}</code></td><td>{{if .Fixed}}<code class="fixed">{{.Fixed}}</code>{{else}}—{{end}}</td>{{end}}
<td><code>{{.Target}}{{if .Line}}:{{.Line}}{{end}}</code></td></tr>
{{end}}</tbody></table>{{end}}
<footer>Generated by PortDock on {{.Generated}}.</footer>
</main></body></html>`))

func findingsHTML(app store.Application, s store.ImageScan, fs []Finding) ([]byte, error) {
	var b bytes.Buffer
	err := reportTpl.Execute(&b, map[string]any{
		"Title": kindTitle[s.Kind], "App": app.Name, "Scan": s, "Findings": fs, "Tool": toolOf(s.Kind),
		"Generated": time.Now().UTC().Format("2006-01-02 15:04 UTC"),
	})
	return b.Bytes(), err
}

// removeReports deletes the report files of scans.
func (e *Engine) removeReports(ids ...int64) {
	for _, id := range ids {
		_ = os.RemoveAll(e.reportDir(id))
	}
}

// WriteReportsZip writes the reports of scans into a zip archive, one folder per scan.
func (e *Engine) WriteReportsZip(w io.Writer, scans []store.ImageScan) error {
	z := zip.NewWriter(w)
	for _, s := range scans {
		folder := fmt.Sprintf("%s-%d", s.Kind, s.ID)
		if s.Service != "" {
			folder += "-" + s.Service
		}
		for _, n := range store.SplitList(s.Reports) {
			f, err := os.Open(filepath.Join(e.reportDir(s.ID), n))
			if err != nil {
				continue
			}
			zw, err := z.Create(folder + "/" + n)
			if err == nil {
				_, err = io.Copy(zw, f)
			}
			f.Close()
			if err != nil {
				return err
			}
		}
	}
	return z.Close()
}

var tagRe = regexp.MustCompile(`<[^>]+>`)

// plainText turns the HTML fragments of ZAP descriptions into text.
func plainText(s string) string {
	s = strings.NewReplacer("</p>", "\n", "<br>", "\n", "<br/>", "\n", "</li>", "\n").Replace(s)
	s = tagRe.ReplaceAllString(s, "")
	s = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&", "&quot;", `"`, "&#39;", "'").Replace(s)
	return strings.TrimSpace(s)
}
