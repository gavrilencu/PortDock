package monitor

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
	"github.com/portdock/portdock/internal/stream"
)

const sampleOut = `#cpu
cpu  1000 0 1000 8000 0 0 0 0 0 0
cpu  1100 0 1100 8200 0 0 0 0 0 0
#mem
8000000
2000000
#disk
100000000 85000000
#load
1.50 1.20 1.00 2/300 12345
4
#net
Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 999999 10 0 0 0 0 0 0 999999 10 0 0 0 0 0 0
  eth0: 1000000 10 0 0 0 0 0 0 500000 10 0 0 0 0 0 0
docker0: 777 1 0 0 0 0 0 0 777 1 0 0 0 0 0 0
#ps
aaa|application|app-uuid|running
bbb|application|app-uuid|restarting
ccc|database|db-uuid|running
ddd|service|unknown-uuid|running
#stats
aaa|150.5%|256MiB / 1GiB|1.5MB / 500kB
bbb|0.00%|0B / 1GiB|0B / 0B
ccc|3.25%|100MiB / 7.63GiB|10kB / 20kB
#restarts
aaa 0
bbb 4
ccc 1
#end
`

func TestParse(t *testing.T) {
	s := Parse(sampleOut)
	h := s.Host
	if math.Abs(h.CPU-50) > 0.01 { // 200 busy of 400 total
		t.Errorf("cpu %.2f", h.CPU)
	}
	if h.MemTotal != 8000000*1024 || h.MemUsed != 6000000*1024 || h.DiskUsed != 85000000*1024 || h.Load1 != 1.5 || h.CPUs != 4 {
		t.Errorf("host %+v", h)
	}
	if h.NetRxBytes != 1000000 || h.NetTxBytes != 500000 {
		t.Errorf("net should skip lo/docker0: %v %v", h.NetRxBytes, h.NetTxBytes)
	}
	if len(s.Containers) != 4 {
		t.Fatalf("containers %d", len(s.Containers))
	}
	a := s.Containers[0]
	if a.CPU != 150.5 || a.Mem != 256<<20 || a.MemLimit != 1<<30 || a.NetRx != 1.5e6 || a.NetTx != 500e3 || !a.Running {
		t.Errorf("container a %+v", a)
	}
	if s.Containers[1].Running || s.Containers[1].Restarts != 4 {
		t.Errorf("container b %+v", s.Containers[1])
	}
}

func newTestMonitor(t *testing.T) (*Monitor, *time.Time) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	box, _ := secrets.LoadOrCreate("", dir)
	m := New(db, nil, notify.New(db, box), stream.NewHub())
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	return m, &now
}

func TestRowsRatesAndRestarts(t *testing.T) {
	m, _ := newTestMonitor(t)
	refs := map[string]ref{"app-uuid": {"application", 7}, "db-uuid": {"database", 3}}
	t0 := time.Unix(1_000_000, 0)
	first := m.rows(1, Parse(sampleOut), refs, t0)
	if len(first) != 3 { // server + app + db (unknown resource skipped)
		t.Fatalf("rows %d", len(first))
	}
	app := first[1]
	if app.Scope != "application" || app.RefID != 7 || app.CPU != 150.5 || app.Up != 0 || app.RX != 0 {
		t.Errorf("first app row %+v", app)
	}
	// 60 s later: eth0 +600 kB rx, container aaa +60 kB rx, bbb restarted twice more.
	next := Parse(sampleOut)
	next.Host.NetRxBytes += 600_000
	next.Containers[0].NetRx += 60_000
	next.Containers[1].Restarts = 6
	rows := m.rows(1, next, refs, t0.Add(time.Minute))
	if rows[0].RX != 10_000 {
		t.Errorf("server rx rate %v", rows[0].RX)
	}
	if rows[1].RX != 1000 || rows[1].Restarts != 2 {
		t.Errorf("app rx %v restarts %d", rows[1].RX, rows[1].Restarts)
	}
}

func insertSeries(t *testing.T, m *Monitor, from time.Time, minutes int, row func(i int) Row) {
	var rows []Row
	for i := 0; i < minutes; i++ {
		r := row(i)
		r.TS = from.Add(time.Duration(i) * time.Minute).Unix()
		rows = append(rows, r)
	}
	if err := m.insert(context.Background(), rows); err != nil {
		t.Fatal(err)
	}
}

func TestSeriesAndRollup(t *testing.T) {
	m, now := newTestMonitor(t)
	ctx := context.Background()
	// Two servers running the same app: series must sum them per minute.
	for _, srv := range []int64{1, 2} {
		insertSeries(t, m, now.Add(-2*time.Hour), 120, func(i int) Row {
			return Row{Scope: "application", RefID: 5, ServerID: srv, CPU: 10, Mem: 100, MemTotal: 1000, Up: 1}
		})
	}
	pts, err := m.Series(ctx, "application", 5, "1h")
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) < 59 || pts[0].CPU != 20 || pts[0].Mem != 200 {
		t.Fatalf("1h series: %d points, first %+v", len(pts), pts[0])
	}
	m.rollup(ctx, true)
	var n int
	_ = m.db.QueryRow("SELECT COUNT(*) FROM metrics_hourly WHERE scope = 'application' AND ref_id = 5").Scan(&n)
	if n != 4 { // 2 hours × 2 servers
		t.Fatalf("hourly rows %d", n)
	}
	pts, _ = m.Series(ctx, "application", 5, "7d")
	if len(pts) != 2 || pts[0].CPU != 20 || pts[0].CPUMax != 20 {
		t.Fatalf("7d series %+v", pts)
	}
}

func TestAlertFireAndResolve(t *testing.T) {
	m, now := newTestMonitor(t)
	ctx := context.Background()
	m.SeedDefaultRules(ctx)
	m.SeedDefaultRules(ctx) // idempotent
	var rules int
	_ = m.db.QueryRow("SELECT COUNT(*) FROM alert_rules").Scan(&rules)
	if rules != len(DefaultRules) {
		t.Fatalf("rules %d", rules)
	}
	_, _ = m.db.Exec("INSERT INTO servers(id, name, host) VALUES (1, 'web-1', '10.0.0.1')")

	// 5 minutes at 95% CPU: the 10-minute CPU rule must not fire yet.
	insertSeries(t, m, now.Add(-5*time.Minute), 5, func(int) Row {
		return Row{Scope: "server", RefID: 1, ServerID: 1, CPU: 95, Mem: 10, MemTotal: 100, Disk: 10, DiskTotal: 100, Up: 1}
	})
	m.Evaluate(ctx)
	if n := count(m, "firing"); n != 0 {
		t.Fatalf("fired too early: %d", n)
	}
	// 11 minutes at 95%: fires once, not twice.
	insertSeries(t, m, now.Add(-11*time.Minute), 6, func(int) Row {
		return Row{Scope: "server", RefID: 1, ServerID: 1, CPU: 95, Mem: 10, MemTotal: 100, Disk: 10, DiskTotal: 100, Up: 1}
	})
	m.Evaluate(ctx)
	m.Evaluate(ctx)
	if n := count(m, "firing"); n != 1 {
		t.Fatalf("expected 1 firing alert, got %d", n)
	}
	var target, msg string
	_ = m.db.QueryRow("SELECT target_name, message FROM alert_events").Scan(&target, &msg)
	if target != "web-1" || msg != "CPU 95.0% is above 90% for 10 min" {
		t.Fatalf("event %q %q", target, msg)
	}
	// One minute later CPU drops: a single low sample breaks "every sample above" → resolved.
	*now = now.Add(time.Minute)
	insertSeries(t, m, *now, 1, func(int) Row {
		return Row{Scope: "server", RefID: 1, ServerID: 1, CPU: 20, Mem: 10, MemTotal: 100, Disk: 10, DiskTotal: 100, Up: 1}
	})
	m.Evaluate(ctx)
	if count(m, "firing") != 0 || count(m, "resolved") != 1 {
		t.Fatalf("not resolved: firing=%d resolved=%d", count(m, "firing"), count(m, "resolved"))
	}
}

func TestDownAlertSkipsStoppedApps(t *testing.T) {
	m, now := newTestMonitor(t)
	ctx := context.Background()
	m.SeedDefaultRules(ctx)
	for _, q := range []string{"INSERT INTO servers(id, name, host) VALUES (1, 'web-1', 'h')", "INSERT INTO projects(id, name) VALUES (1, 'p')", "INSERT INTO environments(id, project_id, name) VALUES (1, 1, 'production')"} {
		if _, err := m.db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	app := store.Application{UUID: "u9", Name: "api", Status: "exited", SourceType: "image", BuildPack: "image", Replicas: 1, EnvironmentID: 1, ServerID: 1}
	if _, err := store.Insert(ctx, m.db, "applications", &app); err != nil {
		t.Fatal(err)
	}
	_, _ = m.db.Exec("UPDATE applications SET id = 9 WHERE uuid = 'u9'")
	insertSeries(t, m, now.Add(-4*time.Minute), 4, func(int) Row { return Row{Scope: "application", RefID: 9, ServerID: 1, Up: 0} })
	m.Evaluate(ctx)
	if count(m, "firing") != 0 {
		t.Fatal("down alert fired for a stopped app")
	}
	_, _ = m.db.Exec("UPDATE applications SET status = 'degraded' WHERE id = 9")
	m.Evaluate(ctx)
	if count(m, "firing") != 1 {
		t.Fatal("down alert did not fire for a crashed app")
	}
	// Crash loop: restarts summed over the window.
	insertSeries(t, m, now.Add(-3*time.Minute), 3, func(int) Row { return Row{Scope: "application", RefID: 9, ServerID: 1, Restarts: 2, Up: 1} })
	m.Evaluate(ctx)
	var n int
	_ = m.db.QueryRow("SELECT COUNT(*) FROM alert_events e JOIN alert_rules r ON r.id = e.rule_id WHERE r.metric = 'restarts' AND e.status = 'firing'").Scan(&n)
	if n != 1 {
		t.Fatalf("crash loop alert: %d", n)
	}
}

func count(m *Monitor, status string) int {
	var n int
	_ = m.db.QueryRow("SELECT COUNT(*) FROM alert_events WHERE status = ?", status).Scan(&n)
	return n
}
