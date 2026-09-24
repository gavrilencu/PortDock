package api

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/store"
)

// Topology is the live resource tree (ArgoCD-style): resource → server → containers.

type topoContainer struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Service      string            `json:"service,omitempty"` // compose service
	Image        string            `json:"image"`
	State        string            `json:"state"`
	Health       string            `json:"health,omitempty"`
	Phase        string            `json:"phase"` // healthy | running | starting | unhealthy | crashloop | restarting | failed | exited | paused | created
	RestartCount int               `json:"restart_count"`
	ExitCode     int               `json:"exit_code"`
	OOMKilled    bool              `json:"oom_killed"`
	Error        string            `json:"error,omitempty"`
	StartedAt    string            `json:"started_at"`
	FinishedAt   string            `json:"finished_at,omitempty"`
	CreatedAt    string            `json:"created_at"`
	Ports        []string          `json:"ports"`
	Networks     map[string]string `json:"networks"` // name → IP
	PR           string            `json:"pr,omitempty"`
	Deployment   string            `json:"deployment,omitempty"`
	ServerID     int64             `json:"server_id"`
	ServerName   string            `json:"server_name"`
}

type topoResource struct {
	Kind        string          `json:"kind"`
	ID          int64           `json:"id"`
	UUID        string          `json:"uuid"`
	Name        string          `json:"name"`
	Status      string          `json:"status"`
	Health      string          `json:"health"` // healthy | degraded | down | progressing | stopped
	Project     string          `json:"project"`
	ProjectID   int64           `json:"project_id"`
	Environment string          `json:"environment"`
	EnvID       int64           `json:"environment_id"`
	Image       string          `json:"image"`
	Domains     []string        `json:"domains"`
	Servers     []serverRef     `json:"servers"`
	Containers  []topoContainer `json:"containers"`
	CanWrite    bool            `json:"can_write"`
}

type inspectJSON struct {
	ID      string `json:"Id"`
	Name    string `json:"Name"`
	Created string `json:"Created"`
	Config  struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Status     string `json:"Status"`
		Restarting bool   `json:"Restarting"`
		OOMKilled  bool   `json:"OOMKilled"`
		ExitCode   int    `json:"ExitCode"`
		Error      string `json:"Error"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
		Health     *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	RestartCount    int `json:"RestartCount"`
	NetworkSettings struct {
		Ports    map[string][]struct{ HostIP, HostPort string } `json:"Ports"`
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// phase condenses docker state into what an operator cares about.
func phase(c inspectJSON, now time.Time) string {
	started, _ := time.Parse(time.RFC3339Nano, c.State.StartedAt)
	health := ""
	if c.State.Health != nil {
		health = c.State.Health.Status
	}
	switch {
	case c.State.Restarting && c.RestartCount >= 3:
		return "crashloop"
	case c.State.Restarting:
		return "restarting"
	case c.State.Status == "running" && health == "unhealthy":
		return "unhealthy"
	case c.State.Status == "running" && health == "starting":
		return "starting"
	case c.State.Status == "running" && c.RestartCount >= 3 && now.Sub(started) < 10*time.Minute:
		return "crashloop" // came back up, but keeps dying
	case c.State.Status == "running" && health == "healthy":
		return "healthy"
	case c.State.Status == "running":
		return "running"
	case c.State.Status == "paused":
		return "paused"
	case c.State.Status == "exited" && (c.State.ExitCode != 0 || c.State.OOMKilled):
		return "failed"
	case c.State.Status == "dead":
		return "failed"
	case c.State.Status == "created":
		return "created"
	}
	return "exited"
}

// managedContainers inspects every PortDock-managed container of a server in one round trip.
func managedContainers(ctx context.Context, x executor.Executor) ([]inspectJSON, error) {
	out, err := executor.Output(ctx, x, "ids=$("+docker.IDsCmd("ps", true, "label="+docker.LabelManaged+"=true")+"); [ -n \"$ids\" ] && docker inspect $ids || echo '[]'")
	if err != nil {
		return nil, err
	}
	var list []inspectJSON
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil, err
	}
	for i := range list {
		list[i].Config.Labels = docker.NormalizeLabels(list[i].Config.Labels)
	}
	return list, nil
}

func (s *Server) topology(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	q := r.URL.Query()
	visible := s.envVisibility(ctx)
	type res struct {
		Kind, Table string
	}
	type row struct {
		ID       int64  `db:"id"`
		UUID     string `db:"uuid"`
		Name     string `db:"name"`
		Status   string `db:"status"`
		EnvID    int64  `db:"environment_id"`
		ServerID int64  `db:"server_id"`
		Image    string `db:"image"`
	}
	envs, _ := store.Select[store.Environment](ctx, s.db, "SELECT * FROM environments")
	envByID := map[int64]store.Environment{}
	for _, e := range envs {
		envByID[e.ID] = e
	}
	projects, _ := store.Select[store.Project](ctx, s.db, "SELECT * FROM projects")
	projByID := map[int64]string{}
	for _, p := range projects {
		projByID[p.ID] = p.Name
	}
	filterProject, _ := strconv.ParseInt(q.Get("project"), 10, 64)
	filterEnv, _ := strconv.ParseInt(q.Get("environment"), 10, 64)
	filterKind, filterID := q.Get("kind"), q.Get("id")

	// Everything the resources refer to, loaded once instead of per resource.
	serverList, _ := store.Select[store.Server](ctx, s.db, "SELECT * FROM servers")
	serverByID := map[int64]store.Server{}
	for _, srv := range serverList {
		serverByID[srv.ID] = srv
	}
	type appServer struct {
		AppID    int64 `db:"application_id"`
		ServerID int64 `db:"server_id"`
	}
	extra := map[int64][]int64{}
	if rows, err := store.Select[appServer](ctx, s.db, "SELECT application_id, server_id FROM application_servers ORDER BY server_id"); err == nil {
		for _, r := range rows {
			extra[r.AppID] = append(extra[r.AppID], r.ServerID)
		}
	}
	domainsByApp := map[int64][]store.AppDomain{}
	if rows, err := store.Select[store.AppDomain](ctx, s.db, "SELECT * FROM app_domains ORDER BY id"); err == nil {
		for _, d := range rows {
			domainsByApp[d.ApplicationID] = append(domainsByApp[d.ApplicationID], d)
		}
	}
	canWrite := map[int64]bool{}

	var resources []*topoResource
	serversNeeded := map[int64]bool{}
	byUUID := map[string]*topoResource{}
	for _, t := range []res{{"application", "applications"}, {"database", "databases"}, {"service", "services"}} {
		if filterKind != "" && filterKind != t.Kind {
			continue
		}
		imageCol := "image"
		if t.Kind == "application" {
			imageCol = "CASE WHEN current_image != '' THEN current_image ELSE docker_image END"
		} else if t.Kind == "service" {
			imageCol = "template"
		}
		rows, err := store.Select[row](ctx, s.db, "SELECT id, uuid, name, status, environment_id, server_id, "+imageCol+" AS image FROM "+t.Table+" ORDER BY name")
		if err != nil {
			return err
		}
		for _, x := range rows {
			env := envByID[x.EnvID]
			if !visible(x.EnvID) || (filterEnv != 0 && x.EnvID != filterEnv) || (filterProject != 0 && env.ProjectID != filterProject) ||
				(filterID != "" && filterID != strconv.FormatInt(x.ID, 10)) {
				continue
			}
			tr := &topoResource{Kind: t.Kind, ID: x.ID, UUID: x.UUID, Name: x.Name, Status: x.Status, Project: projByID[env.ProjectID], ProjectID: env.ProjectID,
				Environment: env.Name, EnvID: env.ID, Image: x.Image, Domains: []string{}, Containers: []topoContainer{}}
			ids := []int64{x.ServerID}
			if t.Kind == "application" {
				for _, id := range extra[x.ID] {
					if id != x.ServerID {
						ids = append(ids, id)
					}
				}
				for _, d := range domainsByApp[x.ID] {
					tr.Domains = append(tr.Domains, d.URL())
				}
			}
			if t.Kind == "service" {
				var dom string
				_ = s.db.QueryRowContext(ctx, "SELECT domain FROM services WHERE id = ?", x.ID).Scan(&dom)
				tr.Domains = append(tr.Domains, store.SplitList(dom)...)
			}
			for _, id := range ids {
				serversNeeded[id] = true
				if srv, ok := serverByID[id]; ok {
					tr.Servers = append(tr.Servers, serverRef{ID: srv.ID, Name: srv.Name, Host: srv.Host, Status: srv.Status})
				}
			}
			if _, seen := canWrite[x.EnvID]; !seen {
				_, _, err := s.authorizeEnv(ctx, x.EnvID, permWrite)
				canWrite[x.EnvID] = err == nil
			}
			tr.CanWrite = canWrite[x.EnvID]
			resources = append(resources, tr)
			byUUID[x.UUID] = tr
		}
	}

	// Inspect the servers in parallel.
	var mu sync.Mutex
	var wg sync.WaitGroup
	errs := map[string]string{}
	now := time.Now()
	for sid := range serversNeeded {
		wg.Add(1)
		go func(sid int64) {
			defer wg.Done()
			x, srv, err := s.pool.ForID(ctx, sid)
			if err != nil {
				mu.Lock()
				errs[strconv.FormatInt(sid, 10)] = err.Error()
				mu.Unlock()
				return
			}
			// Shared by every open topology page for a few seconds.
			list, err := cached(s.cache, "inspect:"+strconv.FormatInt(sid, 10), 5*time.Second, func() ([]inspectJSON, error) {
				cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
				defer cancel()
				return managedContainers(cctx, x)
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[srv.Name] = err.Error()
				return
			}
			for _, c := range list {
				tr := byUUID[c.Config.Labels[docker.LabelResource]]
				if tr == nil {
					continue
				}
				tc := topoContainer{ID: c.ID, Name: strings.TrimPrefix(c.Name, "/"), Service: c.Config.Labels["com.docker.compose.service"], Image: c.Config.Image,
					State: c.State.Status, Phase: phase(c, now), RestartCount: c.RestartCount, ExitCode: c.State.ExitCode, OOMKilled: c.State.OOMKilled,
					Error: c.State.Error, StartedAt: c.State.StartedAt, CreatedAt: c.Created, Networks: map[string]string{}, Ports: []string{},
					PR: c.Config.Labels[docker.LabelPR], Deployment: c.Config.Labels[docker.LabelDeployment], ServerID: srv.ID, ServerName: srv.Name}
				if c.State.Health != nil {
					tc.Health = c.State.Health.Status
				}
				if c.State.Status != "running" {
					tc.FinishedAt = c.State.FinishedAt
				}
				for port, binds := range c.NetworkSettings.Ports {
					for _, b := range binds {
						tc.Ports = append(tc.Ports, b.HostPort+"→"+port)
					}
				}
				sort.Strings(tc.Ports)
				for n, v := range c.NetworkSettings.Networks {
					tc.Networks[n] = v.IPAddress
				}
				tr.Containers = append(tr.Containers, tc)
			}
		}(sid)
	}
	wg.Wait()
	for _, tr := range resources {
		sort.Slice(tr.Containers, func(i, j int) bool {
			a, b := tr.Containers[i], tr.Containers[j]
			if a.ServerName != b.ServerName {
				return a.ServerName < b.ServerName
			}
			return a.Name < b.Name
		})
		tr.Health = resourceHealth(tr)
	}
	return ok(w, map[string]any{"resources": resources, "errors": errs, "generated_at": now.UTC().Format(time.RFC3339)})
}

func resourceHealth(tr *topoResource) string {
	if tr.Status == "deploying" || tr.Status == "queued" {
		return "progressing"
	}
	up, bad, total := 0, 0, 0
	for _, c := range tr.Containers {
		if c.PR != "" && c.PR != "0" {
			continue
		}
		total++
		switch c.Phase {
		case "healthy", "running":
			up++
		case "crashloop", "failed", "unhealthy", "restarting":
			bad++
		}
	}
	switch {
	case total == 0:
		return "stopped"
	case bad == 0 && up == total:
		return "healthy"
	case up == 0 && bad > 0:
		return "down"
	case up == 0:
		return "stopped"
	}
	return "degraded"
}

var secretKeyRe = regexp.MustCompile(`(?i)(pass|secret|token|key|credential|private|auth|dsn|database_url|_url$)`)

// containerEnv lists a container's environment; values are only revealed to
// users who may change the resource.
func (s *Server) containerEnv(w http.ResponseWriter, r *http.Request) error {
	x, _, err := s.serverExec(r)
	if err != nil {
		return err
	}
	cid := r.PathValue("cid")
	if err := s.authorizeContainer(r, x, cid, permRead); err != nil {
		return err
	}
	reveal := s.authorizeContainer(r, x, cid, permWrite) == nil
	out, err := executor.Output(r.Context(), x, "docker inspect --format '{{json .Config.Env}}' "+executor.Quote(cid))
	if err != nil {
		return badRequest("%v", err)
	}
	var env []string
	_ = json.Unmarshal([]byte(out), &env)
	type item struct {
		Key    string `json:"key"`
		Value  string `json:"value"`
		Secret bool   `json:"secret"`
		Masked bool   `json:"masked"`
	}
	items := make([]item, 0, len(env))
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		it := item{Key: k, Value: v, Secret: secretKeyRe.MatchString(k)}
		if !reveal {
			it.Value, it.Masked = "", true
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
	return ok(w, map[string]any{"env": items, "revealed": reveal})
}

// stripEnv removes environment values from a docker inspect document.
func stripEnv(raw []byte) []byte {
	var list []map[string]any
	if json.Unmarshal(raw, &list) != nil {
		return []byte("[]")
	}
	for _, c := range list {
		if cfg, ok := c["Config"].(map[string]any); ok {
			if env, ok := cfg["Env"].([]any); ok {
				for i, e := range env {
					if s, ok := e.(string); ok {
						k, _, _ := strings.Cut(s, "=")
						env[i] = k + "=********"
					}
				}
			}
		}
	}
	b, _ := json.Marshal(list)
	return b
}
