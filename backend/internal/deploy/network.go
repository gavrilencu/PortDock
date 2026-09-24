package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/netcfg"
	"github.com/portdock/portdock/internal/store"
)

// Network returns the corporate network settings (proxy and mirrors).
func (e *Engine) Network(ctx context.Context) netcfg.Config {
	return netcfg.FromSettings(func(k string) string {
		v := e.db.Setting(ctx, k)
		if k == "proxy_password" && v != "" {
			return e.box.MustDecrypt(v)
		}
		return v
	})
}

// ReloadNetwork applies the settings to PortDock's own outgoing requests.
func (e *Engine) ReloadNetwork(ctx context.Context) {
	netcfg.SetCurrent(e.Network(ctx))
}

// containerProxyEnv is the proxy environment of an application's containers:
// the global choice, unless the application turns it on or off.
func containerProxyEnv(net netcfg.Config, mode string) map[string]string {
	if !net.Enabled() || mode == "off" || (mode != "on" && !net.Containers) {
		return nil
	}
	return net.ProxyEnv()
}

// envFlags renders -e KEY=value flags for docker run.
func envFlags(env map[string]string) string {
	var b strings.Builder
	for _, k := range netcfg.Keys(env) {
		b.WriteString(" -e " + q(k+"="+env[k]))
	}
	return b.String()
}

// buildProxyEnv is the proxy environment of builds (git, docker build, nixpacks, Trivy).
func buildProxyEnv(net netcfg.Config) map[string]string {
	if !net.Builds {
		return nil
	}
	return net.ProxyEnv()
}

// ---- Docker daemon of a server ----

const (
	daemonJSON      = "/etc/docker/daemon.json"
	daemonDropInDir = "/etc/systemd/system/docker.service.d"
	daemonDropIn    = daemonDropInDir + "/http-proxy.conf"
)

// DaemonNetwork is what the Docker daemon of a server uses for pulls.
type DaemonNetwork struct {
	Mirrors      []string `json:"mirrors"`
	Insecure     []string `json:"insecure"`
	HTTPProxy    string   `json:"http_proxy"`
	HTTPSProxy   string   `json:"https_proxy"`
	NoProxy      string   `json:"no_proxy"`
	DaemonJSON   string   `json:"daemon_json"`
	DropIn       string   `json:"drop_in"`
	Systemd      bool     `json:"systemd"`
	WantMirror   string   `json:"want_mirror"`
	WantProxy    bool     `json:"want_proxy"`
	UpToDate     bool     `json:"up_to_date"`
	MirrorOK     bool     `json:"mirror_ok"`
	ProxyOK      bool     `json:"proxy_ok"`
	ProxyWritten bool     `json:"proxy_written"`
}

func maskProxy(s string) string {
	if i := strings.Index(s, "://"); i >= 0 {
		if at := strings.LastIndex(s, "@"); at > i {
			return s[:i+3] + "***@" + s[at+1:]
		}
	}
	return s
}

// DaemonStatus reads the registry mirrors and proxy the daemon of a server runs with.
func (e *Engine) DaemonStatus(ctx context.Context, srv store.Server) (DaemonNetwork, error) {
	x, err := e.pool.For(ctx, srv)
	if err != nil {
		return DaemonNetwork{}, err
	}
	var d DaemonNetwork
	out, err := executor.Output(ctx, x, "docker info --format '{{json .}}'")
	if err != nil {
		return d, err
	}
	var info struct {
		HTTPProxy      string `json:"HttpProxy"`
		HTTPSProxy     string `json:"HttpsProxy"`
		NoProxy        string `json:"NoProxy"`
		RegistryConfig struct {
			Mirrors               []string `json:"Mirrors"`
			InsecureRegistryCIDRs []string `json:"InsecureRegistryCIDRs"`
			IndexConfigs          map[string]struct {
				Secure bool `json:"Secure"`
			} `json:"IndexConfigs"`
		} `json:"RegistryConfig"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return d, fmt.Errorf("unexpected docker info output: %w", err)
	}
	d.Mirrors = info.RegistryConfig.Mirrors
	if d.Mirrors == nil {
		d.Mirrors = []string{}
	}
	d.Insecure = []string{}
	for name, ic := range info.RegistryConfig.IndexConfigs {
		if !ic.Secure {
			d.Insecure = append(d.Insecure, name)
		}
	}
	d.HTTPProxy, d.HTTPSProxy, d.NoProxy = maskProxy(info.HTTPProxy), maskProxy(info.HTTPSProxy), info.NoProxy
	d.DaemonJSON, _ = executor.Output(ctx, x, "cat "+daemonJSON+" 2>/dev/null || true")
	drop, _ := executor.Output(ctx, x, "cat "+daemonDropIn+" 2>/dev/null || true")
	d.DropIn = maskProxy(drop)
	_, serr := executor.Output(ctx, x, "command -v systemctl >/dev/null && [ -d /run/systemd/system ]")
	d.Systemd = serr == nil

	net := e.Network(ctx)
	d.WantMirror, d.WantProxy = net.RegistryMirror, net.Enabled()
	d.ProxyWritten = strings.TrimSpace(drop) == strings.TrimSpace(net.DaemonProxyDropIn())
	mirrorOK := net.RegistryMirror == "" || (len(d.Mirrors) > 0 && strings.TrimRight(d.Mirrors[0], "/") == strings.TrimRight(net.RegistryMirror, "/"))
	proxyOK := !net.Enabled() || info.HTTPSProxy != "" || info.HTTPProxy != ""
	d.MirrorOK, d.ProxyOK, d.UpToDate = mirrorOK, proxyOK, mirrorOK && proxyOK
	return d, nil
}

// asRoot runs a command as root: directly, or with sudo -n for other users.
const asRoot = `S=; [ "$(id -u)" = 0 ] || S="sudo -n"; `

// rootWrite writes a file owned by root (through a temporary file and install).
func rootWrite(ctx context.Context, x executor.Executor, file string, content []byte, mode string) error {
	tmp := fmt.Sprintf("/tmp/portdock-%d.tmp", time.Now().UnixNano())
	if err := executor.WriteFile(ctx, x, tmp, content, "0600"); err != nil {
		return err
	}
	dir := file[:strings.LastIndex(file, "/")]
	_, err := executor.Output(ctx, x, asRoot+fmt.Sprintf(`$S mkdir -p %s && $S install -m %s %s %s; r=$?; rm -f %s; exit $r`, q(dir), mode, q(tmp), q(file), q(tmp)))
	if err != nil {
		return fmt.Errorf("write %s (PortDock needs root or passwordless sudo on the server): %w", file, err)
	}
	return nil
}

// Daemon apply modes.
const (
	DaemonWrite   = "write"   // only write the files
	DaemonReload  = "reload"  // reload the daemon's configuration: mirrors apply at once, no container restarts
	DaemonRestart = "restart" // restart Docker: needed for the proxy; containers with a restart policy come back
)

// ApplyDaemonNetwork writes the registry mirror into daemon.json (other keys
// are kept) and the proxy into a systemd drop-in, then reloads or restarts
// the Docker daemon.
func (e *Engine) ApplyDaemonNetwork(ctx context.Context, srv store.Server, mode string) (string, error) {
	x, err := e.pool.For(ctx, srv)
	if err != nil {
		return "", err
	}
	net := e.Network(ctx)
	raw, _ := executor.Output(ctx, x, "cat "+daemonJSON+" 2>/dev/null || true")
	existing := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &existing); err != nil {
			return "", fmt.Errorf("%s is not valid JSON; fix it first: %w", daemonJSON, err)
		}
	}
	prevKey := fmt.Sprintf("daemon_mirror_%d", srv.ID)
	merged := net.ApplyDaemonJSON(existing, e.db.Setting(ctx, prevKey))
	b, _ := json.MarshalIndent(merged, "", "  ")
	var steps []string
	if string(b) != strings.TrimSpace(raw) && !(len(merged) == 0 && strings.TrimSpace(raw) == "") {
		if strings.TrimSpace(raw) != "" {
			_, _ = executor.Output(ctx, x, asRoot+"$S cp -f "+daemonJSON+" "+daemonJSON+".portdock.bak")
		}
		if err := rootWrite(ctx, x, daemonJSON, append(b, '\n'), "0644"); err != nil {
			return "", err
		}
		steps = append(steps, "daemon.json updated")
	}
	_ = e.db.SetSetting(ctx, prevKey, net.RegistryMirror)

	_, serr := executor.Output(ctx, x, "command -v systemctl >/dev/null && [ -d /run/systemd/system ]")
	systemd := serr == nil
	if systemd {
		if net.Enabled() {
			if err := rootWrite(ctx, x, daemonDropIn, []byte(net.DaemonProxyDropIn()), "0600"); err != nil {
				return "", err
			}
			steps = append(steps, "proxy drop-in written")
		} else if out, _ := executor.Output(ctx, x, "grep -l 'Written by PortDock' "+daemonDropIn+" 2>/dev/null || true"); strings.TrimSpace(out) != "" {
			_, _ = executor.Output(ctx, x, asRoot+"$S rm -f "+daemonDropIn)
			steps = append(steps, "proxy drop-in removed")
		}
	} else if net.Enabled() {
		steps = append(steps, "the daemon proxy needs systemd; set it in the service manager of this server")
	}
	switch mode {
	case DaemonReload:
		// SIGHUP reloads registry-mirrors and insecure-registries without touching containers.
		cmd := asRoot + `$S kill -HUP "$(pidof dockerd)"`
		if systemd {
			cmd = asRoot + "$S systemctl daemon-reload && $S systemctl reload docker"
		}
		if _, err := executor.Output(ctx, x, cmd); err != nil {
			return strings.Join(steps, "; "), fmt.Errorf("reload Docker: %w", err)
		}
		steps = append(steps, "Docker configuration reloaded")
	case DaemonRestart:
		if !systemd {
			return strings.Join(steps, "; "), ErrNoSystemd
		}
		// In the background: the local server may be running PortDock itself.
		if _, err := executor.Output(ctx, x, asRoot+"nohup $S sh -c 'sleep 2; systemctl daemon-reload && systemctl restart docker' >/dev/null 2>&1 &"); err != nil {
			return "", err
		}
		steps = append(steps, "Docker is restarting")
	}
	return strings.Join(steps, "; "), nil
}

// ErrNoSystemd is returned when the daemon can't be restarted by PortDock.
var ErrNoSystemd = errors.New("the server doesn't use systemd: restart Docker yourself to apply the proxy")
