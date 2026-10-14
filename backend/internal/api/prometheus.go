package api

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/store"
)

// GET /api/metrics serves the latest values in the Prometheus text format, so
// an existing Prometheus (or an OpenTelemetry Collector with its prometheus
// receiver) scrapes PortDock with an API token. Resources are filtered like
// everywhere else: a token limited to some projects only exports those.

// promStale drops samples older than this, so a stopped resource disappears
// from the scrape instead of repeating its last value.
const promStale = 5 * time.Minute

// promSet collects samples by family: the text format needs the samples of a
// family together, after its HELP and TYPE lines.
type promSet struct {
	order []string
	fams  map[string]*promFamily
}

type promFamily struct {
	help, typ string
	samples   []string
}

func newPromSet() *promSet { return &promSet{fams: map[string]*promFamily{}} }

// add records a sample; labels are name, value pairs.
func (p *promSet) add(name, typ, help string, v float64, labels ...string) {
	f := p.fams[name]
	if f == nil {
		f = &promFamily{help: help, typ: typ}
		p.fams[name] = f
		p.order = append(p.order, name)
	}
	var b strings.Builder
	b.WriteString(name)
	if len(labels) > 0 {
		b.WriteByte('{')
		for i := 0; i+1 < len(labels); i += 2 {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(labels[i])
			b.WriteString(`="`)
			b.WriteString(promEscape(labels[i+1]))
			b.WriteByte('"')
		}
		b.WriteByte('}')
	}
	b.WriteByte(' ')
	b.WriteString(promValue(v))
	f.samples = append(f.samples, b.String())
}

func (p *promSet) gauge(name, help string, v float64, labels ...string) {
	p.add(name, "gauge", help, v, labels...)
}

func (p *promSet) WriteTo(w io.Writer) (int64, error) {
	var b strings.Builder
	for _, name := range p.order {
		f := p.fams[name]
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, f.help, name, f.typ)
		for _, s := range f.samples {
			b.WriteString(s)
			b.WriteByte('\n')
		}
	}
	n, err := io.WriteString(w, b.String())
	return int64(n), err
}

func promEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

func promValue(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// promTime converts a stored RFC 3339 time to Unix seconds.
func promTime(s string) (float64, bool) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0, false
	}
	return float64(t.Unix()), true
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// promResource is an application, database or service with its place.
type promResource struct {
	ID          int64  `db:"id"`
	UUID        string `db:"uuid"`
	Name        string `db:"name"`
	Status      string `db:"status"`
	EnvID       int64  `db:"environment_id"`
	ServerID    int64  `db:"server_id"`
	Environment string `db:"environment"`
	Project     string `db:"project"`
	kind        string
}

// labels identifies the resource in every family.
func (r promResource) labels(extra ...string) []string {
	return append([]string{"kind", r.kind, "project", r.Project, "environment", r.Environment, "name", r.Name, "id", strconv.FormatInt(r.ID, 10)}, extra...)
}

func (s *Server) promResources(ctx context.Context, visible func(int64) bool) (map[string]map[int64]promResource, error) {
	out := map[string]map[int64]promResource{}
	for kind, table := range map[string]string{"application": "applications", "database": "databases", "service": "services"} {
		rs, err := store.Select[promResource](ctx, s.db, `SELECT r.id, r.uuid, r.name, r.status, r.environment_id, r.server_id, e.name AS environment, p.name AS project
			FROM `+table+` r JOIN environments e ON e.id = r.environment_id JOIN projects p ON p.id = e.project_id`)
		if err != nil {
			return nil, err
		}
		out[kind] = map[int64]promResource{}
		for _, r := range rs {
			if visible(r.EnvID) {
				r.kind = kind
				out[kind][r.ID] = r
			}
		}
	}
	return out, nil
}

type promSample struct {
	Scope    string  `db:"scope"`
	RefID    int64   `db:"ref_id"`
	ServerID int64   `db:"server_id"`
	CPU      float64 `db:"cpu"`
	Mem      float64 `db:"mem"`
	MemTotal float64 `db:"mem_total"`
	Disk     float64 `db:"disk"`
	DiskTot  float64 `db:"disk_total"`
	RX       float64 `db:"rx"`
	TX       float64 `db:"tx"`
	Load     float64 `db:"load"`
	Restarts float64 `db:"restarts"`
	Up       float64 `db:"up"`
}

func (s *Server) prometheusMetrics(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := newPromSet()
	p.gauge("portdock_build_info", "PortDock version (always 1).", 1, "version", Version)

	visible := s.envVisibility(ctx)
	res, err := s.promResources(ctx, visible)
	if err != nil {
		return err
	}

	servers, err := store.Select[store.Server](ctx, s.db, "SELECT * FROM servers ORDER BY id")
	if err != nil {
		return err
	}
	serverName := map[int64]string{}
	for _, srv := range servers {
		serverName[srv.ID] = srv.Name
		p.gauge("portdock_server_up", "1 when PortDock reaches the server.", boolValue(srv.Status == "reachable"), "server", srv.Name)
		if srv.ProxyEnabled {
			p.gauge("portdock_server_proxy_up", "1 when the server's proxy (Traefik) runs.", boolValue(srv.ProxyStatus == "running"), "server", srv.Name)
		}
	}

	samples, err := store.Select[promSample](ctx, s.db, `SELECT m.scope, m.ref_id, m.server_id, m.cpu, m.mem, m.mem_total, m.disk, m.disk_total, m.rx, m.tx, m.load, m.restarts, m.up
		FROM metrics m JOIN (SELECT scope, ref_id, server_id, MAX(ts) AS ts FROM metrics WHERE ts >= ? GROUP BY scope, ref_id, server_id) l
		ON l.scope = m.scope AND l.ref_id = m.ref_id AND l.server_id = m.server_id AND l.ts = m.ts
		ORDER BY m.scope, m.ref_id, m.server_id`, time.Now().Add(-promStale).Unix())
	if err != nil {
		return err
	}
	for _, m := range samples {
		srv := serverName[m.ServerID]
		if m.Scope == "server" {
			l := []string{"server", srv}
			p.gauge("portdock_server_cpu_percent", "CPU use of the server, percent of all cores.", m.CPU, l...)
			p.gauge("portdock_server_memory_used_bytes", "Memory in use on the server.", m.Mem, l...)
			p.gauge("portdock_server_memory_total_bytes", "Memory of the server.", m.MemTotal, l...)
			p.gauge("portdock_server_disk_used_bytes", "Used space of the server's root file system.", m.Disk, l...)
			p.gauge("portdock_server_disk_total_bytes", "Size of the server's root file system.", m.DiskTot, l...)
			p.gauge("portdock_server_load1", "One-minute load average of the server.", m.Load, l...)
			p.gauge("portdock_server_network_receive_bytes_per_second", "Traffic received by the server's interfaces.", m.RX, l...)
			p.gauge("portdock_server_network_transmit_bytes_per_second", "Traffic sent by the server's interfaces.", m.TX, l...)
			continue
		}
		rs, known := res[m.Scope][m.RefID]
		if !known {
			continue
		}
		l := rs.labels("server", srv)
		p.gauge("portdock_resource_cpu_percent", "CPU use of the resource's containers on a server (100 = one core).", m.CPU, l...)
		p.gauge("portdock_resource_memory_bytes", "Memory used by the resource's containers on a server.", m.Mem, l...)
		if m.MemTotal > 0 {
			p.gauge("portdock_resource_memory_limit_bytes", "Memory limit of the resource's containers on a server.", m.MemTotal, l...)
		}
		p.gauge("portdock_resource_network_receive_bytes_per_second", "Traffic received by the resource's containers.", m.RX, l...)
		p.gauge("portdock_resource_network_transmit_bytes_per_second", "Traffic sent by the resource's containers.", m.TX, l...)
		p.gauge("portdock_resource_restarts", "Container restarts during the last sampling interval (one minute).", m.Restarts, l...)
		p.gauge("portdock_resource_up", "Share of the resource's containers that are running on the server (1 = all).", m.Up, l...)
	}

	for _, kind := range []string{"application", "database", "service"} {
		for _, rs := range sortedResources(res[kind]) {
			p.gauge("portdock_resource_info", "Status of a resource as shown in PortDock (always 1).", 1, rs.labels("status", rs.Status, "server", serverName[rs.ServerID])...)
		}
	}

	if err := s.promDeployments(ctx, p, res["application"]); err != nil {
		return err
	}
	if err := s.promJobs(ctx, p, res["application"]); err != nil {
		return err
	}
	if err := s.promBackups(ctx, p, res["database"]); err != nil {
		return err
	}
	if err := s.promPITR(ctx, p, res["database"]); err != nil {
		return err
	}
	if err := s.promReplicas(ctx, p, res["database"], serverName); err != nil {
		return err
	}
	if err := s.promCertificates(ctx, p, res["application"]); err != nil {
		return err
	}
	if err := s.promMonitors(ctx, p, res["application"]); err != nil {
		return err
	}
	// Alerts span the organization: administrators only, and not through a
	// token limited to some projects (a read-only token still sees them).
	if t := auth.TokenFrom(ctx); (store.User{Role: auth.RealRoleFrom(ctx)}).IsAdmin() && (t == nil || t.ProjectIDs == "") {
		if err := s.promAlerts(ctx, p); err != nil {
			return err
		}
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, err = p.WriteTo(w)
	return err
}

func sortedResources(m map[int64]promResource) []promResource {
	out := make([]promResource, 0, len(m))
	for _, r := range m {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Server) promDeployments(ctx context.Context, p *promSet, apps map[int64]promResource) error {
	type count struct {
		AppID  int64  `db:"application_id"`
		Status string `db:"status"`
		N      int64  `db:"n"`
	}
	counts, err := store.Select[count](ctx, s.db, "SELECT application_id, status, COUNT(*) AS n FROM deployments GROUP BY application_id, status ORDER BY application_id, status")
	if err != nil {
		return err
	}
	for _, c := range counts {
		if a, ok := apps[c.AppID]; ok {
			p.gauge("portdock_deployments", "Deployments recorded for an application, by status.", float64(c.N), a.labels("status", c.Status)...)
		}
	}
	type lastRun struct {
		AppID      int64  `db:"application_id"`
		Status     string `db:"status"`
		StartedAt  string `db:"started_at"`
		FinishedAt string `db:"finished_at"`
		LastOK     string `db:"last_ok"`
	}
	last, err := store.Select[lastRun](ctx, s.db, `SELECT d.application_id, d.status, d.started_at, d.finished_at,
		COALESCE((SELECT MAX(finished_at) FROM deployments o WHERE o.application_id = d.application_id AND o.status = 'success' AND o.pr_number = 0), '') AS last_ok
		FROM deployments d JOIN (SELECT application_id, MAX(id) AS id FROM deployments WHERE finished_at != '' AND pr_number = 0 GROUP BY application_id) l ON l.id = d.id
		ORDER BY d.application_id`)
	if err != nil {
		return err
	}
	for _, d := range last {
		a, ok := apps[d.AppID]
		if !ok {
			continue
		}
		p.gauge("portdock_deployment_last_success", "1 when the last finished production deployment succeeded.", boolValue(d.Status == "success"), a.labels()...)
		start, ok1 := promTime(d.StartedAt)
		end, ok2 := promTime(d.FinishedAt)
		if ok1 && ok2 && end >= start {
			p.gauge("portdock_deployment_last_duration_seconds", "Duration of the last finished production deployment.", end-start, a.labels()...)
		}
		if t, ok := promTime(d.LastOK); ok {
			p.gauge("portdock_deployment_last_success_timestamp_seconds", "When the last successful production deployment finished.", t, a.labels()...)
		}
	}
	return nil
}

func (s *Server) promJobs(ctx context.Context, p *promSet, apps map[int64]promResource) error {
	jobs, err := store.Select[store.AppJob](ctx, s.db, "SELECT * FROM app_jobs ORDER BY id")
	if err != nil {
		return err
	}
	for _, j := range jobs {
		a, ok := apps[j.ApplicationID]
		if !ok {
			continue
		}
		l := []string{"project", a.Project, "environment", a.Environment, "application", a.Name, "job", j.Name}
		p.gauge("portdock_job_enabled", "1 when the scheduled job is enabled.", boolValue(j.Enabled), l...)
		if t, ok := promTime(j.LastRunAt); ok && j.LastStatus != "running" {
			p.gauge("portdock_job_last_run_timestamp_seconds", "When the job last ran.", t, l...)
			p.gauge("portdock_job_last_run_success", "1 when the last run of the job succeeded.", boolValue(j.LastStatus == "success"), l...)
		}
	}
	return nil
}

func (s *Server) promBackups(ctx context.Context, p *promSet, dbs map[int64]promResource) error {
	type run struct {
		DatabaseID int64  `db:"database_id"`
		Status     string `db:"status"`
		FinishedAt string `db:"finished_at"`
		CreatedAt  string `db:"created_at"`
	}
	runs, err := store.Select[run](ctx, s.db, `SELECT b.database_id, r.status, r.finished_at, r.created_at FROM backup_runs r JOIN backups b ON b.id = r.backup_id
		WHERE r.status IN ('success', 'failed') ORDER BY r.id`)
	if err != nil {
		return err
	}
	type state struct {
		lastOK float64
		ok     bool
	}
	latest := map[int64]*state{}
	for _, r := range runs {
		st := latest[r.DatabaseID]
		if st == nil {
			st = &state{}
			latest[r.DatabaseID] = st
		}
		st.ok = r.Status == "success"
		at := r.FinishedAt
		if at == "" {
			at = r.CreatedAt
		}
		if t, valid := promTime(at); valid && st.ok {
			st.lastOK = t
		}
	}
	for _, d := range sortedResources(dbs) {
		st := latest[d.ID]
		if st == nil {
			continue
		}
		l := []string{"project", d.Project, "environment", d.Environment, "database", d.Name}
		p.gauge("portdock_backup_last_run_success", "1 when the last backup of the database succeeded.", boolValue(st.ok), l...)
		if st.lastOK > 0 {
			p.gauge("portdock_backup_last_success_timestamp_seconds", "When the last successful backup of the database finished.", st.lastOK, l...)
		}
	}

	// Test restores: the last one of each database.
	type check struct {
		DatabaseID int64  `db:"database_id"`
		Status     string `db:"verify_status"`
		At         string `db:"verified_at"`
	}
	checks, err := store.Select[check](ctx, s.db, `SELECT b.database_id, r.verify_status, r.verified_at FROM backup_runs r JOIN backups b ON b.id = r.backup_id
		WHERE r.verify_status IN ('success', 'failed') AND r.verified_at != '' ORDER BY r.verified_at, r.id`)
	if err != nil {
		return err
	}
	last := map[int64]check{}
	for _, c := range checks {
		last[c.DatabaseID] = c
	}
	for _, d := range sortedResources(dbs) {
		c, found := last[d.ID]
		if !found {
			continue
		}
		l := []string{"project", d.Project, "environment", d.Environment, "database", d.Name}
		p.gauge("portdock_backup_last_verify_success", "1 when the last test restore of a backup of the database succeeded.", boolValue(c.Status == "success"), l...)
		if t, ok := promTime(c.At); ok {
			p.gauge("portdock_backup_last_verify_timestamp_seconds", "When the last test restore of a backup of the database finished.", t, l...)
		}
	}
	return nil
}

// promPITR exports the WAL archiving and base backups of databases with
// point-in-time recovery.
func (s *Server) promPITR(ctx context.Context, p *promSet, dbs map[int64]promResource) error {
	type row struct {
		ID         int64  `db:"id"`
		ArchivedAt string `db:"pitr_archived_at"`
		Error      string `db:"pitr_archive_error"`
		LastBase   string `db:"last_base"`
	}
	rows, err := store.Select[row](ctx, s.db, `SELECT d.id, d.pitr_archived_at, d.pitr_archive_error,
		COALESCE((SELECT MAX(finished_at) FROM pitr_backups b WHERE b.database_id = d.id AND b.status = 'success'), '') AS last_base
		FROM databases d WHERE d.pitr = 1 ORDER BY d.id`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		d, ok := dbs[r.ID]
		if !ok {
			continue
		}
		l := []string{"project", d.Project, "environment", d.Environment, "database", d.Name}
		p.gauge("portdock_pitr_archive_ok", "1 while WAL archiving of a database with point-in-time recovery works.", boolValue(r.Error == ""), l...)
		if t, ok := promTime(r.ArchivedAt); ok {
			p.gauge("portdock_pitr_last_archived_timestamp_seconds", "When the last WAL segment was archived: the latest time a point-in-time restore reaches.", t, l...)
		}
		if t, ok := promTime(r.LastBase); ok {
			p.gauge("portdock_pitr_last_base_backup_timestamp_seconds", "When the last base backup finished.", t, l...)
		}
	}
	return nil
}

// promReplicas exports the replication of PostgreSQL replicas.
func (s *Server) promReplicas(ctx context.Context, p *promSet, dbs map[int64]promResource, serverName map[int64]string) error {
	rs, err := store.Select[store.DBReplica](ctx, s.db, "SELECT * FROM db_replicas ORDER BY id")
	if err != nil {
		return err
	}
	for _, r := range rs {
		d, ok := dbs[r.DatabaseID]
		if !ok {
			continue
		}
		l := []string{"project", d.Project, "environment", d.Environment, "database", d.Name, "replica", r.UUID, "server", serverName[r.ServerID]}
		p.gauge("portdock_replica_streaming", "1 while the replica streams from its primary.", boolValue(r.Status == "streaming" || r.Status == "lagging"), l...)
		if r.Status == "streaming" || r.Status == "lagging" {
			p.gauge("portdock_replica_lag_bytes", "WAL the replica has not replayed yet.", float64(r.LagBytes), l...)
			p.gauge("portdock_replica_lag_seconds", "Replay delay of the replica.", r.LagSeconds, l...)
		}
	}
	return nil
}

func (s *Server) promCertificates(ctx context.Context, p *promSet, apps map[int64]promResource) error {
	ds, err := store.Select[store.AppDomain](ctx, s.db, "SELECT * FROM app_domains WHERE cert_expires_at != '' ORDER BY id")
	if err != nil {
		return err
	}
	for _, d := range ds {
		a, ok := apps[d.ApplicationID]
		if !ok {
			continue
		}
		if t, ok := promTime(d.CertExpiresAt); ok {
			p.gauge("portdock_certificate_expiry_timestamp_seconds", "When the TLS certificate of a domain expires.", t,
				"project", a.Project, "environment", a.Environment, "application", a.Name, "domain", d.Host+d.Path)
		}
	}
	return nil
}

func (s *Server) promMonitors(ctx context.Context, p *promSet, apps map[int64]promResource) error {
	ms, err := store.Select[store.Monitor](ctx, s.db, "SELECT * FROM monitors ORDER BY id")
	if err != nil {
		return err
	}
	for _, m := range ms {
		if m.ApplicationID != 0 {
			if _, ok := apps[m.ApplicationID]; !ok {
				continue
			}
		}
		l := []string{"monitor", m.Name, "url", m.URL}
		if m.Status == "up" || m.Status == "down" {
			p.gauge("portdock_monitor_up", "1 when the uptime monitor's last checks succeeded.", boolValue(m.Status == "up"), l...)
			p.gauge("portdock_monitor_response_seconds", "Response time of the monitor's last check.", float64(m.LastResponseMs)/1000, l...)
		}
		if t, ok := promTime(m.SSLExpiresAt); ok {
			p.gauge("portdock_monitor_certificate_expiry_timestamp_seconds", "When the TLS certificate seen by the monitor expires.", t, l...)
		}
	}
	return nil
}

func (s *Server) promAlerts(ctx context.Context, p *promSet) error {
	type bySeverity struct {
		Severity string `db:"severity"`
		N        int64  `db:"n"`
	}
	firing, err := store.Select[bySeverity](ctx, s.db, `SELECT r.severity, COUNT(*) AS n FROM alert_events e JOIN alert_rules r ON r.id = e.rule_id
		WHERE e.status = 'firing' GROUP BY r.severity`)
	if err != nil {
		return err
	}
	counts := map[string]int64{"warning": 0, "critical": 0}
	for _, f := range firing {
		counts[f.Severity] = f.N
	}
	for _, sev := range []string{"critical", "warning"} {
		p.gauge("portdock_alerts_firing", "Alerts of PortDock's alert rules that are firing, by severity.", float64(counts[sev]), "severity", sev)
	}
	return nil
}
