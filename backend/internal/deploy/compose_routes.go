package deploy

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/portdock/portdock/internal/composefile"
	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/proxy"
	"github.com/portdock/portdock/internal/store"
	"go.yaml.in/yaml/v3"
)

// Compose applications are routed like every other application: through the
// file provider, per domain. A domain can target any service of the compose
// file (for example app.example.com → frontend, api.example.com → backend), so
// a monorepo with a frontend and a backend deploys as one application.

const composeServiceLabel = "com.docker.compose.service"

// composeMain resolves the main service and its port the same way RunComposeUp does.
func composeMain(raw, service string, port int) (main string, mainPort int, services []composefile.Service, err error) {
	var spec composeSpec
	if err := yaml.Unmarshal([]byte(raw), &spec); err != nil {
		return "", 0, nil, fmt.Errorf("invalid compose file: %w", err)
	}
	if len(spec.Services) == 0 {
		return "", 0, nil, errors.New("compose file defines no services")
	}
	services, _ = composefile.Services(raw)
	main = service
	if main == "" {
		main = spec.PortDock.Service
	}
	if main == "" {
		main = services[0].Name
	}
	if _, ok := spec.Services[main]; !ok {
		return "", 0, nil, fmt.Errorf("service %q not found in compose file (available: %s)", main, serviceNames(services))
	}
	mainPort = port
	if mainPort == 0 {
		mainPort = spec.PortDock.Port
	}
	if mainPort == 0 {
		mainPort = composefile.ServicePort(spec.Services[main])
	}
	return main, mainPort, services, nil
}

func serviceNames(ss []composefile.Service) string {
	names := make([]string, len(ss))
	for i, s := range ss {
		names[i] = s.Name
	}
	return strings.Join(names, ", ")
}

// resolveComposeRoutes fills in the service and port of every route: domains
// without a service go to the main service, and a missing port falls back to the
// main service's port or the port declared by the service. Routes whose port
// can't be told are dropped with a warning.
func resolveComposeRoutes(routes []proxy.DomainRoute, main string, mainPort int, services []composefile.Service, log interface{ Printf(string, ...any) }) ([]proxy.DomainRoute, error) {
	ports := map[string]int{}
	for _, s := range services {
		ports[s.Name] = s.Port
	}
	out := make([]proxy.DomainRoute, 0, len(routes))
	for _, r := range routes {
		if r.Service == "" {
			r.Service = main
		}
		declared, ok := ports[r.Service]
		if !ok {
			return nil, fmt.Errorf("domain %s: service %q is not in the compose file (available: %s)", r.Host+r.Path, r.Service, serviceNames(services))
		}
		if r.Port == 0 {
			r.Port = declared
			if r.Service == main && mainPort != 0 {
				r.Port = mainPort
			}
		}
		if r.Port == 0 {
			if log != nil {
				log.Printf("⚠ Domain %s is not routed: set its port (service %s declares no single port).", r.Host+r.Path, r.Service)
			}
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// routedServices lists the services that receive traffic.
func routedServices(routes []proxy.DomainRoute) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range routes {
		if r.Service != "" && !seen[r.Service] {
			seen[r.Service] = true
			out = append(out, r.Service)
		}
	}
	sort.Strings(out)
	return out
}

// writeComposeRoutes points the routes at the running containers of each
// service, joining them to the proxy network first when needed.
func (e *Engine) writeComposeRoutes(ctx context.Context, x executor.Executor, app store.Application, routes []proxy.DomainRoute, pr int, log interface{ Printf(string, ...any) }) error {
	cs, err := docker.PS(ctx, x, false, "label=com.docker.compose.project="+composeProject(app.UUID))
	if err != nil {
		return err
	}
	byService := map[string][]string{}
	for _, c := range cs {
		if c.State == "running" {
			svc := c.Labels[composeServiceLabel]
			byService[svc] = append(byService[svc], c.Name)
		}
	}
	for _, svc := range routedServices(routes) {
		for _, name := range byService[svc] {
			// No-op when already connected (services routed at deploy time join through the override).
			_ = x.Run(ctx, fmt.Sprintf("docker network connect %s %s >/dev/null 2>&1 || true", q(e.cfg.ProxyNetwork), q(name)), executor.RunOpts{})
		}
		if len(byService[svc]) == 0 && log != nil {
			log.Printf("⚠ Service %s has no running container; its domains get no traffic.", svc)
		}
	}
	name := app.UUID
	if pr > 0 {
		name = fmt.Sprintf("%s-pr%d", app.UUID, pr)
	}
	if err := writeRoutes(ctx, x, e.routesPath(app.UUID, pr), proxy.RenderDynamic(proxy.AppRoutes{Name: name, Services: byService, Domains: routes})); err != nil {
		return err
	}
	if log != nil {
		for _, r := range routes {
			log.Printf("🌐 %s → %s:%d", r.Host+r.Path, r.Service, r.Port)
		}
	}
	return nil
}

// applyComposeRoutes re-routes a deployed compose application after a domain change.
func (e *Engine) applyComposeRoutes(ctx context.Context, t target, app store.Application, routes []proxy.DomainRoute) error {
	raw, err := executor.Output(ctx, t.x, "cat "+q(e.composeFile(app))+" 2>/dev/null")
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil // not deployed on this server yet; the first deploy writes the routes
	}
	main, port, services, err := composeMain(raw, app.ComposeService, app.Port)
	if err != nil {
		return err
	}
	resolved, err := resolveComposeRoutes(routes, main, port, services, nil)
	if err != nil {
		return err
	}
	return e.writeComposeRoutes(ctx, t.x, app, resolved, 0, nil)
}

// ComposeServices lists the services of the compose file checked out by the
// last deploy on the application's primary server.
func (e *Engine) ComposeServices(ctx context.Context, app store.Application) ([]composefile.Service, error) {
	x, _, err := e.pool.ForID(ctx, app.ServerID)
	if err != nil {
		return nil, err
	}
	raw, err := executor.Output(ctx, x, "cat "+q(e.composeFile(app))+" 2>/dev/null")
	if err != nil || strings.TrimSpace(raw) == "" {
		return []composefile.Service{}, nil
	}
	return composefile.Services(raw)
}

func (e *Engine) composeFile(app store.Application) string {
	return path.Join(e.cfg.DataDir, "apps", app.UUID, "compose", "src", strings.Trim(app.BaseDirectory, "/"), strings.TrimPrefix(app.ComposePath, "/"))
}
