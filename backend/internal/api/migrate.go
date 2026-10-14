package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/migrate"
	"github.com/portdock/portdock/internal/store"
)

// Import from Coolify, Heroku or Dokku (administrators): a plan of what
// would be created, then the creation of the chosen items through the API,
// with the administrator's rights, so every check and audit entry applies.
// Credentials are used for the request only, except a Heroku key that an
// imported application keeps to build from Heroku Git.

type migrateInput struct {
	Source   string `json:"source"`
	URL      string `json:"url"`
	Token    string `json:"token"`
	ServerID int64  `json:"server_id"` // Dokku: the server it runs on
}

func (s *Server) readMigration(r *http.Request, in migrateInput) (migrate.Plan, error) {
	c := migrate.Credentials{URL: in.URL, Token: in.Token}
	if in.Source == migrate.Dokku {
		srv, err := store.ByID[store.Server](r.Context(), s.db, "servers", in.ServerID)
		if err != nil {
			return migrate.Plan{}, badRequest("choose the server where Dokku runs")
		}
		x, err := s.pool.For(r.Context(), srv)
		if err != nil {
			return migrate.Plan{}, badRequest("%s: %v", srv.Name, err)
		}
		c.Run = func(ctx context.Context, cmd string) (string, error) { return executor.Output(ctx, x, cmd) }
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	p, err := migrate.Read(ctx, in.Source, c)
	if err != nil {
		return p, badRequest("%v", err)
	}
	return p, nil
}

// planMigration reads a source and returns what an import would create;
// secret values are not sent back.
func (s *Server) planMigration(w http.ResponseWriter, r *http.Request) error {
	var in migrateInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	p, err := s.readMigration(r, in)
	if err != nil {
		return err
	}
	mask := func(vs []migrate.Var) {
		for i := range vs {
			if vs[i].Secret {
				vs[i].Value = ""
			}
		}
	}
	for i := range p.Apps {
		mask(p.Apps[i].Env)
	}
	for i := range p.Services {
		mask(p.Services[i].Env)
	}
	return ok(w, p)
}

type migrateItem struct {
	Kind        string `json:"kind"` // app | database | service
	Ref         string `json:"ref"`
	Project     string `json:"project"`     // "" = as in the source
	Environment string `json:"environment"` // "" = as in the source
}

type migrateResult struct {
	Kind  string   `json:"kind"`
	Ref   string   `json:"ref"`
	Name  string   `json:"name"`
	ID    int64    `json:"id,omitempty"`
	Error string   `json:"error,omitempty"`
	Notes []string `json:"notes,omitempty"`
}

// applyMigration creates the chosen items of a plan, read again from the
// source, on the target server.
func (s *Server) applyMigration(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		migrateInput
		TargetServerID int64         `json:"target_server_id"`
		Items          []migrateItem `json:"items"`
		Deploy         bool          `json:"deploy"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if _, err := store.ByID[store.Server](r.Context(), s.db, "servers", in.TargetServerID); err != nil {
		return badRequest("choose the server to create the resources on")
	}
	if len(in.Items) == 0 {
		return badRequest("choose what to import")
	}
	p, err := s.readMigration(r, in.migrateInput)
	if err != nil {
		return err
	}
	envs := envResolver{s: s, r: r, ids: map[string]int64{}}
	results := make([]migrateResult, 0, len(in.Items))
	for _, item := range in.Items {
		res := migrateResult{Kind: item.Kind, Ref: item.Ref}
		place := func(project, env string) (int64, error) {
			if item.Project != "" {
				project = item.Project
			}
			if item.Environment != "" {
				env = item.Environment
			}
			return envs.get(project, env)
		}
		switch item.Kind {
		case "app":
			a, found := findBy(p.Apps, func(a migrate.App) bool { return a.Ref == item.Ref })
			if !found {
				res.Error = "not found in the source"
				break
			}
			res.Name = a.Name
			envID, err := place(a.Project, a.Environment)
			if err != nil {
				res.Error = err.Error()
				break
			}
			if in.Source == migrate.Dokku {
				a = s.dokkuImage(r, a, in.ServerID, in.TargetServerID)
			}
			res.ID, res.Notes, err = s.importApp(r, a, envID, in.TargetServerID, in.Deploy)
			if err != nil {
				res.Error = err.Error()
			}
		case "database":
			d, found := findBy(p.Databases, func(d migrate.Database) bool { return d.Ref == item.Ref })
			if !found {
				res.Error = "not found in the source"
				break
			}
			res.Name, res.Notes = d.Name, d.Notes
			envID, err := place(d.Project, d.Environment)
			if err != nil {
				res.Error = err.Error()
				break
			}
			var out struct{ ID int64 }
			body := map[string]any{"name": d.Name, "engine": d.Engine, "environment_id": envID, "server_id": in.TargetServerID, "start": in.Deploy}
			if d.Image != "" {
				body["image"] = d.Image
			}
			if err := s.call(r, "POST", "/api/databases", body, &out); err != nil {
				res.Error = err.Error()
			}
			res.ID = out.ID
		case "service":
			sv, found := findBy(p.Services, func(x migrate.Service) bool { return x.Ref == item.Ref })
			if !found {
				res.Error = "not found in the source"
				break
			}
			res.Name, res.Notes = sv.Name, sv.Notes
			envID, err := place(sv.Project, sv.Environment)
			if err != nil {
				res.Error = err.Error()
				break
			}
			var out struct{ ID int64 }
			if err := s.call(r, "POST", "/api/services", map[string]any{"name": sv.Name, "template": "custom", "compose": sv.Compose, "domain": sv.Domain,
				"environment_id": envID, "server_id": in.TargetServerID, "deploy": false}, &out); err != nil {
				res.Error = err.Error()
				break
			}
			res.ID = out.ID
			for _, v := range sv.Env {
				if err := s.call(r, "POST", fmt.Sprintf("/api/services/%d/envs", out.ID), map[string]any{"key": v.Key, "value": v.Value, "is_secret": v.Secret}, nil); err != nil {
					res.Notes = append(res.Notes, v.Key+": "+err.Error())
				}
			}
			if in.Deploy {
				if err := s.call(r, "POST", fmt.Sprintf("/api/services/%d/deploy", out.ID), map[string]any{}, nil); err != nil {
					res.Notes = append(res.Notes, "deploy: "+err.Error())
				}
			}
		default:
			res.Error = "unknown kind " + item.Kind
		}
		results = append(results, res)
	}
	failed := 0
	for _, res := range results {
		if res.Error != "" {
			failed++
		}
	}
	s.activity(r, "migration.import", fmt.Sprintf("%s: %d imported, %d failed", in.Source, len(results)-failed, failed))
	return ok(w, map[string]any{"results": results})
}

// dokkuImage gives the image Dokku built a name no registry has
// (localhost/dokku/<app>), so a deploy never pulls another image of the same
// name from Docker Hub and runs the one on the server.
func (s *Server) dokkuImage(r *http.Request, a migrate.App, dokkuServer, target int64) migrate.App {
	srv, err := store.ByID[store.Server](r.Context(), s.db, "servers", dokkuServer)
	if err != nil {
		return a
	}
	x, err := s.pool.For(r.Context(), srv)
	if err != nil {
		return a
	}
	local := "localhost/" + strings.TrimPrefix(a.Image, "localhost/")
	if _, err := executor.Output(r.Context(), x, "docker tag "+executor.Quote(a.Image)+" "+executor.Quote(local)); err != nil {
		a.Notes = append(a.Notes, "the image could not be tagged "+local+" on the Dokku server: "+err.Error())
		return a
	}
	a.Image = local
	if target != dokkuServer {
		a.Notes = append(a.Notes, "the image is on the Dokku server: copy it to this server (docker save | docker load) or push it to a registry before deploying")
	}
	return a
}

// importApp creates an application with its variables, volumes and jobs.
func (s *Server) importApp(r *http.Request, a migrate.App, envID, serverID int64, deploy bool) (int64, []string, error) {
	notes := append([]string{}, a.Notes...)
	body := map[string]any{
		"name": a.Name, "environment_id": envID, "server_id": serverID, "source_type": a.SourceType, "build_pack": a.BuildPack,
		"git_repository": a.GitRepo, "docker_image": a.Image, "port": a.Port, "replicas": a.Replicas, "health_check_path": a.HealthPath,
		"install_command": a.Install, "build_command": a.Build, "start_command": a.Start, "pre_deploy_command": a.PreDeploy, "post_deploy_command": a.PostDeploy,
		"cpu_limit": a.CPU, "memory_limit": a.Memory, "watch_paths": a.WatchPaths, "domains": strings.Join(a.Domains, ","),
	}
	set := func(k, v string) {
		if v != "" {
			body[k] = v
		}
	}
	set("git_branch", a.GitBranch)
	set("git_commit", a.GitCommit)
	set("git_token", a.GitToken)
	set("base_directory", a.BaseDirectory)
	set("dockerfile_path", a.Dockerfile)
	set("compose_path", a.ComposePath)
	set("publish_directory", a.Publish)
	if a.Port == 0 {
		delete(body, "port")
	}
	var out struct{ ID int64 }
	err := s.call(r, "POST", "/api/applications", body, &out)
	if err != nil && len(a.Domains) > 0 {
		// A domain already used here: create it without, and say so.
		body["domains"] = ""
		if err2 := s.call(r, "POST", "/api/applications", body, &out); err2 == nil {
			notes = append(notes, "domains not added ("+err.Error()+"): add them in the Domains tab")
			err = nil
		}
	}
	if err != nil {
		return 0, notes, err
	}
	path := fmt.Sprintf("/api/applications/%d", out.ID)
	for _, v := range a.Env {
		if err := s.call(r, "POST", path+"/envs", map[string]any{"key": v.Key, "value": v.Value, "is_build": v.Build, "is_preview": v.Preview, "is_secret": v.Secret}, nil); err != nil {
			notes = append(notes, v.Key+": "+err.Error())
		}
	}
	for _, v := range a.Volumes {
		if err := s.call(r, "POST", path+"/volumes", map[string]any{"name": v.Name, "mount_path": v.Path}, nil); err != nil {
			notes = append(notes, "volume "+v.Path+": "+err.Error())
		}
	}
	for _, j := range a.Jobs {
		if err := s.call(r, "POST", path+"/jobs", map[string]any{"name": j.Name, "schedule": j.Schedule, "command": j.Command, "enabled": true}, nil); err != nil {
			notes = append(notes, "job "+j.Name+": "+err.Error())
		}
	}
	if deploy {
		if err := s.call(r, "POST", path+"/deploy", map[string]any{}, nil); err != nil {
			notes = append(notes, "deploy: "+err.Error())
		}
	}
	return out.ID, notes, nil
}

// envResolver finds or creates the projects and environments of an import.
type envResolver struct {
	s   *Server
	r   *http.Request
	ids map[string]int64
}

func (e *envResolver) get(project, env string) (int64, error) {
	key := strings.ToLower(project) + "\x00" + strings.ToLower(env)
	if id, ok := e.ids[key]; ok {
		return id, nil
	}
	type environment struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	var projects []struct {
		ID           int64         `json:"id"`
		Name         string        `json:"name"`
		Environments []environment `json:"environments"`
	}
	if err := e.s.call(e.r, "GET", "/api/projects", nil, &projects); err != nil {
		return 0, err
	}
	var pid int64
	var penvs []environment
	for _, p := range projects {
		if strings.EqualFold(p.Name, project) {
			pid, penvs = p.ID, p.Environments
		}
	}
	if pid == 0 {
		var created struct {
			ID           int64         `json:"id"`
			Environments []environment `json:"environments"`
		}
		if err := e.s.call(e.r, "POST", "/api/projects", map[string]any{"name": project, "description": "Imported"}, &created); err != nil {
			return 0, fmt.Errorf("project %s: %w", project, err)
		}
		pid, penvs = created.ID, created.Environments
	}
	for _, en := range penvs {
		if strings.EqualFold(en.Name, env) {
			e.ids[key] = en.ID
			return en.ID, nil
		}
	}
	var created environment
	if err := e.s.call(e.r, "POST", fmt.Sprintf("/api/projects/%d/environments", pid), map[string]any{"name": env}, &created); err != nil {
		return 0, fmt.Errorf("environment %s of %s: %w", env, project, err)
	}
	e.ids[key] = created.ID
	return created.ID, nil
}

func findBy[T any](items []T, match func(T) bool) (T, bool) {
	for _, it := range items {
		if match(it) {
			return it, true
		}
	}
	var zero T
	return zero, false
}
