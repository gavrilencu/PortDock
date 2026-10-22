package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/portdock/portdock/internal/gitops"
	"github.com/portdock/portdock/internal/store"
)

// GitOps: POST /api/gitops/apply reconciles PortDock with a portdock.yaml and
// GET /api/applications/{id}/export renders an application as one.
//
// Apply runs every change through PortDock's own API handlers, with the
// caller's credentials: the same validation and permissions apply as in the
// interface, and a token's scope and projects limit what a file can change.

// change is one step of the plan.
type change struct {
	Resource string   `json:"resource"` // project | environment | application | domain | variable | volume | job | protection
	Name     string   `json:"name"`
	Action   string   `json:"action"` // create | update | delete | unchanged
	Fields   []string `json:"fields,omitempty"`
}

type applyResult struct {
	DryRun  bool     `json:"dry_run"`
	Changes []change `json:"changes"`
	Error   string   `json:"error,omitempty"`
}

// recorder is a minimal ResponseWriter for internal calls.
type recorder struct {
	code int
	hdr  http.Header
	body bytes.Buffer
}

func (r *recorder) Header() http.Header         { return r.hdr }
func (r *recorder) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *recorder) WriteHeader(code int)        { r.code = code }

// call runs an API request with the credentials of r and decodes the answer into out.
func (s *Server) call(r *http.Request, method, path string, body, out any) error {
	// Requests made for the AI assistant may only read, plus the few actions
	// the user confirmed (chattools.go).
	if chatGuarded(r.Context()) {
		if err := chatCallAllowed(method, path); err != nil {
			return err
		}
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(r.Context(), method, path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for _, h := range []string{"Cookie", "Authorization", "User-Agent"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	req.RemoteAddr, req.Host = r.RemoteAddr, r.Host
	rec := &recorder{code: 200, hdr: http.Header{}}
	s.self.ServeHTTP(rec, req)
	if rec.code >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.body.Bytes(), &e)
		if e.Error == "" {
			e.Error = http.StatusText(rec.code)
		}
		return fmt.Errorf("%s %s: %s", method, path, e.Error)
	}
	if out != nil {
		return json.Unmarshal(rec.body.Bytes(), out)
	}
	return nil
}

func (s *Server) gitopsApply(w http.ResponseWriter, r *http.Request) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return badRequest("could not read the file")
	}
	f, err := gitops.Parse(b)
	if err != nil {
		return badRequest("%v", err)
	}
	a := &applier{s: s, r: r, f: f, dry: r.URL.Query().Get("dry_run") == "true"}
	res := applyResult{DryRun: a.dry}
	if err := a.run(); err != nil {
		res.Changes, res.Error = a.changes, err.Error()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		return json.NewEncoder(w).Encode(res)
	}
	res.Changes = a.changes
	if res.Changes == nil {
		res.Changes = []change{}
	}
	if !a.dry {
		s.activity(r, "gitops.apply", fmt.Sprintf("%s/%s: %d change(s)", f.Project, f.Environment, countChanged(res.Changes)))
	}
	return ok(w, res)
}

func countChanged(cs []change) int {
	n := 0
	for _, c := range cs {
		if c.Action != "unchanged" {
			n++
		}
	}
	return n
}

type applier struct {
	s       *Server
	r       *http.Request
	f       gitops.File
	dry     bool
	changes []change
}

func (a *applier) add(resource, name, action string, fields ...string) {
	a.changes = append(a.changes, change{Resource: resource, Name: name, Action: action, Fields: fields})
}

// do performs a change unless this is a dry run.
func (a *applier) do(method, path string, body, out any) error {
	if a.dry {
		return nil
	}
	return a.s.call(a.r, method, path, body, out)
}

func (a *applier) run() error {
	var projects []struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		Environments []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"environments"`
	}
	if err := a.s.call(a.r, "GET", "/api/projects", nil, &projects); err != nil {
		return err
	}
	var projectID, envID int64
	for _, p := range projects {
		if p.Name == a.f.Project {
			projectID = p.ID
			for _, e := range p.Environments {
				if e.Name == a.f.Environment {
					envID = e.ID
				}
			}
		}
	}
	newProject := projectID == 0
	if newProject {
		a.add("project", a.f.Project, "create")
		var p struct {
			ID           int64 `json:"id"`
			Environments []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"environments"`
		}
		if err := a.do("POST", "/api/projects", map[string]string{"name": a.f.Project}, &p); err != nil {
			return err
		}
		projectID = p.ID
		for _, e := range p.Environments {
			if e.Name == a.f.Environment {
				envID = e.ID
			}
		}
	}
	// A new project comes with a production environment.
	if envID == 0 && !(newProject && a.f.Environment == "production") {
		a.add("environment", a.f.Project+"/"+a.f.Environment, "create")
		var e struct {
			ID int64 `json:"id"`
		}
		if err := a.do("POST", fmt.Sprintf("/api/projects/%d/environments", projectID), map[string]string{"name": a.f.Environment}, &e); err != nil {
			return err
		}
		envID = e.ID
	}

	var servers []struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		IsLocal bool   `json:"is_local"`
	}
	if err := a.s.call(a.r, "GET", "/api/servers", nil, &servers); err != nil {
		return err
	}
	serverID := func(name string) (int64, error) {
		for _, sv := range servers {
			if (name == "" && sv.IsLocal) || (name != "" && sv.Name == name) {
				return sv.ID, nil
			}
		}
		if name == "" && len(servers) > 0 {
			return servers[0].ID, nil
		}
		return 0, fmt.Errorf("server %q not found", name)
	}

	var apps []store.Application
	if err := a.s.call(a.r, "GET", "/api/applications", nil, &apps); err != nil {
		return err
	}
	for _, spec := range a.f.Applications {
		var current *store.Application
		for i := range apps {
			if apps[i].Name == spec.Name && apps[i].EnvironmentID == envID && envID != 0 {
				current = &apps[i]
			}
		}
		srvName := firstNonEmpty(spec.Server, a.f.Server)
		sid, err := serverID(srvName)
		if err != nil {
			return fmt.Errorf("application %s: %w", spec.Name, err)
		}
		if err := a.application(spec, current, envID, sid); err != nil {
			return fmt.Errorf("application %s: %w", spec.Name, err)
		}
	}
	return nil
}

// desiredFields maps a spec onto the JSON fields of an application.
func desiredFields(spec gitops.App, envID, serverID int64) map[string]any {
	m := map[string]any{"name": spec.Name, "environment_id": envID, "server_id": serverID}
	set := func(k string, v any) { m[k] = v }
	if spec.Description != "" {
		set("description", spec.Description)
	}
	if spec.Git != nil {
		set("source_type", "git")
		set("git_repository", spec.Git.Repository)
		set("git_branch", firstNonEmpty(spec.Git.Branch, "main"))
		set("git_commit", spec.Git.Commit)
	} else {
		set("source_type", "image")
		set("build_pack", "image")
		set("docker_image", spec.Image)
	}
	if b := spec.Build; b != nil {
		for k, v := range map[string]string{"build_pack": b.Pack, "base_directory": b.BaseDirectory, "dockerfile_path": b.Dockerfile, "compose_path": b.ComposeFile,
			"compose_service": b.ComposeService, "install_command": b.Install, "build_command": b.Command, "start_command": b.Start, "publish_directory": b.PublishDirectory} {
			if v != "" {
				set(k, v)
			}
		}
	}
	if spec.Port > 0 {
		set("port", spec.Port)
	}
	if spec.Replicas > 0 {
		set("replicas", spec.Replicas)
	}
	if r := spec.Resources; r != nil {
		set("cpu_limit", r.CPU)
		set("memory_limit", r.Memory)
	}
	if h := spec.HealthCheck; h != nil {
		set("health_check_path", h.Path)
		if h.Timeout > 0 {
			set("health_check_timeout", h.Timeout)
		}
	}
	if d := spec.Deploy; d != nil {
		if d.Auto != nil {
			set("auto_deploy", *d.Auto)
		}
		set("pre_deploy_command", d.PreCommand)
		set("post_deploy_command", d.PostCommand)
		set("watch_paths", strings.Join(d.WatchPaths, "\n"))
		if d.Previews != nil {
			set("preview_enabled", *d.Previews)
		}
		if d.PreviewDomain != "" {
			set("preview_domain", d.PreviewDomain)
		}
	}
	return m
}

func (a *applier) application(spec gitops.App, current *store.Application, envID, serverID int64) error {
	want := desiredFields(spec, envID, serverID)
	var appID int64
	if current == nil {
		a.add("application", spec.Name, "create")
		var created store.Application
		if err := a.do("POST", "/api/applications", want, &created); err != nil {
			return err
		}
		appID = created.ID
	} else {
		appID = current.ID
		var cur map[string]any
		b, _ := json.Marshal(current)
		_ = json.Unmarshal(b, &cur)
		patch := map[string]any{}
		for k, v := range want {
			wb, _ := json.Marshal(v)
			cb, _ := json.Marshal(cur[k])
			if string(wb) != string(cb) {
				patch[k] = v
			}
		}
		if len(patch) == 0 {
			a.add("application", spec.Name, "unchanged")
		} else {
			fields := make([]string, 0, len(patch))
			for k := range patch {
				fields = append(fields, k)
			}
			sort.Strings(fields)
			a.add("application", spec.Name, "update", fields...)
			if err := a.do("PATCH", fmt.Sprintf("/api/applications/%d", appID), patch, nil); err != nil {
				return err
			}
		}
	}
	// A new application in a dry run has nothing to compare its parts to.
	exists := appID != 0
	if err := a.domains(spec, appID, exists); err != nil {
		return err
	}
	if err := a.variables(spec, appID, exists); err != nil {
		return err
	}
	if err := a.volumes(spec, appID, exists); err != nil {
		return err
	}
	if err := a.jobs(spec, appID, exists); err != nil {
		return err
	}
	return a.protection(spec, appID, exists)
}

func (a *applier) domains(spec gitops.App, appID int64, exists bool) error {
	var current domainsResponse
	if exists {
		if err := a.s.call(a.r, "GET", fmt.Sprintf("/api/applications/%d/domains", appID), nil, &current); err != nil {
			return err
		}
	}
	byKey := map[string]store.AppDomain{}
	for _, d := range current.Domains {
		byKey[d.Host+d.Path] = d
	}
	seen := map[string]bool{}
	for _, d := range spec.Domains {
		host, path, https, err := parseDomain(d.Domain)
		if err != nil {
			return err
		}
		key := host + path
		seen[key] = true
		body := map[string]any{"domain": d.Domain, "port": d.Port, "service": d.Service, "www_redirect": firstNonEmpty(d.WWWRedirect, "none"), "cert_mode": firstNonEmpty(d.Certificate, "auto")}
		if https != nil {
			body["https"] = *https
		}
		if d.Indexing != nil {
			body["indexing"] = *d.Indexing
		}
		cur, ok := byKey[key]
		if !ok {
			a.add("domain", key, "create")
			if err := a.do("POST", fmt.Sprintf("/api/applications/%d/domains", appID), body, nil); err != nil {
				return err
			}
			continue
		}
		var fields []string
		if cur.Port != d.Port {
			fields = append(fields, "port")
		}
		if cur.Service != d.Service {
			fields = append(fields, "service")
		}
		if cur.WWWRedirect != firstNonEmpty(d.WWWRedirect, "none") {
			fields = append(fields, "www_redirect")
		}
		if https != nil && cur.HTTPS != *https {
			fields = append(fields, "https")
		}
		if d.Indexing != nil && cur.Indexing != *d.Indexing {
			fields = append(fields, "indexing")
		}
		if firstNonEmpty(cur.CertMode, "auto") != firstNonEmpty(d.Certificate, "auto") {
			fields = append(fields, "certificate")
		}
		if len(fields) == 0 {
			a.add("domain", key, "unchanged")
			continue
		}
		a.add("domain", key, "update", fields...)
		if err := a.do("PATCH", fmt.Sprintf("/api/domains/%d", cur.ID), body, nil); err != nil {
			return err
		}
	}
	if a.f.Prune {
		for key, d := range byKey {
			if !seen[key] {
				a.add("domain", key, "delete")
				if err := a.do("DELETE", fmt.Sprintf("/api/domains/%d", d.ID), nil, nil); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (a *applier) variables(spec gitops.App, appID int64, exists bool) error {
	var current []store.EnvVar
	if exists {
		if err := a.s.call(a.r, "GET", fmt.Sprintf("/api/applications/%d/envs", appID), nil, &current); err != nil {
			return err
		}
	}
	byKey := map[string]store.EnvVar{}
	for _, v := range current {
		if v.Service == "" && !v.IsPreview && !v.IsBuild {
			byKey[v.Key] = v
		}
	}
	for _, k := range spec.Secrets {
		if _, ok := byKey[k]; !ok && exists {
			return fmt.Errorf("the secret %s is not set: add it in PortDock (Environment tab) before applying", k)
		}
	}
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := spec.Env[k]
		cur, ok := byKey[k]
		switch {
		case !ok:
			a.add("variable", k, "create")
			if err := a.do("POST", fmt.Sprintf("/api/applications/%d/envs", appID), map[string]any{"key": k, "value": v}, nil); err != nil {
				return err
			}
		case cur.RefStoreID > 0:
			return fmt.Errorf("%s comes from a secret store in PortDock: list it under secret_refs, not env", k)
		case cur.IsSecret:
			return fmt.Errorf("%s is a secret in PortDock: list it under secrets, not env", k)
		case cur.Value != v:
			a.add("variable", k, "update", "value")
			if err := a.do("PATCH", fmt.Sprintf("/api/envs/%d", cur.ID), map[string]any{"value": v}, nil); err != nil {
				return err
			}
		default:
			a.add("variable", k, "unchanged")
		}
	}
	if err := a.secretRefs(spec, appID, byKey); err != nil {
		return err
	}
	if a.f.Prune {
		for k, v := range byKey {
			_, ref := spec.SecretRefs[k]
			if v.RefStoreID > 0 && !ref && !slices.Contains(spec.Secrets, k) {
				a.add("variable", k, "delete")
				if err := a.do("DELETE", fmt.Sprintf("/api/envs/%d", v.ID), nil, nil); err != nil {
					return err
				}
				continue
			}
			if _, keep := spec.Env[k]; !keep && !ref && !v.IsSecret && !slices.Contains(spec.Secrets, k) {
				a.add("variable", k, "delete")
				if err := a.do("DELETE", fmt.Sprintf("/api/envs/%d", v.ID), nil, nil); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// secretRefs points the variables of secret_refs at their secret store.
func (a *applier) secretRefs(spec gitops.App, appID int64, byKey map[string]store.EnvVar) error {
	if len(spec.SecretRefs) == 0 {
		return nil
	}
	var stores []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := a.s.call(a.r, "GET", "/api/secret-stores/names", nil, &stores); err != nil {
		return err
	}
	ids := map[string]int64{}
	for _, st := range stores {
		ids[st.Name] = st.ID
	}
	keys := make([]string, 0, len(spec.SecretRefs))
	for k := range spec.SecretRefs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		name, path, key, err := gitops.ParseRef(spec.SecretRefs[k])
		if err != nil {
			return fmt.Errorf("secret_refs %s: %w", k, err)
		}
		id, found := ids[name]
		if !found {
			return fmt.Errorf("secret_refs %s: no secret store named %s (Settings → Secret managers)", k, name)
		}
		body := map[string]any{"ref_store_id": id, "ref_path": path, "ref_key": key}
		cur, exists := byKey[k]
		switch {
		case !exists:
			a.add("variable", k, "create", "secret store")
			body["key"] = k
			if err := a.do("POST", fmt.Sprintf("/api/applications/%d/envs", appID), body, nil); err != nil {
				return err
			}
		case cur.RefStoreID == id && cur.RefPath == path && cur.RefKey == key:
			a.add("variable", k, "unchanged")
		default:
			a.add("variable", k, "update", "secret store")
			if err := a.do("PATCH", fmt.Sprintf("/api/envs/%d", cur.ID), body, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *applier) volumes(spec gitops.App, appID int64, exists bool) error {
	var current volumesResponse
	if exists {
		if err := a.s.call(a.r, "GET", fmt.Sprintf("/api/applications/%d/volumes", appID), nil, &current); err != nil {
			return err
		}
	}
	byName := map[string]store.AppVolume{}
	for _, v := range current.Volumes {
		byName[v.Name] = v
	}
	seen := map[string]bool{}
	for _, v := range spec.Volumes {
		seen[v.Name] = true
		body := map[string]any{"name": v.Name, "mount_path": v.Path, "read_only": v.ReadOnly, "host_path": v.HostPath}
		cur, ok := byName[v.Name]
		if !ok {
			a.add("volume", v.Name, "create")
			if err := a.do("POST", fmt.Sprintf("/api/applications/%d/volumes", appID), body, nil); err != nil {
				return err
			}
			continue
		}
		var fields []string
		if cur.MountPath != strings.TrimRight(v.Path, "/") {
			fields = append(fields, "path")
		}
		if cur.ReadOnly != v.ReadOnly {
			fields = append(fields, "read_only")
		}
		if cur.HostPath != v.HostPath {
			fields = append(fields, "host_path")
		}
		if len(fields) == 0 {
			a.add("volume", v.Name, "unchanged")
			continue
		}
		a.add("volume", v.Name, "update", fields...)
		if err := a.do("PATCH", fmt.Sprintf("/api/volumes/%d", cur.ID), body, nil); err != nil {
			return err
		}
	}
	if a.f.Prune {
		for name, v := range byName {
			if !seen[name] {
				a.add("volume", name, "delete")
				if err := a.do("DELETE", fmt.Sprintf("/api/volumes/%d", v.ID), nil, nil); err != nil { // data kept
					return err
				}
			}
		}
	}
	return nil
}

func (a *applier) jobs(spec gitops.App, appID int64, exists bool) error {
	var current []jobDTO
	if exists {
		if err := a.s.call(a.r, "GET", fmt.Sprintf("/api/applications/%d/jobs", appID), nil, &current); err != nil {
			return err
		}
	}
	byName := map[string]jobDTO{}
	for _, j := range current {
		byName[j.Name] = j
	}
	seen := map[string]bool{}
	for _, j := range spec.Jobs {
		seen[j.Name] = true
		enabled := j.Enabled == nil || *j.Enabled
		timeout := j.Timeout
		if timeout == 0 {
			timeout = 3600
		}
		body := map[string]any{"name": j.Name, "schedule": j.Schedule, "command": j.Command, "service": j.Service, "timeout": timeout, "enabled": enabled}
		cur, ok := byName[j.Name]
		if !ok {
			a.add("job", j.Name, "create")
			if err := a.do("POST", fmt.Sprintf("/api/applications/%d/jobs", appID), body, nil); err != nil {
				return err
			}
			continue
		}
		var fields []string
		for f, differs := range map[string]bool{"schedule": cur.Schedule != j.Schedule, "command": cur.Command != j.Command, "service": cur.Service != j.Service,
			"timeout": cur.Timeout != timeout, "enabled": cur.Enabled != enabled} {
			if differs {
				fields = append(fields, f)
			}
		}
		sort.Strings(fields)
		if len(fields) == 0 {
			a.add("job", j.Name, "unchanged")
			continue
		}
		a.add("job", j.Name, "update", fields...)
		if err := a.do("PATCH", fmt.Sprintf("/api/jobs/%d", cur.ID), body, nil); err != nil {
			return err
		}
	}
	if a.f.Prune {
		for name, j := range byName {
			if !seen[name] {
				a.add("job", name, "delete")
				if err := a.do("DELETE", fmt.Sprintf("/api/jobs/%d", j.ID), nil, nil); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (a *applier) protection(spec gitops.App, appID int64, exists bool) error {
	p := spec.Protection
	if p == nil {
		return nil
	}
	var current protectionDTO
	if exists {
		if err := a.s.call(a.r, "GET", fmt.Sprintf("/api/applications/%d/protection", appID), nil, &current); err != nil {
			return err
		}
	}
	for _, u := range p.Users {
		if exists && !slices.Contains(current.Users, u) {
			return fmt.Errorf("password user %s does not exist: passwords are set in PortDock (Domains → Access protection), never in the file", u)
		}
	}
	users := make([]map[string]string, 0, len(p.Users))
	for _, u := range p.Users {
		users = append(users, map[string]string{"user": u})
	}
	body := map[string]any{"auth": p.Auth, "sso_access": firstNonEmpty(p.SSOAccess, "project"), "sso_emails": strings.Join(p.SSOEmails, "\n"),
		"ip_allow": strings.Join(p.IPAllow, "\n"), "ip_depth": p.IPDepth, "users": users}
	if p.RateLimit != nil {
		body["rate_average"], body["rate_burst"] = p.RateLimit.Average, p.RateLimit.Burst
	}
	var fields []string
	cmp := map[string]bool{
		"auth":       current.Auth != p.Auth,
		"sso_access": current.SSOAccess != firstNonEmpty(p.SSOAccess, "project"),
		"sso_emails": current.SSOEmails != strings.Join(p.SSOEmails, "\n"),
		"ip_allow":   current.IPAllow != strings.Join(p.IPAllow, "\n"),
		"ip_depth":   current.IPDepth != p.IPDepth,
		"users":      strings.Join(current.Users, ",") != strings.Join(p.Users, ","),
	}
	if p.RateLimit != nil {
		cmp["rate_limit"] = current.RateAverage != p.RateLimit.Average || current.RateBurst != p.RateLimit.Burst
	}
	for f, differs := range cmp {
		if differs {
			fields = append(fields, f)
		}
	}
	sort.Strings(fields)
	if len(fields) == 0 {
		a.add("protection", spec.Name, "unchanged")
		return nil
	}
	a.add("protection", spec.Name, "update", fields...)
	return a.do("PUT", fmt.Sprintf("/api/applications/%d/protection", appID), body, nil)
}

// ---- export ----

func (s *Server) exportApplication(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	env, _ := store.ByID[store.Environment](ctx, s.db, "environments", app.EnvironmentID)
	project, _ := store.ByID[store.Project](ctx, s.db, "projects", env.ProjectID)
	srv, _ := store.ByID[store.Server](ctx, s.db, "servers", app.ServerID)
	spec := gitops.App{Name: app.Name, Description: app.Description, Server: srv.Name, Port: app.Port, Replicas: app.Replicas}
	if app.SourceType == "image" {
		spec.Image = app.DockerImage
	} else {
		spec.Git = &gitops.Git{Repository: app.GitRepository, Branch: app.GitBranch, Commit: app.GitCommit}
		spec.Build = &gitops.Build{Pack: app.BuildPack, BaseDirectory: app.BaseDirectory, Dockerfile: app.DockerfilePath, Install: app.InstallCommand,
			Command: app.BuildCommand, Start: app.StartCommand, PublishDirectory: app.PublishDirectory}
		if app.BuildPack == "compose" {
			spec.Build.ComposeFile, spec.Build.ComposeService = app.ComposePath, app.ComposeService
		}
	}
	if app.CPULimit != "" || app.MemoryLimit != "" {
		spec.Resources = &gitops.Resources{CPU: app.CPULimit, Memory: app.MemoryLimit}
	}
	if app.HealthCheckPath != "" {
		spec.HealthCheck = &gitops.Health{Path: app.HealthCheckPath, Timeout: app.HealthCheckTimeout}
	}
	auto, previews := app.AutoDeploy, app.PreviewEnabled
	spec.Deploy = &gitops.Deploy{Auto: &auto, PreCommand: app.PreDeployCommand, PostCommand: app.PostDeployCommand, Previews: &previews, PreviewDomain: app.PreviewDomain}
	if wp := strings.TrimSpace(app.WatchPaths); wp != "" {
		spec.Deploy.WatchPaths = strings.Split(wp, "\n")
	}
	ds, _ := s.db.Domains(ctx, app.ID)
	for _, d := range ds {
		indexing := d.Indexing
		spec.Domains = append(spec.Domains, gitops.Domain{Domain: d.URL(), Port: d.Port, Service: d.Service, WWWRedirect: d.WWWRedirect, Indexing: &indexing, Certificate: d.CertMode})
	}
	vars, _ := store.Select[store.EnvVar](ctx, s.db, "SELECT * FROM env_vars WHERE owner_type = 'application' AND owner_id = ? AND service = '' AND is_preview = 0 AND is_build = 0 ORDER BY key", app.ID)
	names := s.storeNames(ctx)
	for _, v := range vars {
		if v.RefStoreID > 0 {
			if spec.SecretRefs == nil {
				spec.SecretRefs = map[string]string{}
			}
			spec.SecretRefs[v.Key] = gitops.Ref(names[v.RefStoreID], v.RefPath, v.RefKey)
			continue
		}
		if v.IsSecret {
			spec.Secrets = append(spec.Secrets, v.Key)
			continue
		}
		if spec.Env == nil {
			spec.Env = gitops.Env{}
		}
		spec.Env[v.Key] = s.box.MustDecrypt(v.Value)
	}
	vols, _ := s.db.Volumes(ctx, app.ID)
	for _, v := range vols {
		spec.Volumes = append(spec.Volumes, gitops.Volume{Name: v.Name, Path: v.MountPath, ReadOnly: v.ReadOnly, HostPath: v.HostPath})
	}
	jobs, _ := store.Select[store.AppJob](ctx, s.db, "SELECT * FROM app_jobs WHERE application_id = ? ORDER BY name", app.ID)
	for _, j := range jobs {
		enabled := j.Enabled
		spec.Jobs = append(spec.Jobs, gitops.Job{Name: j.Name, Schedule: j.Schedule, Command: j.Command, Service: j.Service, Timeout: j.Timeout, Enabled: &enabled})
	}
	if p, err := s.db.Protection(ctx, app.ID); err == nil && (p.Auth != "" || p.IPAllow != "" || p.RateAverage > 0) {
		pp := &gitops.Protection{Auth: p.Auth, SSOAccess: p.SSOAccess, IPDepth: p.IPDepth}
		for _, l := range strings.Split(p.SSOEmails, "\n") {
			if l != "" {
				pp.SSOEmails = append(pp.SSOEmails, l)
			}
		}
		for _, l := range strings.Split(p.IPAllow, "\n") {
			if l != "" {
				pp.IPAllow = append(pp.IPAllow, l)
			}
		}
		for _, u := range p.Users() {
			pp.Users = append(pp.Users, u.User)
		}
		if p.RateAverage > 0 {
			pp.RateLimit = &gitops.RateLimit{Average: p.RateAverage, Burst: p.RateBurst}
		}
		spec.Protection = pp
	}
	file := gitops.File{Version: gitops.Version, Project: project.Name, Environment: env.Name, Applications: []gitops.App{spec}}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="portdock.yaml"`)
	_, err = w.Write(gitops.Marshal(file))
	return err
}
