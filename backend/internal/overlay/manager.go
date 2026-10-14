package overlay

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
)

// ContainerName is the agent container on every server of the network.
const ContainerName = "portdock-wg"

const labelConfig = "portdock.overlay-config"

var q = executor.Quote

// Manager keeps the agent and its configuration on every server that joins
// the private network, and none on the others.
type Manager struct {
	db      *store.DB
	box     *secrets.Box
	pool    *executor.Pool
	dataDir string
	version string
	trigger chan struct{}

	mu      sync.Mutex
	applied map[int64]applied
	image   string
}

type applied struct {
	key string // fingerprint of what was applied ("" = no agent)
	at  time.Time
	err string
}

// New returns a manager; version names PortDock's image when it cannot
// find the image it runs from.
func New(db *store.DB, box *secrets.Box, pool *executor.Pool, dataDir, version string) *Manager {
	return &Manager{db: db, box: box, pool: pool, dataDir: dataDir, version: version, trigger: make(chan struct{}, 1), applied: map[int64]applied{}}
}

// Run applies the network every minute, and right after Trigger.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		if err := m.Reconcile(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("private network", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.trigger:
		}
	}
}

// Trigger asks Run to apply the network now.
func (m *Manager) Trigger() {
	select {
	case m.trigger <- struct{}{}:
	default:
	}
}

func (m *Manager) dir() string { return strings.TrimRight(m.dataDir, "/") + "/overlay" }

// Settings reads the settings of the network.
func (m *Manager) Settings(ctx context.Context) (Settings, error) {
	return ParseSettings(func(k string) string { return m.db.Setting(ctx, k) })
}

// Prepare gives every joining server an address and keys, and returns the
// nodes of the network.
func (m *Manager) Prepare(ctx context.Context, s Settings) ([]Node, error) {
	servers, err := store.Select[store.Server](ctx, m.db, "SELECT * FROM servers ORDER BY id")
	if err != nil {
		return nil, err
	}
	var used []string
	for _, srv := range servers {
		if srv.OverlayJoin && srv.OverlayIP != "" && s.Subnet.Contains(mustAddr(srv.OverlayIP)) {
			used = append(used, srv.OverlayIP)
		}
	}
	var nodes []Node
	for _, srv := range servers {
		if !srv.OverlayJoin {
			continue
		}
		changed := false
		if srv.OverlayIP == "" || !s.Subnet.Contains(mustAddr(srv.OverlayIP)) {
			ip, err := NextIP(s.Subnet, used)
			if err != nil {
				return nil, err
			}
			srv.OverlayIP, used, changed = ip, append(used, ip), true
		}
		if srv.OverlayPrivateKey == "" || srv.OverlayPublicKey == "" {
			priv, pub, err := NewKeys()
			if err != nil {
				return nil, err
			}
			srv.OverlayPrivateKey, srv.OverlayPublicKey, changed = m.box.Encrypt(priv), pub, true
		}
		if changed {
			if _, err := m.db.ExecContext(ctx, "UPDATE servers SET overlay_ip = ?, overlay_public_key = ?, overlay_private_key = ? WHERE id = ?",
				srv.OverlayIP, srv.OverlayPublicKey, srv.OverlayPrivateKey, srv.ID); err != nil {
				return nil, err
			}
		}
		nodes = append(nodes, Node{ID: srv.ID, Name: srv.Name, IP: srv.OverlayIP, PublicKey: srv.OverlayPublicKey, Endpoint: Endpoint(srv.Host, srv.OverlayEndpoint, s.Port)})
	}
	return nodes, nil
}

// Reconcile applies the network on every reachable server.
func (m *Manager) Reconcile(ctx context.Context) error {
	s, err := m.Settings(ctx)
	if err != nil {
		return err
	}
	var nodes []Node
	if s.Enabled {
		if nodes, err = m.Prepare(ctx, s); err != nil {
			return err
		}
	}
	servers, err := store.Select[store.Server](ctx, m.db, "SELECT * FROM servers WHERE status = 'reachable'")
	if err != nil {
		return err
	}
	image, imageID := "", ""
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, srv := range servers {
		joined := s.Enabled && srv.OverlayJoin && srv.OverlayIP != ""
		key, conf, address := "", "", ""
		if joined {
			if image == "" {
				image = m.Image(ctx)
				// The same tag can carry a newer build: the agent follows the image itself.
				imageID, _ = executor.Output(ctx, executor.Local{}, "docker image inspect -f '{{.Id}}' "+q(image)+" 2>/dev/null")
			}
			priv, err := m.box.Decrypt(srv.OverlayPrivateKey)
			if err != nil {
				m.record(srv.ID, "", err)
				continue
			}
			conf = Config(Node{ID: srv.ID}, priv, s.Port, nodes)
			address = fmt.Sprintf("%s/%d", srv.OverlayIP, s.Subnet.Bits())
			key = fmt.Sprintf("%x", sha256.Sum256([]byte(conf+address+image+imageID)))
		}
		m.mu.Lock()
		last, seen := m.applied[srv.ID]
		m.mu.Unlock()
		if seen && last.key == key && last.err == "" && (key == "" || time.Since(last.at) < 10*time.Minute) {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(srv store.Server) {
			defer func() { <-sem; wg.Done() }()
			sctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()
			x, err := m.pool.For(sctx, srv)
			if err == nil {
				if key == "" {
					err = m.remove(sctx, x)
				} else {
					err = m.apply(sctx, x, srv, conf, address, image, imageID)
				}
			}
			if err != nil {
				slog.Warn("private network", "server", srv.Name, "err", err)
			}
			m.record(srv.ID, key, err)
		}(srv)
	}
	wg.Wait()
	return nil
}

func (m *Manager) record(id int64, key string, err error) {
	a := applied{key: key, at: time.Now()}
	if err != nil {
		a.err = err.Error()
	}
	m.mu.Lock()
	m.applied[id] = a
	m.mu.Unlock()
}

func (m *Manager) remove(ctx context.Context, x executor.Executor) error {
	// Stopping the agent removes pd0.
	return x.Run(ctx, "docker rm -f "+ContainerName+" >/dev/null 2>&1 || true", executor.RunOpts{})
}

func (m *Manager) apply(ctx context.Context, x executor.Executor, srv store.Server, conf, address, image, imageID string) error {
	dir := m.dir()
	if err := executor.WriteFile(ctx, x, dir+"/"+ConfigFile, []byte(conf), "600"); err != nil {
		return fmt.Errorf("write the configuration: %w", err)
	}
	if err := executor.WriteFile(ctx, x, dir+"/"+AddressFile, []byte(address+"\n"), "600"); err != nil {
		return err
	}
	// The agent reloads a changed configuration by itself; the container
	// changes only with the image.
	fp := fmt.Sprintf("%x", sha256.Sum256([]byte(image+imageID)))[:16]
	if out, err := executor.Output(ctx, x, fmt.Sprintf("docker inspect -f '{{.State.Running}} {{index .Config.Labels %q}}' %s 2>/dev/null", labelConfig, ContainerName)); err == nil && out == "true "+fp {
		return nil
	}
	if err := m.ensureImage(ctx, x, image, imageID); err != nil {
		return err
	}
	_ = x.Run(ctx, "docker rm -f "+ContainerName+" >/dev/null 2>&1", executor.RunOpts{})
	cmd := fmt.Sprintf("docker run -d --name %s --restart unless-stopped --network host --cap-add NET_ADMIN -v %s:%s:ro -v /var/run/docker.sock:/var/run/docker.sock "+
		"--label portdock.managed=true --label portdock.kind=overlay --label %s=%s %s wg-agent",
		ContainerName, q(dir), AgentDir, labelConfig, fp, q(image))
	if _, err := executor.Output(ctx, x, cmd); err != nil {
		return fmt.Errorf("start the agent: %w", err)
	}
	return nil
}

// Image is PortDock's own image, which carries the agent: PORTDOCK_IMAGE,
// else the image of the container PortDock runs in, else the release.
func (m *Manager) Image(ctx context.Context) string {
	if v := strings.TrimSpace(os.Getenv("PORTDOCK_IMAGE")); v != "" {
		return v
	}
	m.mu.Lock()
	cached := m.image
	m.mu.Unlock()
	if cached != "" {
		return cached
	}
	img := "ghcr.io/gavrilencu/portdock:" + m.version
	if host, err := os.Hostname(); err == nil {
		if out, err := executor.Output(ctx, executor.Local{}, "docker inspect -f '{{.Config.Image}}' "+q(host)+" 2>/dev/null"); err == nil && out != "" {
			img = out
		}
	}
	m.mu.Lock()
	m.image = img
	m.mu.Unlock()
	return img
}

// ensureImage makes the image available on a server: already there, pulled,
// or streamed from the server PortDock runs on (networks without a registry).
func (m *Manager) ensureImage(ctx context.Context, x executor.Executor, image, id string) error {
	current := func() bool {
		got, err := executor.Output(ctx, x, "docker image inspect -f '{{.Id}}' "+q(image)+" 2>/dev/null")
		return err == nil && (id == "" || got == id)
	}
	if current() {
		return nil
	}
	if _, err := executor.Output(ctx, x, "docker pull -q "+q(image)); err == nil && current() {
		return nil
	}
	if _, ok := x.(executor.Local); ok {
		return fmt.Errorf("the image %s is not available", image)
	}
	pr, pw := io.Pipe()
	errc := make(chan error, 1)
	go func() {
		err := executor.Local{}.Run(ctx, "docker save "+q(image), executor.RunOpts{Stdout: pw})
		pw.CloseWithError(err)
		errc <- err
	}()
	loadErr := x.Run(ctx, "docker load -q", executor.RunOpts{Stdin: pr})
	pr.Close()
	if err := <-errc; err != nil {
		return fmt.Errorf("copy the image %s: %w", image, err)
	}
	if loadErr != nil {
		return fmt.Errorf("copy the image %s: %w", image, loadErr)
	}
	return nil
}

// PeerStatus is the link from a server to another one.
type PeerStatus struct {
	ServerID      int64  `json:"server_id"`
	Name          string `json:"name"`
	Connected     bool   `json:"connected"`
	LastHandshake string `json:"last_handshake,omitempty"`
	Endpoint      string `json:"endpoint,omitempty"`
}

// ServerStatus is the private network as one server sees it.
type ServerStatus struct {
	ServerID int64        `json:"server_id"`
	Name     string       `json:"name"`
	Joined   bool         `json:"joined"`
	IP       string       `json:"ip"`
	Endpoint string       `json:"endpoint"`
	Agent    string       `json:"agent"` // running | stopped | missing | unreachable
	Error    string       `json:"error,omitempty"`
	Peers    []PeerStatus `json:"peers"`
}

// Status reads the agents and their WireGuard peers.
func (m *Manager) Status(ctx context.Context) ([]ServerStatus, error) {
	s, err := m.Settings(ctx)
	if err != nil {
		return nil, err
	}
	servers, err := store.Select[store.Server](ctx, m.db, "SELECT * FROM servers ORDER BY id")
	if err != nil {
		return nil, err
	}
	byKey := map[string]store.Server{}
	for _, srv := range servers {
		if srv.OverlayPublicKey != "" {
			byKey[srv.OverlayPublicKey] = srv
		}
	}
	out := make([]ServerStatus, len(servers))
	var wg sync.WaitGroup
	for i, srv := range servers {
		st := ServerStatus{ServerID: srv.ID, Name: srv.Name, Joined: s.Enabled && srv.OverlayJoin, IP: srv.OverlayIP,
			Endpoint: Endpoint(srv.Host, srv.OverlayEndpoint, s.Port), Peers: []PeerStatus{}}
		m.mu.Lock()
		st.Error = m.applied[srv.ID].err
		m.mu.Unlock()
		out[i] = st
		if !st.Joined {
			continue
		}
		if srv.Status != "reachable" {
			out[i].Agent = "unreachable"
			continue
		}
		wg.Add(1)
		go func(i int, srv store.Server) {
			defer wg.Done()
			sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			x, err := m.pool.For(sctx, srv)
			if err != nil {
				out[i].Agent, out[i].Error = "unreachable", err.Error()
				return
			}
			state, err := executor.Output(sctx, x, "docker inspect -f '{{.State.Status}}' "+ContainerName+" 2>/dev/null")
			switch {
			case err != nil:
				out[i].Agent = "missing"
				return
			case state != "running":
				out[i].Agent = "stopped"
				return
			}
			out[i].Agent = "running"
			dump, err := executor.Output(sctx, x, "docker exec "+ContainerName+" wg show "+Interface+" dump")
			if err != nil {
				out[i].Error = "WireGuard: " + err.Error()
				return
			}
			now := time.Now()
			for _, p := range ParseDump(dump) {
				peer := byKey[p.PublicKey]
				ps := PeerStatus{ServerID: peer.ID, Name: peer.Name, Connected: p.Connected(now), Endpoint: p.Endpoint}
				if !p.LastHandshake.IsZero() {
					ps.LastHandshake = p.LastHandshake.UTC().Format(time.RFC3339)
				}
				out[i].Peers = append(out[i].Peers, ps)
			}
		}(i, srv)
	}
	wg.Wait()
	return out, nil
}

// ErrNotJoined reports a server outside the private network.
var ErrNotJoined = errors.New("the server is not in the private network")
