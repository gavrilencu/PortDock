package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/shipyard/shipyard/internal/executor"
	"github.com/shipyard/shipyard/internal/notify"
	"github.com/shipyard/shipyard/internal/store"
	"github.com/shipyard/shipyard/internal/stream"
)

const (
	Interval      = time.Minute
	rawRetention  = 48 * time.Hour
	hourRetention = 90 * 24 * time.Hour
)

// Monitor collects metrics and evaluates alert rules.
type Monitor struct {
	db     *store.DB
	pool   *executor.Pool
	notify *notify.Notifier
	hub    *stream.Hub

	mu         sync.Mutex
	prev       map[int64]*snapshot // per server
	lastRollup int64
	now        func() time.Time
}

// snapshot keeps cumulative counters of the previous sample to compute rates.
type snapshot struct {
	at         time.Time
	rx, tx     float64
	containers map[string]ContainerSample
}

func New(db *store.DB, pool *executor.Pool, n *notify.Notifier, hub *stream.Hub) *Monitor {
	return &Monitor{db: db, pool: pool, notify: n, hub: hub, prev: map[int64]*snapshot{}, now: time.Now}
}

// Run samples every Interval until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) {
	m.SeedDefaultRules(ctx)
	m.rollup(ctx, true)
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		m.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick runs one collection + evaluation cycle.
func (m *Monitor) Tick(ctx context.Context) {
	servers, err := store.Select[store.Server](ctx, m.db, "SELECT * FROM servers WHERE status = 'reachable'")
	if err != nil {
		return
	}
	refs := m.resourceIndex(ctx)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, srv := range servers {
		wg.Add(1)
		go func(srv store.Server) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := m.collect(ctx, srv, refs); err != nil {
				slog.Debug("metrics", "server", srv.Name, "err", err)
			}
		}(srv)
	}
	wg.Wait()
	m.Evaluate(ctx)
	m.rollup(ctx, false)
}

type ref struct {
	scope string
	id    int64
}

// resourceIndex maps resource UUIDs to their scope and id.
func (m *Monitor) resourceIndex(ctx context.Context) map[string]ref {
	out := map[string]ref{}
	for _, t := range []struct{ table, scope string }{{"applications", "application"}, {"databases", "database"}, {"services", "service"}} {
		type row struct {
			ID   int64  `db:"id"`
			UUID string `db:"uuid"`
		}
		rows, err := store.Select[row](ctx, m.db, "SELECT id, uuid FROM "+t.table)
		if err != nil {
			continue
		}
		for _, r := range rows {
			out[r.UUID] = ref{t.scope, r.ID}
		}
	}
	return out
}

// Row is one stored sample.
type Row struct {
	TS, RefID, ServerID int64
	Scope               string
	CPU, Mem, MemTotal  float64
	Disk, DiskTotal     float64
	RX, TX, Load        float64
	Restarts            int
	Up                  float64
}

func (m *Monitor) collect(ctx context.Context, srv store.Server, refs map[string]ref) error {
	x, err := m.pool.For(ctx, srv)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	out, err := executor.Output(cctx, x, sampleScript)
	if err != nil && out == "" {
		return err
	}
	rows := m.rows(srv.ID, Parse(out), refs, m.now())
	return m.insert(ctx, rows)
}

// rows converts a sample into stored rows, using the previous sample of the
// server for network rates and restart deltas.
func (m *Monitor) rows(serverID int64, s Sample, refs map[string]ref, now time.Time) []Row {
	m.mu.Lock()
	prev := m.prev[serverID]
	cur := &snapshot{at: now, rx: s.Host.NetRxBytes, tx: s.Host.NetTxBytes, containers: map[string]ContainerSample{}}
	m.prev[serverID] = cur
	m.mu.Unlock()

	ts := now.Unix()
	rate := func(curV, prevV float64) float64 {
		if prev == nil || curV < prevV {
			return 0 // first sample or counter reset
		}
		dt := now.Sub(prev.at).Seconds()
		if dt <= 0 {
			return 0
		}
		return (curV - prevV) / dt
	}
	var prx, ptx float64
	if prev != nil {
		prx, ptx = prev.rx, prev.tx
	}
	out := []Row{{TS: ts, Scope: "server", RefID: serverID, ServerID: serverID, CPU: s.Host.CPU, Mem: s.Host.MemUsed, MemTotal: s.Host.MemTotal,
		Disk: s.Host.DiskUsed, DiskTotal: s.Host.DiskTotal, RX: rate(s.Host.NetRxBytes, prx), TX: rate(s.Host.NetTxBytes, ptx), Load: s.Host.Load1, Up: 1}}

	agg := map[ref]*Row{}
	var order []ref
	for _, c := range s.Containers {
		cur.containers[c.ID] = c
		r, ok := refs[c.Resource]
		if !ok {
			continue
		}
		row := agg[r]
		if row == nil {
			row = &Row{TS: ts, Scope: r.scope, RefID: r.id, ServerID: serverID, Up: 1}
			agg[r] = row
			order = append(order, r)
		}
		row.CPU += c.CPU
		row.Mem += c.Mem
		row.MemTotal += c.MemLimit
		if !c.Running {
			row.Up = 0
		}
		if prev != nil {
			if p, seen := prev.containers[c.ID]; seen {
				row.RX += rate(c.NetRx, p.NetRx)
				row.TX += rate(c.NetTx, p.NetTx)
				if d := c.Restarts - p.Restarts; d > 0 {
					row.Restarts += d
				}
			} else {
				row.Restarts += c.Restarts // restarted before we first saw it
			}
		}
	}
	for _, r := range order {
		out = append(out, *agg[r])
	}
	return out
}

func (m *Monitor) insert(ctx context.Context, rows []Row) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO metrics(ts, scope, ref_id, server_id, cpu, mem, mem_total, disk, disk_total, rx, tx, load, restarts, up)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.ExecContext(ctx, r.TS, r.Scope, r.RefID, r.ServerID, r.CPU, r.Mem, r.MemTotal, r.Disk, r.DiskTotal, r.RX, r.TX, r.Load, r.Restarts, r.Up); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// rollup aggregates completed hours into metrics_hourly and applies retention.
// On startup (all=true) the whole raw window is (re)rolled up; it is idempotent.
func (m *Monitor) rollup(ctx context.Context, all bool) {
	hour := m.now().Unix() / 3600 * 3600
	m.mu.Lock()
	if !all && m.lastRollup == hour {
		m.mu.Unlock()
		return
	}
	m.lastRollup = hour
	m.mu.Unlock()
	from := hour - 3600
	if all {
		from = hour - int64(rawRetention.Seconds())
	}
	_, err := m.db.ExecContext(ctx, `INSERT OR REPLACE INTO metrics_hourly
		(ts, scope, ref_id, server_id, cpu, cpu_max, mem, mem_max, mem_total, disk, disk_total, rx, tx, load, restarts, up)
		SELECT ts/3600*3600, scope, ref_id, server_id, avg(cpu), max(cpu), avg(mem), max(mem), avg(mem_total), avg(disk), avg(disk_total),
		       avg(rx), avg(tx), avg(load), sum(restarts), avg(up)
		FROM metrics WHERE ts >= ? AND ts < ? GROUP BY ts/3600, scope, ref_id, server_id`, from, hour)
	if err != nil {
		slog.Warn("metrics rollup", "err", err)
	}
	now := m.now()
	_, _ = m.db.ExecContext(ctx, "DELETE FROM metrics WHERE ts < ?", now.Add(-rawRetention).Unix())
	_, _ = m.db.ExecContext(ctx, "DELETE FROM metrics_hourly WHERE ts < ?", now.Add(-hourRetention).Unix())
	_, _ = m.db.ExecContext(ctx, "DELETE FROM alert_events WHERE status = 'resolved' AND resolved_at < ?", now.Add(-hourRetention).UTC().Format(time.RFC3339))
}

// ---- queries ----

// Point is one bucket of a time series.
type Point struct {
	T         int64   `json:"t"`
	CPU       float64 `json:"cpu"`
	CPUMax    float64 `json:"cpu_max"`
	Mem       float64 `json:"mem"`
	MemTotal  float64 `json:"mem_total"`
	Disk      float64 `json:"disk"`
	DiskTotal float64 `json:"disk_total"`
	RX        float64 `json:"rx"`
	TX        float64 `json:"tx"`
	Load      float64 `json:"load"`
	Restarts  int     `json:"restarts"`
	Up        float64 `json:"up"`
}

// Ranges maps a range key to its span and bucket size.
var Ranges = map[string]struct {
	Span   time.Duration
	Bucket int64
	Hourly bool
}{
	"1h":  {time.Hour, 60, false},
	"6h":  {6 * time.Hour, 300, false},
	"24h": {24 * time.Hour, 900, false},
	"7d":  {7 * 24 * time.Hour, 3600, true},
	"30d": {30 * 24 * time.Hour, 4 * 3600, true},
}

// Series returns bucketed points for one resource, summed across its servers.
func (m *Monitor) Series(ctx context.Context, scope string, id int64, rangeKey string) ([]Point, error) {
	rg, ok := Ranges[rangeKey]
	if !ok {
		return nil, fmt.Errorf("unknown range %q", rangeKey)
	}
	since := m.now().Add(-rg.Span).Unix()
	table, cpuMax, memMax, slot := "metrics", "cpu", "mem", int64(60)
	if rg.Hourly {
		table, cpuMax, memMax, slot = "metrics_hourly", "cpu_max", "mem_max", 3600
	}
	// Inner query: one value per time slot summed over servers; outer: average per bucket.
	q := fmt.Sprintf(`SELECT b, avg(cpu), max(cpu_max), avg(mem), avg(mem_total), avg(disk), avg(disk_total), avg(rx), avg(tx), avg(load), sum(restarts), min(up)
		FROM (SELECT (ts/%[1]d)*%[1]d AS b, ts/%[2]d AS slot, sum(cpu) AS cpu, sum(%[3]s) AS cpu_max, sum(mem) AS mem, sum(%[4]s) AS mem_max,
		             sum(mem_total) AS mem_total, max(disk) AS disk, max(disk_total) AS disk_total, sum(rx) AS rx, sum(tx) AS tx,
		             max(load) AS load, sum(restarts) AS restarts, min(up) AS up
		      FROM %[5]s WHERE scope = ? AND ref_id = ? AND ts >= ? GROUP BY slot)
		GROUP BY b ORDER BY b`, rg.Bucket, slot, cpuMax, memMax, table)
	rows, err := m.db.QueryContext(ctx, q, scope, id, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Point{}
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.T, &p.CPU, &p.CPUMax, &p.Mem, &p.MemTotal, &p.Disk, &p.DiskTotal, &p.RX, &p.TX, &p.Load, &p.Restarts, &p.Up); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
