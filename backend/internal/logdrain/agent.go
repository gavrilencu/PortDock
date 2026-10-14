package logdrain

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
)

const (
	// Image of the agent (Vector), pinned.
	Image         = "timberio/vector:0.58.0-alpine"
	ContainerName = "portdock-logs"
	// LabelConfig fingerprints how the container was started (image, server
	// name, mounts); the configuration itself is reloaded without a restart
	// (SIGHUP), so no line is lost.
	LabelConfig = "portdock.logs-config"
	// recheck is how often a server whose files did not change is checked again
	// (a removed or stopped agent is started again).
	recheck = 10 * time.Minute
)

var q = executor.Quote

// Manager keeps an agent on every reachable server while destinations are
// enabled, and none otherwise.
type Manager struct {
	db      *store.DB
	box     *secrets.Box
	pool    *executor.Pool
	network string
	dataDir string
	trigger chan struct{}

	mu      sync.Mutex
	applied map[int64]applied
}

type applied struct {
	key string // fingerprint of what was applied ("" = no agent)
	at  time.Time
	err string
}

func New(db *store.DB, box *secrets.Box, pool *executor.Pool, network, dataDir string) *Manager {
	return &Manager{db: db, box: box, pool: pool, network: network, dataDir: dataDir,
		trigger: make(chan struct{}, 1), applied: map[int64]applied{}}
}

// Run applies the configuration every minute, and right after Trigger.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		if err := m.Reconcile(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("log drains", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.trigger:
		}
	}
}

// Trigger asks Run to apply the configuration now (destinations changed).
func (m *Manager) Trigger() {
	select {
	case m.trigger <- struct{}{}:
	default:
	}
}

// Targets loads the enabled destinations with their decrypted configuration.
func (m *Manager) Targets(ctx context.Context) ([]Target, error) {
	drains, err := store.Select[store.LogDrain](ctx, m.db, "SELECT * FROM log_drains WHERE enabled = 1 ORDER BY id")
	if err != nil {
		return nil, err
	}
	out := make([]Target, 0, len(drains))
	for _, d := range drains {
		c, err := m.ConfigOf(d)
		if err != nil {
			return nil, fmt.Errorf("destination %q: %w", d.Name, err)
		}
		out = append(out, Target{ID: d.ID, Type: d.Type, URL: d.URL, Config: c, Projects: d.Projects(), IncludeProxy: d.IncludeProxy})
	}
	return out, nil
}

// ConfigOf decrypts the configuration of a destination.
func (m *Manager) ConfigOf(d store.LogDrain) (Config, error) {
	var c Config
	if d.Config == "" {
		return c, nil
	}
	plain, err := m.box.Decrypt(d.Config)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal([]byte(plain), &c)
	return c, err
}

// Resources lists what the agent labels lines with.
func (m *Manager) Resources(ctx context.Context) ([]Resource, error) {
	var out []Resource
	for kind, table := range map[string]string{"application": "applications", "database": "databases", "service": "services"} {
		type row struct {
			UUID        string `db:"uuid"`
			Name        string `db:"name"`
			Environment string `db:"environment"`
			Project     string `db:"project"`
			ProjectID   int64  `db:"project_id"`
		}
		rows, err := store.Select[row](ctx, m.db, `SELECT r.uuid, r.name, e.name AS environment, p.name AS project, p.id AS project_id
			FROM `+table+` r JOIN environments e ON e.id = r.environment_id JOIN projects p ON p.id = e.project_id`)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, Resource{UUID: r.UUID, Kind: kind, Name: r.Name, Project: r.Project, Environment: r.Environment, ProjectID: r.ProjectID})
		}
	}
	return out, nil
}

func (m *Manager) servers(ctx context.Context) ([]store.Server, error) {
	return store.Select[store.Server](ctx, m.db, "SELECT * FROM servers WHERE status = 'reachable' ORDER BY id")
}

// Reconcile starts, reconfigures or removes the agents.
func (m *Manager) Reconcile(ctx context.Context) error {
	targets, err := m.Targets(ctx)
	if err != nil {
		return err
	}
	servers, err := m.servers(ctx)
	if err != nil {
		return err
	}
	var files Files
	if len(targets) > 0 {
		res, err := m.Resources(ctx)
		if err != nil {
			return err
		}
		if files, err = Render(targets, res); err != nil {
			return err
		}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, srv := range servers {
		key := ""
		if len(targets) > 0 {
			key = files.Fingerprint() + "|" + m.containerFingerprint(srv)
		}
		m.mu.Lock()
		cur, known := m.applied[srv.ID]
		m.mu.Unlock()
		if known && cur.err == "" && cur.key == key && time.Since(cur.at) < recheck {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(srv store.Server, key string) {
			defer func() { <-sem; wg.Done() }()
			var err error
			if key == "" {
				err = m.remove(ctx, srv)
			} else {
				err = m.apply(ctx, srv, files)
			}
			st := applied{key: key, at: time.Now()}
			if err != nil {
				st.err = err.Error()
				slog.Warn("log agent", "server", srv.Name, "err", err)
			}
			m.mu.Lock()
			m.applied[srv.ID] = st
			m.mu.Unlock()
		}(srv, key)
	}
	wg.Wait()
	return nil
}

func (m *Manager) containerFingerprint(srv store.Server) string {
	h := sha256.Sum256([]byte(Image + "|" + m.network + "|" + m.dataDir + "|" + srv.Name))
	return hex.EncodeToString(h[:8])
}

func (m *Manager) dir() string { return m.dataDir + "/logs" }

// apply writes the configuration, checked by Vector before it replaces the
// current one, and makes sure the agent runs.
func (m *Manager) apply(ctx context.Context, srv store.Server, f Files) error {
	e, err := m.pool.For(ctx, srv)
	if err != nil {
		return err
	}
	conf := m.dir() + "/config"
	if err := executor.WriteFile(ctx, e, conf+"/"+f.TableName, f.Table, "0600"); err != nil {
		return err
	}
	if err := executor.WriteFile(ctx, e, conf+"/vector-next.yaml", f.Vector, "0600"); err != nil {
		return err
	}
	out, err := executor.Output(ctx, e, fmt.Sprintf("docker run --rm -v %s:%s:ro %s validate --no-environment %s/vector-next.yaml",
		q(conf), confDir, Image, confDir))
	if err != nil {
		return fmt.Errorf("the agent refused the configuration: %s", lastLines(out+err.Error(), 6))
	}
	// Old tables go once the new configuration is in place.
	if _, err := executor.Output(ctx, e, fmt.Sprintf("cd %s && mv vector-next.yaml vector.yaml && find . -maxdepth 1 -name 'resources-*.csv' ! -name %s -delete",
		q(conf), q(f.TableName))); err != nil {
		return err
	}
	fp := m.containerFingerprint(srv)
	cur, _ := executor.Output(ctx, e, "docker inspect --format '{{.State.Running}} {{index .Config.Labels \""+LabelConfig+"\"}}' "+ContainerName+" 2>/dev/null")
	if strings.TrimSpace(cur) == "true "+fp {
		_, err := executor.Output(ctx, e, "docker kill --signal HUP "+ContainerName)
		return err
	}
	if err := docker.EnsureNetwork(ctx, e, m.network); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "mkdir -p %s/data && (docker rm -f %s >/dev/null 2>&1 || true) && ", q(m.dir()), ContainerName)
	fmt.Fprintf(&b, "docker run -d --name %s --restart unless-stopped --network %s ", ContainerName, q(m.network))
	b.WriteString("-v /var/run/docker.sock:/var/run/docker.sock:ro ")
	fmt.Fprintf(&b, "-v %s:%s:ro -v %s/data:/var/lib/vector ", q(conf), confDir, q(m.dir()))
	fmt.Fprintf(&b, "-e PORTDOCK_SERVER=%s ", q(srv.Name))
	fmt.Fprintf(&b, "--label %s=true --label %s=logs --label %s=%s ", docker.LabelManaged, docker.LabelKind, LabelConfig, fp)
	fmt.Fprintf(&b, "%s --config %s/vector.yaml", Image, confDir)
	_, err = executor.Output(ctx, e, b.String())
	return err
}

func (m *Manager) remove(ctx context.Context, srv store.Server) error {
	e, err := m.pool.For(ctx, srv)
	if err != nil {
		return err
	}
	_, err = executor.Output(ctx, e, "docker rm -f "+ContainerName+" >/dev/null 2>&1 || true")
	return err
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ServerStatus is the agent of one server.
type ServerStatus struct {
	ServerID int64  `json:"server_id"`
	Server   string `json:"server"`
	State    string `json:"state"` // running | exited | missing…
	Error    string `json:"error,omitempty"`
	// Destinations: what was sent and the errors since the agent started.
	Destinations map[string]DrainStats `json:"destinations"`
}

type DrainStats struct {
	Sent      float64 `json:"sent"`
	Errors    float64 `json:"errors"`
	Discarded float64 `json:"discarded"`
}

// Status reports the agents of the reachable servers.
func (m *Manager) Status(ctx context.Context) ([]ServerStatus, error) {
	servers, err := m.servers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ServerStatus, len(servers))
	var wg sync.WaitGroup
	for i, srv := range servers {
		wg.Add(1)
		go func(i int, srv store.Server) {
			defer wg.Done()
			st := ServerStatus{ServerID: srv.ID, Server: srv.Name, State: "missing", Destinations: map[string]DrainStats{}}
			m.mu.Lock()
			st.Error = m.applied[srv.ID].err
			m.mu.Unlock()
			e, err := m.pool.For(ctx, srv)
			if err != nil {
				st.Error = err.Error()
				out[i] = st
				return
			}
			if state, _, err := docker.State(ctx, e, ContainerName); err == nil {
				st.State = state
			}
			if st.State == "running" {
				metrics, _ := executor.Output(ctx, e, "docker exec "+ContainerName+" wget -qO- http://"+MetricsAddress+"/metrics")
				st.Destinations = ParseStats(metrics)
			}
			out[i] = st
		}(i, srv)
	}
	wg.Wait()
	return out, nil
}

// ParseStats sums Vector's counters per destination.
func ParseStats(metrics string) map[string]DrainStats {
	out := map[string]DrainStats{}
	sc := bufio.NewScanner(strings.NewReader(metrics))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		name, rest, found := strings.Cut(line, "{")
		if !found {
			continue
		}
		labels, value, found := strings.Cut(rest, "} ")
		if !found {
			continue
		}
		id := labelValue(labels, "component_id")
		if n, isDrain := strings.CutPrefix(id, "drain_"); !isDrain || strings.Trim(n, "0123456789") != "" {
			continue // not a destination (its filter or format transform, PortDock's own components)
		}
		v, err := strconv.ParseFloat(strings.Fields(value)[0], 64)
		if err != nil {
			continue
		}
		s := out[id]
		switch name {
		case "vector_component_sent_events_total":
			s.Sent += v
		case "vector_component_errors_total":
			s.Errors += v
		case "vector_component_discarded_events_total", "vector_buffer_discarded_events_total":
			s.Discarded += v
		default:
			continue
		}
		out[id] = s
	}
	return out
}

func labelValue(labels, key string) string {
	for _, kv := range strings.Split(labels, ",") {
		k, v, _ := strings.Cut(kv, "=")
		if k == key {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

// Test writes one line through every agent: it reaches every destination
// that is not limited to some projects or that includes the test kind.
func (m *Manager) Test(ctx context.Context, message string) ([]string, error) {
	servers, err := m.servers(ctx)
	if err != nil {
		return nil, err
	}
	var sent []string
	for _, srv := range servers {
		e, err := m.pool.For(ctx, srv)
		if err != nil {
			return sent, fmt.Errorf("%s: %w", srv.Name, err)
		}
		if state, _, err := docker.State(ctx, e, ContainerName); err != nil || state != "running" {
			continue
		}
		cmd := fmt.Sprintf("docker run --rm --label %s=true --label %s=test --entrypoint sh %s -c %s",
			docker.LabelManaged, docker.LabelKind, Image, q("echo "+q(message)+"; sleep 3"))
		if _, err := executor.Output(ctx, e, cmd); err != nil {
			return sent, fmt.Errorf("%s: %w", srv.Name, err)
		}
		sent = append(sent, srv.Name)
	}
	sort.Strings(sent)
	return sent, nil
}
