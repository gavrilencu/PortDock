package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
)

// Step is one line of the update log.
type Step struct {
	Time    string `json:"time"`
	Message string `json:"message"`
}

// State is persisted in <data>/update-state.json so progress survives the
// restart of PortDock itself.
type State struct {
	ID         string `json:"id"`
	Status     string `json:"status"` // running | success | rolled_back | failed
	From       string `json:"from"`
	To         string `json:"to"`
	Image      string `json:"image"`
	Steps      []Step `json:"steps"`
	Error      string `json:"error,omitempty"`
	Backup     string `json:"backup,omitempty"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
}

var stateMu sync.Mutex

func statePath(dataDir string) string { return filepath.Join(dataDir, "update-state.json") }

// LoadState reads the last update attempt (zero value when none).
func LoadState(dataDir string) State {
	stateMu.Lock()
	defer stateMu.Unlock()
	var s State
	b, err := os.ReadFile(statePath(dataDir))
	if err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func saveState(dataDir string, s State) {
	stateMu.Lock()
	defer stateMu.Unlock()
	b, _ := json.MarshalIndent(s, "", "  ")
	tmp := statePath(dataDir) + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, statePath(dataDir))
	}
}

// Log appends a step and persists the state.
func (s *State) Log(dataDir, format string, args ...any) {
	s.Steps = append(s.Steps, Step{Time: time.Now().UTC().Format(time.RFC3339), Message: fmt.Sprintf(format, args...)})
	saveState(dataDir, *s)
}

func (s *State) finish(dataDir, status string, err error) {
	s.Status, s.FinishedAt = status, time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		s.Error = err.Error()
	}
	saveState(dataDir, *s)
}

// ---- configuration cloning ----

// CloneBody builds the create request for the new container from the old
// container's inspect data. Settings that merely repeat the old image's
// defaults (Env, Cmd, Entrypoint, Labels, Healthcheck…) are dropped so the new
// image's defaults apply; everything the operator configured is kept.
func CloneBody(old Container, oldImage map[string]json.RawMessage, newImage string) (map[string]any, error) {
	var cfg map[string]any
	if err := json.Unmarshal(old.Raw["Config"], &cfg); err != nil {
		return nil, fmt.Errorf("container config: %w", err)
	}
	var imgCfg map[string]any
	if b, err := json.Marshal(oldImage); err == nil {
		_ = json.Unmarshal(b, &imgCfg)
	}
	// Hostname/Domainname default to the container ID; a new container gets its own.
	delete(cfg, "Hostname")
	delete(cfg, "Domainname")
	cfg["Image"] = newImage

	// Env: keep variables that differ from the image defaults.
	imgEnv := map[string]string{}
	for _, e := range toStrings(imgCfg["Env"]) {
		k, v, _ := strings.Cut(e, "=")
		imgEnv[k] = v
	}
	var env []string
	for _, e := range toStrings(cfg["Env"]) {
		k, v, _ := strings.Cut(e, "=")
		if iv, ok := imgEnv[k]; ok && iv == v {
			continue
		}
		env = append(env, e)
	}
	cfg["Env"] = env

	for _, key := range []string{"Cmd", "Entrypoint", "Healthcheck", "WorkingDir", "User", "ExposedPorts", "Volumes", "StopSignal", "Shell", "OnBuild"} {
		if v, ok := cfg[key]; ok && reflect.DeepEqual(v, imgCfg[key]) {
			delete(cfg, key)
		}
	}
	// Labels: drop the ones inherited from the old image (e.g. its OCI version label).
	if labels, ok := cfg["Labels"].(map[string]any); ok {
		imgLabels, _ := imgCfg["Labels"].(map[string]any)
		for k, v := range labels {
			if iv, ok := imgLabels[k]; ok && iv == v {
				delete(labels, k)
			}
		}
	}

	var host map[string]any
	if err := json.Unmarshal(old.Raw["HostConfig"], &host); err != nil {
		return nil, fmt.Errorf("host config: %w", err)
	}
	body := cfg
	body["HostConfig"] = host

	// Networks with their aliases (minus the old container's short ID alias).
	var ns struct {
		Networks map[string]struct {
			Aliases    []string       `json:"Aliases"`
			IPAMConfig map[string]any `json:"IPAMConfig"`
		} `json:"Networks"`
	}
	_ = json.Unmarshal(old.Raw["NetworkSettings"], &ns)
	endpoints := map[string]any{}
	short := old.ID
	if len(short) > 12 {
		short = short[:12]
	}
	for name, n := range ns.Networks {
		var aliases []string
		for _, a := range n.Aliases {
			if a != short && a != old.ID {
				aliases = append(aliases, a)
			}
		}
		ep := map[string]any{"Aliases": aliases}
		if len(n.IPAMConfig) > 0 {
			ep["IPAMConfig"] = n.IPAMConfig
		}
		endpoints[name] = ep
	}
	body["NetworkingConfig"] = map[string]any{"EndpointsConfig": endpoints}
	return body, nil
}

func toStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ---- running PortDock side ----

// SelfContainer finds the container PortDock runs in.
func SelfContainer(ctx context.Context, d *Docker) (Container, error) {
	candidates := []string{os.Getenv("PORTDOCK_CONTAINER"), os.Getenv("SHIPYARD_CONTAINER")}
	if h, err := os.Hostname(); err == nil {
		candidates = append(candidates, h)
	}
	candidates = append(candidates, "portdock", "shipyard")
	for _, id := range candidates {
		if id == "" {
			continue
		}
		c, err := d.Inspect(ctx, id)
		if err == nil && c.hasSocket() {
			return c, nil
		}
	}
	return Container{}, errors.New("PortDock is not running in a Docker container with the Docker socket mounted; update it by pulling the new image manually")
}

func (c Container) hasSocket() bool {
	for _, m := range c.Mounts {
		if m.Destination == SocketPath {
			return true
		}
	}
	return false
}

// DataMountSource returns the host path mounted at dataDir.
func (c Container) DataMountSource(dataDir string) string {
	for _, m := range c.Mounts {
		if m.Destination == dataDir {
			return m.Source
		}
	}
	return ""
}

// Plan describes an update to start.
type Plan struct {
	DataDir      string
	From         string // current version
	To           string // target tag
	Image        string // full reference, e.g. ghcr.io/acme/portdock:v1.3.0
	RegistryUser string
	RegistryPass string
	// Backup copies the database before anything changes; it returns the backup path.
	Backup func(ctx context.Context) (string, error)
	// Verify checks the signature of the pulled image. It returns the image
	// pinned to the verified digest (or "" to keep Image) and a line for the
	// update log; an error stops the update before anything changes.
	Verify func(ctx context.Context, image string) (pinned, message string, err error)
}

// Launch pulls the image, backs up the data and starts the updater container,
// which replaces this container. It returns once the updater runs.
func Launch(ctx context.Context, d *Docker, p Plan) (State, error) {
	st := State{ID: fmt.Sprintf("upd-%d", time.Now().Unix()), Status: "running", From: p.From, To: p.To, Image: p.Image, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	st.Log(p.DataDir, "Updating PortDock %s → %s", p.From, p.To)
	fail := func(err error) (State, error) {
		st.Log(p.DataDir, "❌ %v", err)
		st.finish(p.DataDir, "failed", err)
		return st, err
	}
	self, err := SelfContainer(ctx, d)
	if err != nil {
		return fail(err)
	}
	if d.ImageExists(ctx, p.Image) {
		st.Log(p.DataDir, "Image %s is already present", p.Image)
	} else {
		st.Log(p.DataDir, "Pulling %s", p.Image)
		if err := d.Pull(ctx, p.Image, p.RegistryUser, p.RegistryPass, func(line string) {}); err != nil {
			return fail(fmt.Errorf("pull %s: %w", p.Image, err))
		}
		st.Log(p.DataDir, "Image downloaded")
	}
	if p.Verify != nil {
		pinned, msg, err := p.Verify(ctx, p.Image)
		if err != nil {
			return fail(err)
		}
		if msg != "" {
			st.Log(p.DataDir, "%s", msg)
		}
		if pinned != "" && pinned != p.Image {
			if !d.ImageExists(ctx, pinned) {
				if err := d.Pull(ctx, pinned, p.RegistryUser, p.RegistryPass, func(string) {}); err != nil {
					return fail(fmt.Errorf("pull %s: %w", pinned, err))
				}
			}
			p.Image, st.Image = pinned, pinned
		}
	}
	if p.Backup != nil {
		path, err := p.Backup(ctx)
		if err != nil {
			return fail(fmt.Errorf("backup failed, nothing was changed: %w", err))
		}
		st.Backup = path
		st.Log(p.DataDir, "Database backed up to %s", path)
	}
	dataSrc := self.DataMountSource(p.DataDir)
	if dataSrc == "" {
		return fail(fmt.Errorf("the data directory %s is not a mounted volume; refusing to update because data would be lost", p.DataDir))
	}
	// The updater runs the *current* (known-good) image: a broken release must
	// never be responsible for replacing — or restoring — PortDock.
	body := map[string]any{
		"Image": self.Image,
		"Cmd":   []string{"-self-update", self.ID},
		"Env":   []string{"PORTDOCK_DATA_DIR=" + p.DataDir, "PORTDOCK_UPDATE_IMAGE=" + p.Image, "SHIPYARD_DATA_DIR=" + p.DataDir, "SHIPYARD_UPDATE_IMAGE=" + p.Image},
		"Labels": map[string]string{
			"portdock.managed": "true", "portdock.kind": "updater",
		},
		"HostConfig": map[string]any{
			"Binds": []string{SocketPath + ":" + SocketPath, dataSrc + ":" + p.DataDir},
		},
	}
	name := "portdock-updater-" + fmt.Sprint(time.Now().Unix())
	id, err := d.Create(ctx, name, body)
	if err != nil {
		return fail(fmt.Errorf("create updater container: %w", err))
	}
	if err := d.Start(ctx, id); err != nil {
		_ = d.Remove(ctx, id)
		return fail(fmt.Errorf("start updater container: %w", err))
	}
	st.Log(p.DataDir, "Updater started; PortDock restarts in a few seconds")
	// Make sure the updater really runs (it stops this process when it does).
	for i := 0; i < 5; i++ {
		time.Sleep(2 * time.Second)
		c, err := d.Inspect(ctx, id)
		if err != nil {
			break
		}
		if c.State.Status == "exited" && c.State.ExitCode != 0 {
			logs, _ := d.Logs(ctx, id, 20)
			_ = d.Remove(ctx, id)
			return fail(fmt.Errorf("the updater exited with code %d: %s", c.State.ExitCode, strings.TrimSpace(logs)))
		}
	}
	return st, nil
}

// CleanupUpdaters removes finished updater containers.
func CleanupUpdaters(ctx context.Context, d *Docker) {
	var list []struct {
		ID    string `json:"Id"`
		State string `json:"State"`
	}
	filters := `{"label":["portdock.kind=updater"],"status":["exited","dead","created"]}`
	if err := d.call(ctx, "GET", "/containers/json?all=1&filters="+url.QueryEscape(filters), nil, &list); err != nil {
		return
	}
	for _, c := range list {
		_ = d.Remove(ctx, c.ID)
	}
}

// ---- updater container side ----

// Swap replaces the old container by one running the updater's own image. It
// runs inside the short-lived updater container.
func Swap(ctx context.Context, d *Docker, oldID, dataDir string) error {
	st := LoadState(dataDir)
	if st.Status != "running" {
		st = State{ID: fmt.Sprintf("upd-%d", time.Now().Unix()), Status: "running", StartedAt: time.Now().UTC().Format(time.RFC3339)}
	}
	fail := func(status string, err error) error {
		st.Log(dataDir, "❌ %v", err)
		st.finish(dataDir, status, err)
		return err
	}
	self, err := SelfContainer(ctx, d)
	image := os.Getenv("PORTDOCK_UPDATE_IMAGE")
	if image == "" {
		image = os.Getenv("SHIPYARD_UPDATE_IMAGE")
	}
	if err == nil && image == "" {
		image = self.Config.Image
	}
	if image == "" {
		image = st.Image
	}
	old, err := d.Inspect(ctx, oldID)
	if err != nil {
		return fail("failed", fmt.Errorf("inspect current container: %w", err))
	}
	oldImage, err := d.ImageConfig(ctx, old.Image)
	if err != nil {
		oldImage = map[string]json.RawMessage{}
	}
	body, err := CloneBody(old, oldImage, image)
	if err != nil {
		return fail("failed", err)
	}
	name := old.Name
	previous := name + "-previous"
	time.Sleep(2 * time.Second) // let the API answer the request that started the update

	st.Log(dataDir, "Stopping %s", name)
	if err := d.Stop(ctx, old.ID, 30*time.Second); err != nil {
		return fail("failed", fmt.Errorf("stop current container: %w", err))
	}
	_ = d.Remove(ctx, previous) // leftover of an earlier attempt
	if err := d.Rename(ctx, old.ID, previous); err != nil {
		_ = d.Start(ctx, old.ID)
		return fail("failed", fmt.Errorf("rename current container: %w", err))
	}
	rollback := func(cause error) error {
		st.Log(dataDir, "Rolling back to the previous version")
		if err := d.Rename(ctx, old.ID, name); err != nil {
			st.Log(dataDir, "rename back failed: %v", err)
		}
		if err := d.Start(ctx, old.ID); err != nil {
			return fail("failed", fmt.Errorf("%v; ROLLBACK FAILED: %v — start container %s manually", cause, err, previous))
		}
		st.Log(dataDir, "Previous version is running again")
		return fail("rolled_back", cause)
	}

	st.Log(dataDir, "Creating %s from %s", name, image)
	newID, err := d.Create(ctx, name, body)
	if err != nil {
		return rollback(fmt.Errorf("create new container: %w", err))
	}
	if err := d.Start(ctx, newID); err != nil {
		_ = d.Remove(ctx, newID)
		return rollback(fmt.Errorf("start new container: %w", err))
	}
	st.Log(dataDir, "Waiting for the new version to become healthy")
	if err := waitHealthy(ctx, d, newID, 3*time.Minute); err != nil {
		_ = d.Stop(ctx, newID, 10*time.Second)
		_ = d.Remove(ctx, newID)
		return rollback(err)
	}
	if err := d.Remove(ctx, old.ID); err != nil {
		st.Log(dataDir, "⚠ could not remove the previous container: %v", err)
	}
	st.Log(dataDir, "✅ Update complete")
	st.finish(dataDir, "success", nil)
	return nil
}

func waitHealthy(ctx context.Context, d *Docker, id string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	runningSince := time.Time{}
	for time.Now().Before(deadline) {
		c, err := d.Inspect(ctx, id)
		if err != nil {
			return err
		}
		switch {
		case !c.State.Running && (c.State.Status == "exited" || c.State.Status == "dead"):
			return fmt.Errorf("the new version exited with code %d", c.State.ExitCode)
		case c.State.Health != nil && c.State.Health.Status == "healthy":
			return nil
		case c.State.Health != nil && c.State.Health.Status == "unhealthy":
			return errors.New("the new version is unhealthy")
		case c.State.Health == nil && c.State.Running:
			if runningSince.IsZero() {
				runningSince = time.Now()
			}
			if time.Since(runningSince) > 20*time.Second {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("the new version did not become healthy within %s", timeout)
}
