package deploy

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/proxy"
	"github.com/portdock/portdock/internal/store"
)

// Routing for applications is written as Traefik file-provider config, not
// container labels. That lets domain changes apply instantly and makes a new
// version receive traffic only after it passed its health check.

func (e *Engine) dynamicDir() string { return path.Join(e.cfg.DataDir, proxy.DynamicDir) }

func (e *Engine) routesPath(uuid string, pr int) string {
	name := "app-" + uuid
	if pr > 0 {
		name += fmt.Sprintf("-pr%d", pr)
	}
	return path.Join(e.dynamicDir(), name+".yml")
}

// writeRoutes atomically replaces a routes file; nil content removes it.
func writeRoutes(ctx context.Context, x executor.Executor, file string, content []byte) error {
	if content == nil {
		return x.Run(ctx, "rm -f "+q(file), executor.RunOpts{})
	}
	dir, name := path.Split(file)
	tmp := dir + "." + name + ".tmp" // not *.yml, so Traefik ignores partial writes
	if err := executor.WriteFile(ctx, x, tmp, content, "0644"); err != nil {
		return err
	}
	_, err := executor.Output(ctx, x, "mv -f "+q(tmp)+" "+q(file))
	return err
}

// DomainRoutes converts an application's domain rows into proxy routes.
func (e *Engine) DomainRoutes(ctx context.Context, app store.Application) ([]proxy.DomainRoute, error) {
	rows, err := e.db.Domains(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	dnsReady := e.db.Setting(ctx, "dns_provider") != ""
	out := make([]proxy.DomainRoute, 0, len(rows))
	for _, d := range rows {
		port := d.Port
		if port == 0 && d.Service == "" {
			port = app.Port // 0 for compose domains with a service: the service's own port is used
		}
		cert := ""
		switch {
		case d.CertMode == "custom":
			cert = "custom"
		case d.CertMode == "dns" && dnsReady, d.CertMode != "http" && strings.HasPrefix(d.Host, "*.") && dnsReady:
			cert = "dns"
		}
		out = append(out, proxy.DomainRoute{Host: d.Host, Path: d.Path, Port: port, Service: d.Service, HTTPS: d.HTTPS, WWW: d.WWWRedirect, Indexing: d.Indexing, Cert: cert})
	}
	return out, nil
}

func (e *Engine) previewRoutes(app store.Application, srv store.Server, pr int) []proxy.DomainRoute {
	var out []proxy.DomainRoute
	for _, d := range e.PreviewDomains(app, srv, pr) {
		host, p := proxy.NormalizeDomain(d)
		// Previews are never indexed by search engines.
		out = append(out, proxy.DomainRoute{Host: host, Path: p, Port: app.Port, HTTPS: app.HTTPS, Indexing: false})
	}
	return out
}

// ApplyRoutes rewrites the production routes of app for its running containers
// on every server.
func (e *Engine) ApplyRoutes(ctx context.Context, app store.Application) error {
	routes, err := e.DomainRoutes(ctx, app)
	if err != nil {
		return err
	}
	return e.eachTarget(ctx, app, func(t target) error {
		if len(routes) > 0 && t.srv.ProxyEnabled {
			if err := proxy.Ensure(ctx, t.x, e.ProxyOptions(ctx), false); err != nil {
				return fmt.Errorf("start proxy: %w", err)
			}
		}
		if app.BuildPack == "compose" {
			return e.applyComposeRoutes(ctx, t, app, routes)
		}
		cs, err := docker.PS(ctx, t.x, false, "label="+docker.LabelResource+"="+app.UUID, "label="+docker.LabelPR+"=0")
		if err != nil {
			return err
		}
		var ups []string
		for _, c := range cs {
			ups = append(ups, c.Name)
		}
		ar := proxy.AppRoutes{Name: app.UUID, Upstreams: ups, Domains: routes, Protection: e.protectionFor(ctx, app, t.srv)}
		if t.srv.ID == app.ServerID && e.LoadBalanced(ctx, app) {
			ar.Upstreams, ar.HealthPath = append(ups, e.lbEndpoints(ctx, app)...), app.HealthCheckPath
		}
		if c, ok := activeCanaries.Load(app.ID); ok {
			// Keep the split of a canary in progress.
			run := c.(*canaryRun)
			ar.Upstreams, ar.Canary = run.stable, &proxy.Canary{Upstreams: run.canary, Percent: run.percent, Tag: run.tag}
		}
		prot := ar.Protection
		if err := writeRoutes(ctx, t.x, e.routesPath(app.UUID, 0), proxy.RenderDynamic(ar)); err != nil {
			return err
		}
		return e.applyPreviewRoutes(ctx, t, app, prot)
	})
}

// removeAllRoutes deletes the production and preview routes of an application.
func (e *Engine) removeAllRoutes(ctx context.Context, x executor.Executor, uuid string) {
	_ = x.Run(ctx, "rm -f "+q(e.dynamicDir())+"/app-"+uuid+".yml "+q(e.dynamicDir())+"/app-"+uuid+"-pr*.yml", executor.RunOpts{})
}

func hostsOf(routes []proxy.DomainRoute) []string {
	out := make([]string, 0, len(routes))
	for _, r := range routes {
		out = append(out, r.Host+r.Path)
	}
	return out
}

func urlOf(r proxy.DomainRoute) string {
	scheme := "https://"
	if !r.HTTPS {
		scheme = "http://"
	}
	return scheme + r.Host + strings.TrimSuffix(r.Path, "/")
}

// PublicURL is the externally reachable URL of this PortDock instance.
func (e *Engine) PublicURL(ctx context.Context) string {
	if u := strings.TrimSpace(e.db.Setting(ctx, "instance_url")); u != "" {
		return strings.TrimRight(u, "/")
	}
	return strings.TrimRight(e.cfg.PublicURL, "/")
}
