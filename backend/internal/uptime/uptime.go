// Package uptime checks the monitored URLs from the PortDock server: status
// code, an optional keyword in the body, response time and the expiry of the
// TLS certificate. Consecutive failures open an incident and send
// monitor.down; the first success closes it and sends monitor.up.
package uptime

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/store"
)

const (
	checkRetention    = 7 * 24 * time.Hour
	dayRetention      = 90 // days of daily uptime and incidents
	maxParallelChecks = 20
	maxBodyForKeyword = 2 << 20
)

// Checker runs the checks of every enabled monitor on its interval.
type Checker struct {
	db        *store.DB
	notify    *notify.Notifier
	publicURL func(context.Context) string
	slots     chan struct{}
	running   sync.Map // monitor id → a check is in flight
}

func New(db *store.DB, n *notify.Notifier, publicURL func(context.Context) string) *Checker {
	return &Checker{db: db, notify: n, publicURL: publicURL, slots: make(chan struct{}, maxParallelChecks)}
}

// Run checks the due monitors every few seconds until ctx is done.
func (c *Checker) Run(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	cleanup := time.NewTicker(time.Hour)
	defer cleanup.Stop()
	c.cleanup(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			c.checkDue(ctx)
		case <-cleanup.C:
			c.cleanup(ctx)
		}
	}
}

func due(m store.Monitor, now time.Time) bool {
	last, err := time.Parse(time.RFC3339, m.LastCheckedAt)
	if err != nil {
		return true
	}
	return now.Sub(last) >= time.Duration(max(m.IntervalSeconds, 10))*time.Second-time.Second
}

func (c *Checker) checkDue(ctx context.Context) {
	ms, err := store.Select[store.Monitor](ctx, c.db, "SELECT * FROM monitors WHERE enabled = 1")
	if err != nil {
		return
	}
	now := time.Now()
	for _, m := range ms {
		if !due(m, now) {
			continue
		}
		if _, busy := c.running.LoadOrStore(m.ID, true); busy {
			continue
		}
		go func(m store.Monitor) {
			defer c.running.Delete(m.ID)
			select {
			case c.slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-c.slots }()
			c.CheckNow(ctx, m)
		}(m)
	}
}

// Result is the outcome of one check.
type Result struct {
	OK         bool
	StatusCode int
	ResponseMs int
	Error      string
	CertExpiry time.Time
}

// StatusMatches reports whether code is accepted by a list such as
// "200-399", "200,204" or "2xx,301".
func StatusMatches(expected string, code int) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		expected = "200-399"
	}
	for _, part := range strings.Split(expected, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		switch {
		case part == "":
		case len(part) == 3 && strings.HasSuffix(part, "xx"):
			if d, err := strconv.Atoi(part[:1]); err == nil && code/100 == d {
				return true
			}
		case strings.Contains(part, "-"):
			lo, hi, _ := strings.Cut(part, "-")
			l, err1 := strconv.Atoi(strings.TrimSpace(lo))
			h, err2 := strconv.Atoi(strings.TrimSpace(hi))
			if err1 == nil && err2 == nil && code >= l && code <= h {
				return true
			}
		default:
			if n, err := strconv.Atoi(part); err == nil && n == code {
				return true
			}
		}
	}
	return false
}

// ValidStatus checks the syntax of an expected status list.
func ValidStatus(expected string) error {
	for _, part := range strings.Split(expected, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		ok := false
		switch {
		case len(part) == 3 && strings.HasSuffix(part, "xx"):
			_, err := strconv.Atoi(part[:1])
			ok = err == nil
		case strings.Contains(part, "-"):
			lo, hi, _ := strings.Cut(part, "-")
			l, err1 := strconv.Atoi(strings.TrimSpace(lo))
			h, err2 := strconv.Atoi(strings.TrimSpace(hi))
			ok = err1 == nil && err2 == nil && l <= h
		default:
			_, err := strconv.Atoi(part)
			ok = err == nil
		}
		if !ok {
			return fmt.Errorf("invalid expected status %q: use codes and ranges, e.g. 200-399 or 200,204 or 2xx", part)
		}
	}
	return nil
}

// Probe requests the URL of m once.
func Probe(ctx context.Context, m store.Monitor) Result {
	timeout := time.Duration(max(m.TimeoutSeconds, 1)) * time.Second
	client := &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DisableKeepAlives: true, Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: timeout, ForceAttemptHTTP2: true},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return Result{Error: err.Error()}
	}
	req.Header.Set("User-Agent", "PortDock-Uptime/1.0 (+https://github.com/gavrilencu/PortDock)")
	start := time.Now()
	res, err := client.Do(req)
	r := Result{ResponseMs: int(time.Since(start).Milliseconds())}
	if err != nil {
		r.Error = describe(err)
		return r
	}
	defer res.Body.Close()
	r.StatusCode = res.StatusCode
	if res.TLS != nil && len(res.TLS.PeerCertificates) > 0 {
		r.CertExpiry = res.TLS.PeerCertificates[0].NotAfter
	}
	if !StatusMatches(m.ExpectedStatus, res.StatusCode) {
		r.Error = fmt.Sprintf("HTTP %d %s", res.StatusCode, http.StatusText(res.StatusCode))
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
		return r
	}
	if kw := strings.TrimSpace(m.Keyword); kw != "" {
		body, err := io.ReadAll(io.LimitReader(res.Body, maxBodyForKeyword))
		if err != nil {
			r.Error = "reading the response: " + describe(err)
			return r
		}
		if !strings.Contains(string(body), kw) {
			r.Error = fmt.Sprintf("the response does not contain %q", kw)
			return r
		}
	} else {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	}
	r.OK = true
	return r
}

// describe turns transport errors into short messages.
func describe(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var opErr *net.OpError
	switch {
	case errors.As(err, &dnsErr):
		return "DNS: " + dnsErr.Err + " (" + dnsErr.Name + ")"
	case errors.As(err, &certErr):
		return "TLS certificate: " + certErr.Err.Error()
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Client.Timeout"):
		return "timed out"
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return "connection failed: " + opErr.Err.Error()
	}
	return err.Error()
}

// CheckNow probes m and records the result, updating its state.
func (c *Checker) CheckNow(ctx context.Context, m store.Monitor) Result {
	r := Probe(ctx, m)
	if ctx.Err() != nil {
		return r
	}
	c.record(context.WithoutCancel(ctx), m, r)
	return r
}

func (c *Checker) record(ctx context.Context, m store.Monitor, r Result) {
	now := time.Now().UTC()
	ts := now.Format(time.RFC3339)
	_, _ = store.Insert(ctx, c.db, "monitor_checks", &store.MonitorCheck{MonitorID: m.ID, OK: r.OK, StatusCode: r.StatusCode, ResponseMs: r.ResponseMs, Error: r.Error, CreatedAt: ts})
	failed := 0
	if !r.OK {
		failed = 1
	}
	_, _ = c.db.ExecContext(ctx, `INSERT INTO monitor_days(monitor_id, day, checks, failures, total_ms) VALUES (?, ?, 1, ?, ?)
		ON CONFLICT(monitor_id, day) DO UPDATE SET checks = checks + 1, failures = failures + excluded.failures, total_ms = total_ms + excluded.total_ms`,
		m.ID, now.Format("2006-01-02"), failed, r.ResponseMs)

	status, change := m.Status, m.LastChangeAt
	if status == "paused" {
		status = "pending"
	}
	if r.OK {
		m.ConsecutiveFailures = 0
		if status == "down" {
			c.resolve(ctx, m, ts)
		}
		if status != "up" {
			status, change = "up", ts
		}
	} else {
		m.ConsecutiveFailures++
		if status != "down" && m.ConsecutiveFailures >= max(m.FailuresToAlert, 1) {
			status, change = "down", ts
			c.open(ctx, m, r, ts)
		}
	}
	sslExp, warned := m.SSLExpiresAt, m.SSLWarnedAt
	if !r.CertExpiry.IsZero() {
		sslExp = r.CertExpiry.UTC().Format(time.RFC3339)
		days := int(time.Until(r.CertExpiry).Hours() / 24)
		last, _ := time.Parse(time.RFC3339, warned)
		if m.SSLWarnDays > 0 && days <= m.SSLWarnDays && now.Sub(last) > 24*time.Hour {
			warned = ts
			c.send(ctx, notify.Event{Name: "monitor.ssl_expiring", Title: fmt.Sprintf("%s: the TLS certificate expires in %d days", m.Name, days),
				Message: m.URL, Fields: map[string]string{"Expires": r.CertExpiry.UTC().Format("2006-01-02 15:04 UTC")}})
		}
	}
	_, err := c.db.ExecContext(ctx, `UPDATE monitors SET status = ?, consecutive_failures = ?, last_checked_at = ?, last_change_at = ?,
		last_response_ms = ?, last_status_code = ?, last_error = ?, ssl_expires_at = ?, ssl_warned_at = ? WHERE id = ? AND enabled = 1`,
		status, m.ConsecutiveFailures, ts, change, r.ResponseMs, r.StatusCode, r.Error, sslExp, warned, m.ID)
	if err != nil {
		slog.Warn("uptime: save check", "monitor", m.Name, "err", err)
	}
}

func (c *Checker) open(ctx context.Context, m store.Monitor, r Result, ts string) {
	_, _ = store.Insert(ctx, c.db, "monitor_incidents", &store.MonitorIncident{MonitorID: m.ID, Error: r.Error, StartedAt: ts})
	c.send(ctx, notify.Event{Name: "monitor.down", Title: m.Name + " is down", Message: r.Error,
		Fields: map[string]string{"URL": m.URL, "Failed checks": strconv.Itoa(m.ConsecutiveFailures)}})
}

func (c *Checker) resolve(ctx context.Context, m store.Monitor, ts string) {
	inc, err := store.Get[store.MonitorIncident](ctx, c.db, "SELECT * FROM monitor_incidents WHERE monitor_id = ? AND resolved_at = '' ORDER BY id DESC LIMIT 1", m.ID)
	fields := map[string]string{"URL": m.URL}
	if err == nil {
		_, _ = c.db.ExecContext(ctx, "UPDATE monitor_incidents SET resolved_at = ? WHERE monitor_id = ? AND resolved_at = ''", ts, m.ID)
		if start, err := time.Parse(time.RFC3339, inc.StartedAt); err == nil {
			fields["Down for"] = time.Since(start).Round(time.Second).String()
		}
	}
	c.send(ctx, notify.Event{Name: "monitor.up", Title: m.Name + " is back up", Success: true, Fields: fields})
}

func (c *Checker) send(ctx context.Context, ev notify.Event) {
	if c.notify == nil {
		return
	}
	if c.publicURL != nil {
		ev.URL = c.publicURL(ctx) + "/uptime"
	}
	c.notify.Send(ev)
}

func (c *Checker) cleanup(ctx context.Context) {
	now := time.Now().UTC()
	_, _ = c.db.ExecContext(ctx, "DELETE FROM monitor_checks WHERE created_at < ?", now.Add(-checkRetention).Format(time.RFC3339))
	_, _ = c.db.ExecContext(ctx, "DELETE FROM monitor_days WHERE day < ?", now.AddDate(0, 0, -dayRetention).Format("2006-01-02"))
	_, _ = c.db.ExecContext(ctx, "DELETE FROM monitor_incidents WHERE resolved_at != '' AND resolved_at < ?", now.AddDate(0, 0, -dayRetention).Format(time.RFC3339))
}

// Day is the uptime of a monitor on one day.
type Day struct {
	Day      string   `json:"day"`
	Checks   int      `json:"checks"`
	Failures int      `json:"failures"`
	Uptime   *float64 `json:"uptime"` // nil: no data
	AvgMs    int      `json:"avg_ms"`
}

// Days returns the last n days of a monitor, oldest first, including days without checks.
func Days(ctx context.Context, db *store.DB, monitorID int64, n int) []Day {
	type row struct {
		Day      string `db:"day"`
		Checks   int    `db:"checks"`
		Failures int    `db:"failures"`
		TotalMs  int    `db:"total_ms"`
	}
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -(n - 1)).Format("2006-01-02")
	rows, _ := store.Select[row](ctx, db, "SELECT day, checks, failures, total_ms FROM monitor_days WHERE monitor_id = ? AND day >= ?", monitorID, from)
	by := map[string]row{}
	for _, r := range rows {
		by[r.Day] = r
	}
	out := make([]Day, 0, n)
	for i := n - 1; i >= 0; i-- {
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		day := Day{Day: d}
		if r, ok := by[d]; ok && r.Checks > 0 {
			up := 100 * float64(r.Checks-r.Failures) / float64(r.Checks)
			day.Checks, day.Failures, day.Uptime, day.AvgMs = r.Checks, r.Failures, &up, r.TotalMs/r.Checks
		}
		out = append(out, day)
	}
	return out
}

// Uptime is the share of successful checks over days (nil without data).
func Uptime(days []Day) *float64 {
	checks, failures := 0, 0
	for _, d := range days {
		checks += d.Checks
		failures += d.Failures
	}
	if checks == 0 {
		return nil
	}
	u := 100 * float64(checks-failures) / float64(checks)
	return &u
}

// Uptime24h is the share of successful checks in the last 24 hours (nil without data).
func Uptime24h(ctx context.Context, db *store.DB, monitorID int64) *float64 {
	var total, ok int
	since := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(ok), 0) FROM monitor_checks WHERE monitor_id = ? AND created_at >= ?", monitorID, since).Scan(&total, &ok)
	if total == 0 {
		return nil
	}
	u := 100 * float64(ok) / float64(total)
	return &u
}
