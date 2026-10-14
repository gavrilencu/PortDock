package deploy

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/overlay"
	"github.com/portdock/portdock/internal/proxy"
	"github.com/portdock/portdock/internal/store"
)

// Load balancing across servers. An application on several servers with
// LoadBalance publishes the port of its replicas on the private network
// address of the other servers (a port Docker picks), and the primary
// server's proxy routes to them as well as to its own containers: one DNS
// record for the primary, and the traffic spread over every server. Traefik
// checks the replicas on the health check path, so a server that is down
// stops getting requests.

// LoadBalanced reports whether app is balanced through its primary server.
func (e *Engine) LoadBalanced(ctx context.Context, app store.Application) bool {
	if !app.LoadBalance || app.BuildPack == "compose" || e.db.Setting(ctx, "overlay_enabled") != "true" {
		return false
	}
	return len(e.AppServerIDs(ctx, app)) > 1
}

// lbPublish returns the options that publish a replica of app on srv's
// private address ("" on the primary, for previews, or without the network).
func (e *Engine) lbPublish(ctx context.Context, app store.Application, srv store.Server, pr int) string {
	if pr > 0 || srv.ID == app.ServerID || !srv.OverlayJoin || srv.OverlayIP == "" || !e.LoadBalanced(ctx, app) {
		return ""
	}
	return fmt.Sprintf(" -p %s --label %s=true", q(srv.OverlayIP+"::"+strconv.Itoa(app.Port)), overlay.LabelBind)
}

// lbEndpoints lists ip:port of the running replicas of app on its other
// servers; unreachable servers are skipped.
func (e *Engine) lbEndpoints(ctx context.Context, app store.Application) []string {
	var out []string
	for _, id := range e.AppServerIDs(ctx, app)[1:] {
		srv, err := store.ByID[store.Server](ctx, e.db, "servers", id)
		if err != nil || srv.OverlayIP == "" || srv.Status != "reachable" {
			continue
		}
		x, err := e.pool.For(ctx, srv)
		if err != nil {
			continue
		}
		cs, err := docker.PS(ctx, x, false, "label="+docker.LabelResource+"="+app.UUID, "label="+docker.LabelPR+"=0")
		if err != nil {
			continue
		}
		for _, c := range cs {
			ports, err := executor.Output(ctx, x, fmt.Sprintf("docker port %s %d/tcp", q(c.Name), app.Port))
			if err != nil {
				continue
			}
			for _, line := range strings.Split(ports, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), srv.OverlayIP+":") {
					out = append(out, strings.TrimSpace(line))
				}
			}
		}
	}
	return out
}

// writeLBRoutes rewrites the routes of the primary server: its containers
// and the replicas of the other servers.
func (e *Engine) writeLBRoutes(ctx context.Context, app store.Application) error {
	srv, err := store.ByID[store.Server](ctx, e.db, "servers", app.ServerID)
	if err != nil {
		return err
	}
	x, err := e.pool.For(ctx, srv)
	if err != nil {
		return err
	}
	routes, err := e.DomainRoutes(ctx, app)
	if err != nil {
		return err
	}
	cs, err := docker.PS(ctx, x, false, "label="+docker.LabelResource+"="+app.UUID, "label="+docker.LabelPR+"=0")
	if err != nil {
		return err
	}
	var ups []string
	for _, c := range cs {
		ups = append(ups, c.Name)
	}
	ups = append(ups, e.lbEndpoints(ctx, app)...)
	ar := proxy.AppRoutes{Name: app.UUID, Upstreams: ups, Domains: routes, Protection: e.protectionFor(ctx, app, srv), HealthPath: app.HealthCheckPath}
	return writeRoutes(ctx, x, e.routesPath(app.UUID, 0), proxy.RenderDynamic(ar))
}
