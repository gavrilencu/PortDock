package deploy

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/proxy"
	"github.com/portdock/portdock/internal/store"
)

// denyAll is used when the protection of an application cannot be read: the
// proxy refuses every client rather than serving it unprotected.
var denyAll = proxy.Protection{IPAllow: []string{"255.255.255.255/32"}}

// protectionFor turns the stored protection of app into proxy middlewares for
// the routes written on srv.
func (e *Engine) protectionFor(ctx context.Context, app store.Application, srv store.Server) proxy.Protection {
	p, err := e.db.Protection(ctx, app.ID)
	if err != nil {
		slog.Error("load application protection", "app", app.Name, "err", err)
		return denyAll
	}
	out := proxy.Protection{IPDepth: p.IPDepth, RateAverage: p.RateAverage, RateBurst: p.RateBurst}
	for _, l := range strings.Split(p.IPAllow, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out.IPAllow = append(out.IPAllow, l)
		}
	}
	switch p.Auth {
	case "password":
		for _, u := range p.Users() {
			out.BasicUsers = append(out.BasicUsers, u.User+":"+u.Hash)
		}
		if len(out.BasicUsers) == 0 {
			return denyAll
		}
	case "sso":
		out.ForwardAuth = e.forwardAuthAddress(ctx, srv, app.UUID)
	}
	return out
}

// forwardAuthAddress is where the proxy of srv asks PortDock whether a request
// is signed in: the PortDock container on the proxy network for the local
// server, the public address of PortDock for remote servers.
func (e *Engine) forwardAuthAddress(ctx context.Context, srv store.Server, appUUID string) string {
	if srv.IsLocal && inDocker() {
		if err := e.joinProxyNetwork(ctx); err == nil {
			return fmt.Sprintf("http://%s:%d/api/forward-auth/%s", proxy.DashboardUpstream, e.listenPort(), appUUID)
		} else {
			slog.Warn("join the proxy network for single sign-on", "err", err)
		}
	}
	return e.PublicURL(ctx) + "/api/forward-auth/" + appUUID
}

// joinProxyNetwork connects the PortDock container to the proxy network under
// the portdock-dashboard alias (no-op when already connected).
func (e *Engine) joinProxyNetwork(ctx context.Context) error {
	x := executor.Local{}
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
	return nil
}

// listenPort is the port PortDock serves on inside its container.
func (e *Engine) listenPort() int {
	if _, p, err := net.SplitHostPort(e.cfg.Addr); err == nil {
		if n, err := strconv.Atoi(p); err == nil {
			return n
		}
	}
	return 8000
}

// applyPreviewRoutes rewrites the routes of the running previews of app on t
// (their domains carry the protection too).
func (e *Engine) applyPreviewRoutes(ctx context.Context, t target, app store.Application, prot proxy.Protection) error {
	cs, err := docker.PS(ctx, t.x, false, "label="+docker.LabelResource+"="+app.UUID)
	if err != nil {
		return err
	}
	byPR := map[int][]string{}
	for _, c := range cs {
		if pr, _ := strconv.Atoi(c.Labels[docker.LabelPR]); pr > 0 {
			byPR[pr] = append(byPR[pr], c.Name)
		}
	}
	for pr, ups := range byPR {
		r := proxy.AppRoutes{Name: fmt.Sprintf("%s-pr%d", app.UUID, pr), Upstreams: ups, Domains: e.previewRoutes(app, t.srv, pr), Protection: prot}
		if err := writeRoutes(ctx, t.x, e.routesPath(app.UUID, pr), proxy.RenderDynamic(r)); err != nil {
			return err
		}
	}
	return nil
}
