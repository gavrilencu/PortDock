package deploy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/proxy"
	"github.com/portdock/portdock/internal/store"
)

// Canary deployments: the new version first receives CanaryPercent of the
// traffic (a Traefik weighted service) while the current one keeps the rest.
// PortDock watches the share of server errors (5xx) the new version answers
// with, from Traefik's metrics, and whether its containers keep running. It
// rolls the new version back as soon as it does worse than allowed, and
// moves all the traffic to it once the observation time has passed — or
// when someone promotes or rolls it back by hand.

const (
	canaryTick        = 10 * time.Second
	canaryMinRequests = 20 // fewer requests do not tell anything about the error rate
)

// canaryRun is a canary in progress (ApplyRoutes keeps its split).
type canaryRun struct {
	stable, canary []string
	percent        int
	tag            string
}

var (
	activeCanaries sync.Map // application id -> *canaryRun
	canaryControl  sync.Map // deployment id -> chan string ("promote" | "rollback")
)

// CanaryInfo is what a canary measured so far.
type CanaryInfo struct {
	Percent        int     `json:"percent"`
	StartedAt      string  `json:"started_at"`
	EndsAt         string  `json:"ends_at"`
	Requests       int64   `json:"requests"`
	Errors         int64   `json:"errors"`
	ErrorRate      float64 `json:"error_rate"`
	StableRequests int64   `json:"stable_requests"`
	StableErrors   int64   `json:"stable_errors"`
	StableRate     float64 `json:"stable_rate"`
	MaxErrors      float64 `json:"max_errors"`
	Decision       string  `json:"decision,omitempty"`
}

// CanaryDecision promotes or rolls back the canary of a running deployment.
func (e *Engine) CanaryDecision(deploymentID int64, action string) error {
	if action != "promote" && action != "rollback" {
		return fmt.Errorf("unknown action %q", action)
	}
	ch, ok := canaryControl.Load(deploymentID)
	if !ok {
		return errors.New("this deployment has no canary in progress")
	}
	select {
	case ch.(chan string) <- action:
		return nil
	default:
		return errors.New("a decision is already being applied")
	}
}

// canaryWanted reports whether this rollout starts with a canary: the
// application asks for one, a previous version is running to compare with,
// and the deployment is a production one on a single server (rollbacks go
// straight back to a known version).
func (j *job) canaryWanted(old []docker.Container) bool {
	if j.app.CanaryPercent <= 0 || j.app.CanaryPercent >= 100 || j.d.PRNumber > 0 || j.d.RollbackOf != 0 || len(j.routes) == 0 {
		return false
	}
	running := 0
	for _, c := range old {
		if c.State == "running" && c.Labels[docker.LabelDeployment] != j.d.UUID {
			running++
		}
	}
	if running == 0 {
		return false
	}
	if !j.single || j.app.PortsMapping != "" {
		j.log.Printf("Canary skipped: it needs an application on a single server without published ports.")
		return false
	}
	return true
}

// serviceCounts sums Traefik's request counters per service: total and 5xx.
type serviceCounts struct{ total, errors int64 }

func (j *job) traefikCounts(ctx context.Context) (map[string]serviceCounts, error) {
	out, err := j.output(ctx, fmt.Sprintf("docker exec %s wget -qO- http://127.0.0.1:%d/metrics", proxy.ContainerName, proxy.MetricsPort))
	if err != nil {
		return nil, fmt.Errorf("read the proxy's metrics: %w", err)
	}
	return parseTraefikCounts(out), nil
}

func parseTraefikCounts(metrics string) map[string]serviceCounts {
	res := map[string]serviceCounts{}
	sc := bufio.NewScanner(strings.NewReader(metrics))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "traefik_service_requests_total{") {
			continue
		}
		labels, value, ok := strings.Cut(strings.TrimPrefix(line, "traefik_service_requests_total{"), "} ")
		if !ok {
			continue
		}
		var code, service string
		for _, kv := range strings.Split(labels, ",") {
			k, v, _ := strings.Cut(kv, "=")
			v = strings.Trim(v, `"`)
			switch k {
			case "code":
				code = v
			case "service":
				service = strings.TrimSuffix(v, "@file")
			}
		}
		n, err := strconv.ParseFloat(strings.Fields(value)[0], 64)
		if err != nil || service == "" {
			continue
		}
		c := res[service]
		c.total += int64(n)
		if strings.HasPrefix(code, "5") {
			c.errors += int64(n)
		}
		res[service] = c
	}
	return res
}

func rate(c serviceCounts) float64 {
	if c.total == 0 {
		return 0
	}
	return float64(c.errors) * 100 / float64(c.total)
}

// runCanary splits the traffic and watches the new version. It returns nil
// when the new version may take all the traffic, and an error after it was
// rolled back (the old version serves everything again).
func (j *job) runCanary(ctx context.Context, started []string, old []docker.Container) error {
	e, app := j.e, j.app
	var stable []string
	for _, c := range old {
		if c.State == "running" && c.Labels[docker.LabelDeployment] != j.d.UUID {
			stable = append(stable, c.Name)
		}
	}
	minutes := max(app.CanaryMinutes, 1)
	maxErrors := app.CanaryMaxErrors
	if maxErrors <= 0 {
		maxErrors = 5
	}
	port := j.routes[0].Port
	tag := strconv.FormatInt(j.d.ID, 10)
	_, stableSvc, canarySvc := proxy.CanaryService(j.routeName(), port, tag)
	routesFile := e.routesPath(app.UUID, 0)
	render := func(canary *proxy.Canary) []byte {
		return proxy.RenderDynamic(proxy.AppRoutes{Name: j.routeName(), Upstreams: stable, Domains: j.routes, Protection: e.protectionFor(ctx, app, j.srv), Canary: canary})
	}

	j.log.Step(fmt.Sprintf("Canary: %d%% of the traffic for %d min", app.CanaryPercent, minutes))
	run := &canaryRun{stable: stable, canary: started, percent: app.CanaryPercent, tag: tag}
	activeCanaries.Store(app.ID, run)
	defer activeCanaries.Delete(app.ID)
	decisions := make(chan string, 1)
	canaryControl.Store(j.d.ID, decisions)
	defer canaryControl.Delete(j.d.ID)

	// The counters before the split: the stable and canary services do not
	// exist yet, so every request they get from now on counts.
	base, err := j.traefikCounts(ctx)
	if err != nil {
		j.log.Printf("⚠ %v: only the containers' health decides.", err)
		base = map[string]serviceCounts{}
	}
	if err := writeRoutes(ctx, j.x, routesFile, render(&proxy.Canary{Upstreams: started, Percent: app.CanaryPercent, Tag: tag})); err != nil {
		j.removeContainers(started...)
		return fmt.Errorf("update routing: %w", err)
	}
	start := time.Now()
	info := CanaryInfo{Percent: app.CanaryPercent, StartedAt: start.UTC().Format(time.RFC3339),
		EndsAt: start.Add(time.Duration(minutes) * time.Minute).UTC().Format(time.RFC3339), MaxErrors: maxErrors}
	save := func(status string) {
		b, _ := json.Marshal(info)
		j.d.CanaryStatus, j.d.CanaryInfo = status, string(b)
		_, _ = e.db.ExecContext(context.WithoutCancel(ctx), "UPDATE deployments SET canary_status = ?, canary_info = ? WHERE id = ?", status, j.d.CanaryInfo, j.d.ID)
		e.hub.Event("deployment", j.d.ID, "running")
	}
	save("observing")
	j.log.Printf("New version: %s. Current version: %s. Rolled back above %.1f%% server errors.", strings.Join(started, ", "), strings.Join(stable, ", "), maxErrors)

	rollback := func(reason string) error {
		bg := context.WithoutCancel(ctx)
		j.log.Printf("↩ Rolling back the canary: %s", reason)
		_ = writeRoutes(bg, j.x, routesFile, render(nil))
		time.Sleep(4 * time.Second)
		j.removeContainers(started...)
		info.Decision = reason
		save("rolled_back")
		j.log.Printf("The current version serves all the traffic again.")
		return fmt.Errorf("canary rolled back: %s", reason)
	}

	deadline := start.Add(time.Duration(minutes) * time.Minute)
	ticker := time.NewTicker(canaryTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return rollback("the deployment was cancelled")
		case d := <-decisions:
			if d == "rollback" {
				return rollback("rolled back by hand")
			}
			info.Decision = "promoted by hand"
			save("promoted")
			j.log.Printf("Canary promoted by hand.")
			return nil
		case <-ticker.C:
		}
		// The new containers must keep running.
		for _, name := range started {
			state, err := j.output(ctx, "docker inspect -f '{{.State.Status}} {{.RestartCount}}' "+q(name))
			if f := strings.Fields(state); err != nil || len(f) != 2 || f[0] != "running" || f[1] != "0" {
				return rollback(fmt.Sprintf("the new container %s stopped or restarted", name))
			}
		}
		final := time.Now().After(deadline)
		if now, err := j.traefikCounts(ctx); err == nil {
			diff := func(svc string) serviceCounts {
				n, b := now[svc], base[svc]
				if n.total < b.total { // the counter was reset
					return n
				}
				return serviceCounts{total: n.total - b.total, errors: n.errors - b.errors}
			}
			c, s := diff(canarySvc), diff(stableSvc)
			info.Requests, info.Errors, info.ErrorRate = c.total, c.errors, rate(c)
			info.StableRequests, info.StableErrors, info.StableRate = s.total, s.errors, rate(s)
			save("observing")
			// The new version is to blame when it fails more than allowed and
			// clearly more than the current one (a shared outage hits both).
			// Little traffic decides only at the end of the observation.
			enough := c.total >= canaryMinRequests || (final && c.total > 0)
			if enough && info.ErrorRate > maxErrors && (s.total < canaryMinRequests || info.ErrorRate > 2*info.StableRate) {
				return rollback(fmt.Sprintf("%.1f%% of its %d responses were server errors (the current version: %.1f%%)", info.ErrorRate, c.total, info.StableRate))
			}
		}
		if final {
			info.Decision = fmt.Sprintf("promoted after %d min", minutes)
			save("promoted")
			j.log.Printf("Canary passed: %d responses, %.1f%% server errors. Moving all the traffic.", info.Requests, info.ErrorRate)
			return nil
		}
	}
}

// CanaryOf decodes a deployment's canary numbers.
func CanaryOf(d store.Deployment) *CanaryInfo {
	if d.CanaryInfo == "" {
		return nil
	}
	var c CanaryInfo
	if json.Unmarshal([]byte(d.CanaryInfo), &c) != nil {
		return nil
	}
	return &c
}
