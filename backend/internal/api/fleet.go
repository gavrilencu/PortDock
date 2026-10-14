package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/store"
)

// The fleet views list the containers, images and volumes of every server
// together, each with its server and the PortDock resource it belongs to.

type fleetServer struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Host   string `json:"host"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Count  int    `json:"count"`
}

type fleetResource struct {
	Kind        string `json:"kind"`
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Project     string `json:"project"`
	ProjectID   int64  `json:"project_id"`
	Environment string `json:"environment"`
	envID       int64
}

type fleetContainer struct {
	docker.Container
	Resource *fleetResource `json:"resource"`
	Stats    *docker.Stat   `json:"stats,omitempty"`
}

// resourcesByUUID maps the UUID of every application, database and service to its resource.
func (s *Server) resourcesByUUID(ctx context.Context) map[string]*fleetResource {
	envs, _ := store.Select[store.Environment](ctx, s.db, "SELECT * FROM environments")
	projects, _ := store.Select[store.Project](ctx, s.db, "SELECT * FROM projects")
	projName := map[int64]string{}
	for _, p := range projects {
		projName[p.ID] = p.Name
	}
	envByID := map[int64]store.Environment{}
	for _, e := range envs {
		envByID[e.ID] = e
	}
	type row struct {
		ID    int64  `db:"id"`
		UUID  string `db:"uuid"`
		Name  string `db:"name"`
		EnvID int64  `db:"environment_id"`
	}
	out := map[string]*fleetResource{}
	for kind, table := range map[string]string{"application": "applications", "database": "databases", "service": "services"} {
		rows, _ := store.Select[row](ctx, s.db, "SELECT id, uuid, name, environment_id FROM "+table)
		for _, r := range rows {
			env := envByID[r.EnvID]
			out[r.UUID] = &fleetResource{Kind: kind, ID: r.ID, Name: r.Name, Project: projName[env.ProjectID], ProjectID: env.ProjectID, Environment: env.Name, envID: r.EnvID}
		}
	}
	return out
}

// containerResource finds the resource of a container from its PortDock or compose labels.
func containerResource(c docker.Container, res map[string]*fleetResource) *fleetResource {
	if r := res[c.Labels[docker.LabelResource]]; r != nil {
		return r
	}
	if p := c.Labels["com.docker.compose.project"]; strings.HasPrefix(p, "sy-") {
		for uuid, r := range res {
			if strings.EqualFold(uuid, strings.TrimPrefix(p, "sy-")) {
				return r
			}
		}
	}
	return nil
}

// eachServer runs fn on every server in parallel, with a timeout, and reports each server.
func (s *Server) eachServer(ctx context.Context, only int64, fn func(ctx context.Context, x executor.Executor, srv store.Server) (int, error)) []fleetServer {
	servers, _ := store.Select[store.Server](ctx, s.db, "SELECT * FROM servers ORDER BY is_local DESC, name")
	out := make([]fleetServer, 0, len(servers))
	var targets []store.Server
	for _, srv := range servers {
		if only != 0 && srv.ID != only {
			continue
		}
		fs := fleetServer{ID: srv.ID, Name: srv.Name, Host: srv.Host, Status: srv.Status}
		if srv.IsLocal {
			fs.Host = "localhost"
		}
		out = append(out, fs)
		targets = append(targets, srv)
	}
	// The list is complete before the goroutines start: appending to it while
	// they write their entry was a data race.
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i, srv := range targets {
		wg.Add(1)
		go func(i int, srv store.Server) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
			defer cancel()
			x, err := s.pool.For(cctx, srv)
			n := 0
			if err == nil {
				n, err = fn(cctx, x, srv)
			}
			mu.Lock()
			defer mu.Unlock()
			out[i].Count = n
			if err != nil {
				out[i].Error = err.Error()
			}
		}(i, srv)
	}
	wg.Wait()
	return out
}

func (s *Server) fleetContainers(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	withStats := r.URL.Query().Get("stats") == "1"
	only, _ := queryIDOptional(r, "server")
	res := s.resourcesByUUID(ctx)
	admin := auth.UserFrom(ctx).IsAdmin()
	visible := s.envVisibility(ctx)
	var mu sync.Mutex
	list := []fleetContainer{}
	servers := s.eachServer(ctx, only, func(ctx context.Context, x executor.Executor, srv store.Server) (int, error) {
		sid := strconv.FormatInt(srv.ID, 10)
		cs, err := cached(s.cache, "ps:"+sid, 5*time.Second, func() ([]docker.Container, error) { return docker.PS(ctx, x, true) })
		if err != nil {
			return 0, err
		}
		stats := map[string]docker.Stat{}
		if withStats {
			var ids []string
			for _, c := range cs {
				if c.State == "running" {
					ids = append(ids, c.ID)
				}
			}
			if len(ids) > 0 {
				// `docker stats` samples for a second on the server: one sample serves every viewer.
				if st, err := cached(s.cache, "stats:"+sid, 10*time.Second, func() ([]docker.Stat, error) { return docker.Stats(ctx, x, ids...) }); err == nil {
					for _, s := range st {
						stats[s.Container] = s
					}
				}
			}
		}
		n := 0
		mu.Lock()
		defer mu.Unlock()
		for _, c := range cs {
			fc := fleetContainer{Container: c, Resource: containerResource(c, res)}
			// Members only see the containers of resources they can access.
			if !admin && (fc.Resource == nil || !visible(fc.Resource.envID)) {
				continue
			}
			fc.ServerID, fc.ServerName = srv.ID, srv.Name
			for id, st := range stats {
				if strings.HasPrefix(c.ID, id) || strings.HasPrefix(id, c.ID) {
					st := st
					fc.Stats = &st
				}
			}
			list = append(list, fc)
			n++
		}
		return n, nil
	})
	sort.Slice(list, func(i, j int) bool {
		if list[i].ServerName != list[j].ServerName {
			return list[i].ServerName < list[j].ServerName
		}
		return list[i].Name < list[j].Name
	})
	return ok(w, map[string]any{"servers": servers, "containers": list})
}

type fleetImage struct {
	docker.Image
	ServerID   int64  `json:"server_id"`
	ServerName string `json:"server_name"`
	InUse      int    `json:"in_use"` // containers using it
}

func (s *Server) fleetImages(w http.ResponseWriter, r *http.Request) error {
	only, _ := queryIDOptional(r, "server")
	var mu sync.Mutex
	list := []fleetImage{}
	servers := s.eachServer(r.Context(), only, func(ctx context.Context, x executor.Executor, srv store.Server) (int, error) {
		imgs, err := docker.Images(ctx, x)
		if err != nil {
			return 0, err
		}
		used := map[string]int{}
		if out, err := executor.Output(ctx, x, "docker ps -a --format '{{.Image}}'"); err == nil {
			for _, l := range strings.Split(out, "\n") {
				if l = strings.TrimSpace(l); l != "" {
					used[l]++
				}
			}
		}
		mu.Lock()
		defer mu.Unlock()
		for _, im := range imgs {
			ref := im.Repository + ":" + im.Tag
			n := used[ref] + used[im.ID]
			if im.Tag == "latest" {
				n += used[im.Repository]
			}
			list = append(list, fleetImage{Image: im, ServerID: srv.ID, ServerName: srv.Name, InUse: n})
		}
		return len(imgs), nil
	})
	sort.Slice(list, func(i, j int) bool {
		if list[i].ServerName != list[j].ServerName {
			return list[i].ServerName < list[j].ServerName
		}
		return list[i].Repository+list[i].Tag < list[j].Repository+list[j].Tag
	})
	return ok(w, map[string]any{"servers": servers, "images": list})
}

type fleetVolume struct {
	docker.Volume
	ServerID   int64          `json:"server_id"`
	ServerName string         `json:"server_name"`
	Resource   *fleetResource `json:"resource"`
}

func (s *Server) fleetVolumes(w http.ResponseWriter, r *http.Request) error {
	only, _ := queryIDOptional(r, "server")
	res := s.resourcesByUUID(r.Context())
	var mu sync.Mutex
	list := []fleetVolume{}
	servers := s.eachServer(r.Context(), only, func(ctx context.Context, x executor.Executor, srv store.Server) (int, error) {
		vols, err := docker.Volumes(ctx, x)
		if err != nil {
			return 0, err
		}
		mu.Lock()
		defer mu.Unlock()
		for _, v := range vols {
			fv := fleetVolume{Volume: v, ServerID: srv.ID, ServerName: srv.Name}
			// Volumes of databases are named sy-<uuid>; compose volumes <project>_<name>;
			// application volumes pd-<uuid>_<name>.
			name := strings.TrimPrefix(strings.TrimPrefix(v.Name, "sy-"), "pd-")
			if i := strings.Index(name, "_"); i > 0 {
				name = name[:i]
			}
			fv.Resource = res[name]
			list = append(list, fv)
		}
		return len(vols), nil
	})
	sort.Slice(list, func(i, j int) bool {
		if list[i].ServerName != list[j].ServerName {
			return list[i].ServerName < list[j].ServerName
		}
		return list[i].Name < list[j].Name
	})
	return ok(w, map[string]any{"servers": servers, "volumes": list})
}

func queryIDOptional(r *http.Request, name string) (int64, error) {
	if r.URL.Query().Get(name) == "" {
		return 0, nil
	}
	return queryID(r, name)
}
