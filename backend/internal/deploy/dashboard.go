package deploy

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/proxy"
)

// ErrNotContainerized means PortDock runs outside Docker, so Traefik cannot
// reach it by container name; the dashboard domain must be routed manually.
var ErrNotContainerized = errors.New("PortDock is not running in Docker; route the dashboard domain to it with your own reverse proxy")

func inDocker() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// ApplyDashboardRoute serves the PortDock UI on the configured instance URL
// through the local Traefik proxy (with automatic HTTPS).
func (e *Engine) ApplyDashboardRoute(ctx context.Context) error {
	x := executor.Local{}
	file := path.Join(e.dynamicDir(), "portdock-dashboard.yml")
	raw := strings.TrimSpace(e.db.Setting(ctx, "instance_url"))
	u, _ := url.Parse(raw)
	if raw == "" || u == nil || u.Hostname() == "" || u.Hostname() == "localhost" || net.ParseIP(u.Hostname()) != nil {
		if inDocker() {
			_ = writeRoutes(ctx, x, file, nil)
		}
		return nil
	}
	if !inDocker() {
		return ErrNotContainerized
	}
	self, err := os.Hostname() // the container ID
	if err != nil {
		return err
	}
	if err := docker.EnsureNetwork(ctx, x, e.cfg.ProxyNetwork); err != nil {
		return err
	}
	if out, err := executor.Output(ctx, x, "docker network connect --alias "+proxy.DashboardUpstream+" "+q(e.cfg.ProxyNetwork)+" "+q(self)); err != nil &&
		!strings.Contains(out+err.Error(), "already exists") {
		return err
	}
	if err := proxy.Ensure(ctx, x, e.ProxyOptions(ctx), false); err != nil {
		return err
	}
	port := 8000
	if _, p, err := net.SplitHostPort(e.cfg.Addr); err == nil {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}
	https := u.Scheme == "https" && e.db.Setting(ctx, "instance_https") != "false"
	return writeRoutes(ctx, x, file, proxy.RenderDashboard(strings.ToLower(u.Hostname()), https, port))
}
