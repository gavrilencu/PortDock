package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/templates"
)

// Coolify v4's REST API (/api/v1, a bearer token from Keys & Tokens → API tokens).

var errUnknownSource = errors.New("the source must be coolify, heroku or dokku")

type httpAPI struct {
	base    string
	headers map[string]string
	client  *http.Client
}

func (h httpAPI) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", h.base+path, nil)
	if err != nil {
		return err
	}
	for k, v := range h.headers {
		req.Header.Set(k, v)
	}
	res, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s is unreachable: %w", h.base, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return fmt.Errorf("the token was refused (HTTP %d)", res.StatusCode)
	case res.StatusCode >= 300:
		msg := strings.TrimSpace(string(b))
		var e struct{ Message string }
		if json.Unmarshal(b, &e) == nil && e.Message != "" {
			msg = e.Message
		}
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return fmt.Errorf("GET %s: HTTP %d %s", path, res.StatusCode, msg)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("GET %s: unexpected answer: %w", path, err)
	}
	return nil
}

type coolifyEnvVar struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	RealValue string `json:"real_value"`
	Build     bool   `json:"is_buildtime"`
	Preview   bool   `json:"is_preview"`
	ShownOnce bool   `json:"is_shown_once"`
}

type coolifyApp struct {
	UUID               string `json:"uuid"`
	Name               string `json:"name"`
	EnvironmentID      int64  `json:"environment_id"`
	GitRepository      string `json:"git_repository"`
	GitFullURL         string `json:"git_full_url"`
	GitBranch          string `json:"git_branch"`
	GitCommit          string `json:"git_commit_sha"`
	BuildPack          string `json:"build_pack"`
	BaseDirectory      string `json:"base_directory"`
	DockerfileLoc      string `json:"dockerfile_location"`
	Dockerfile         string `json:"dockerfile"`
	ComposeLocation    string `json:"docker_compose_location"`
	Install            string `json:"install_command"`
	Build              string `json:"build_command"`
	Start              string `json:"start_command"`
	Publish            string `json:"publish_directory"`
	PortsExposes       string `json:"ports_exposes"`
	FQDN               string `json:"fqdn"`
	HealthEnabled      bool   `json:"health_check_enabled"`
	HealthPath         string `json:"health_check_path"`
	Image              string `json:"docker_registry_image_name"`
	ImageTag           string `json:"docker_registry_image_tag"`
	LimitsMemory       string `json:"limits_memory"`
	LimitsCPUs         string `json:"limits_cpus"`
	PreDeploy          string `json:"pre_deployment_command"`
	PostDeploy         string `json:"post_deployment_command"`
	WatchPaths         string `json:"watch_paths"`
	BasicAuth          bool   `json:"is_http_basic_auth_enabled"`
	CustomDockerOpts   string `json:"custom_docker_run_options"`
	PreviewURLTemplate string `json:"preview_url_template"`
}

func readCoolify(ctx context.Context, base, token string) (Plan, error) {
	var p Plan
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return p, errors.New("the Coolify address must be an http(s) URL, e.g. https://coolify.example.com")
	}
	if strings.TrimSpace(token) == "" {
		return p, errors.New("an API token of Coolify is required (Keys & Tokens → API tokens, with read access)")
	}
	api := httpAPI{base: strings.TrimRight(strings.TrimSuffix(strings.TrimRight(base, "/"), "/api/v1"), "/") + "/api/v1",
		headers: map[string]string{"Authorization": "Bearer " + strings.TrimSpace(token), "Accept": "application/json"},
		client:  &http.Client{Timeout: 30 * time.Second}}

	// Projects and environments, to place each resource.
	var projects []struct {
		UUID string `json:"uuid"`
		Name string `json:"name"`
	}
	if err := api.get(ctx, "/projects", &projects); err != nil {
		return p, err
	}
	type place struct{ project, env string }
	places := map[int64]place{}
	for _, pr := range projects {
		var envs []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		}
		if err := api.get(ctx, "/projects/"+pr.UUID+"/environments", &envs); err != nil {
			p.Warnings = append(p.Warnings, fmt.Sprintf("environments of project %s: %v", pr.Name, err))
			continue
		}
		for _, e := range envs {
			places[e.ID] = place{pr.Name, e.Name}
		}
	}

	var apps []coolifyApp
	if err := api.get(ctx, "/applications", &apps); err != nil {
		return p, err
	}
	for _, ca := range apps {
		pl := places[ca.EnvironmentID]
		a := App{Ref: ca.UUID, Name: ca.Name, Project: pl.project, Environment: pl.env, GitBranch: ca.GitBranch, GitCommit: strings.TrimSpace(ca.GitCommit),
			Install: ca.Install, Build: ca.Build, Start: ca.Start, PreDeploy: ca.PreDeploy, PostDeploy: ca.PostDeploy, WatchPaths: ca.WatchPaths,
			BaseDirectory: orDefault(ca.BaseDirectory, "/"), Replicas: 1}
		if a.GitCommit == "HEAD" {
			a.GitCommit = ""
		}
		if ca.HealthEnabled {
			a.HealthPath = ca.HealthPath
		}
		a.Port, _ = strconv.Atoi(strings.TrimSpace(strings.Split(ca.PortsExposes, ",")[0]))
		for _, d := range strings.Split(ca.FQDN, ",") {
			if d = strings.TrimSpace(d); d != "" {
				a.Domains = append(a.Domains, d)
			}
		}
		if m := strings.TrimSpace(ca.LimitsMemory); m != "" && m != "0" {
			a.Memory = m
		}
		if c := strings.TrimSpace(ca.LimitsCPUs); c != "" && c != "0" {
			a.CPU = c
		}
		switch ca.BuildPack {
		case "dockerimage":
			a.SourceType, a.BuildPack = "image", "image"
			a.Image = ca.Image
			if ca.ImageTag != "" {
				a.Image += ":" + ca.ImageTag
			}
		default:
			a.SourceType = "git"
			a.GitRepo = coolifyRepo(ca)
			switch ca.BuildPack {
			case "dockerfile":
				a.BuildPack, a.Dockerfile = "dockerfile", strings.TrimPrefix(orDefault(ca.DockerfileLoc, "/Dockerfile"), "/")
			case "dockercompose":
				a.BuildPack, a.ComposePath = "compose", strings.TrimPrefix(orDefault(ca.ComposeLocation, "/docker-compose.yaml"), "/")
			case "static":
				a.BuildPack, a.Publish = "static", ca.Publish
			default:
				a.BuildPack = "nixpacks"
			}
			if a.GitRepo == "" {
				if ca.Dockerfile != "" {
					a.Notes = append(a.Notes, "built from a Dockerfile written in Coolify: commit it to a repository and set the repository here")
				} else {
					a.Notes = append(a.Notes, "no repository: set one before deploying")
				}
			}
			if strings.HasPrefix(a.GitRepo, "git@") || strings.HasPrefix(a.GitRepo, "ssh://") {
				a.Notes = append(a.Notes, "cloned over SSH: add the deploy key PortDock generates to the repository (Configuration tab)")
			}
		}
		if ca.BasicAuth {
			a.Notes = append(a.Notes, "Coolify protected it with a password: set it again in Domains → Access protection")
		}
		if strings.TrimSpace(ca.CustomDockerOpts) != "" {
			a.Notes = append(a.Notes, "custom Docker options were not copied: "+strings.TrimSpace(ca.CustomDockerOpts))
		}
		var envs []coolifyEnvVar
		if err := api.get(ctx, "/applications/"+ca.UUID+"/envs", &envs); err != nil {
			a.Notes = append(a.Notes, "variables: "+err.Error())
		}
		a.Env = coolifyVars(envs)
		var storages struct {
			Persistent []struct {
				Name      string `json:"name"`
				MountPath string `json:"mount_path"`
				HostPath  string `json:"host_path"`
			} `json:"persistent_storages"`
			Files []struct {
				MountPath string `json:"mount_path"`
			} `json:"file_storages"`
		}
		if err := api.get(ctx, "/applications/"+ca.UUID+"/storages", &storages); err == nil {
			for _, s := range storages.Persistent {
				if s.HostPath != "" {
					a.Notes = append(a.Notes, fmt.Sprintf("the server directory %s mounted at %s became a volume", s.HostPath, s.MountPath))
				}
				a.Volumes = append(a.Volumes, Volume{Name: resourceName(strings.TrimPrefix(s.Name, ca.UUID+"-")), Path: s.MountPath})
			}
			for _, f := range storages.Files {
				a.Notes = append(a.Notes, "the file mounted at "+f.MountPath+" was not copied: put it in the image or in a variable")
			}
		}
		var tasks []struct {
			Name      string `json:"name"`
			Command   string `json:"command"`
			Frequency string `json:"frequency"`
			Enabled   bool   `json:"enabled"`
		}
		if err := api.get(ctx, "/applications/"+ca.UUID+"/scheduled-tasks", &tasks); err == nil {
			for _, t := range tasks {
				if t.Enabled {
					a.Jobs = append(a.Jobs, Job{Name: t.Name, Schedule: coolifySchedule(t.Frequency), Command: t.Command})
				}
			}
		}
		p.Apps = append(p.Apps, a)
	}

	var dbs []map[string]any
	if err := api.get(ctx, "/databases", &dbs); err != nil {
		p.Warnings = append(p.Warnings, "databases: "+err.Error())
	}
	for _, d := range dbs {
		str := func(k string) string { s, _ := d[k].(string); return s }
		envID, _ := d["environment_id"].(float64)
		pl := places[int64(envID)]
		engine, note := engineOf(str("database_type") + " " + str("type") + " " + str("image"))
		if engine == "" {
			p.Warnings = append(p.Warnings, fmt.Sprintf("database %s (%s) has no PortDock engine", str("name"), str("image")))
			continue
		}
		db := Database{Ref: str("uuid"), Name: str("name"), Project: pl.project, Environment: pl.env, Engine: engine, Image: officialImage(engine, str("image"))}
		if note != "" {
			db.Notes = append(db.Notes, note)
		}
		db.Notes = append(db.Notes, "created empty: restore a dump of the Coolify database into it")
		p.Databases = append(p.Databases, db)
	}

	var svcs []struct {
		UUID          string `json:"uuid"`
		Name          string `json:"name"`
		ServiceType   string `json:"service_type"`
		ComposeRaw    string `json:"docker_compose_raw"`
		EnvironmentID int64  `json:"environment_id"`
	}
	if err := api.get(ctx, "/services", &svcs); err != nil {
		p.Warnings = append(p.Warnings, "services: "+err.Error())
	}
	for _, cs := range svcs {
		pl := places[cs.EnvironmentID]
		s := Service{Ref: cs.UUID, Name: cs.Name, Project: pl.project, Environment: pl.env}
		slug := resourceName(orDefault(cs.ServiceType, cs.Name))
		tpl, err := templates.ConvertCoolify(slug, cs.ComposeRaw)
		if err != nil {
			p.Warnings = append(p.Warnings, fmt.Sprintf("service %s: %v", cs.Name, err))
			continue
		}
		s.Compose, s.Notes = tpl.Compose, tpl.Notes
		var apps []struct {
			Name string `json:"name"`
			FQDN string `json:"fqdn"`
		}
		if err := api.get(ctx, "/services/"+cs.UUID+"/applications", &apps); err == nil {
			for _, sa := range apps {
				if sa.Name == tpl.Service && sa.FQDN != "" {
					s.Domain = strings.TrimSpace(strings.Split(sa.FQDN, ",")[0])
				}
			}
		}
		var envs []coolifyEnvVar
		if err := api.get(ctx, "/services/"+cs.UUID+"/envs", &envs); err == nil {
			// Generated values (SERVICE_*) are generated again; the others were set by hand.
			for _, v := range coolifyVars(envs) {
				if !strings.HasPrefix(v.Key, "SERVICE_") {
					s.Env = append(s.Env, v)
				}
			}
		}
		s.Notes = append(s.Notes, "passwords are generated again and the data is not copied")
		p.Services = append(p.Services, s)
	}
	return p, nil
}

// coolifyRepo returns the clone URL of a Coolify application.
func coolifyRepo(ca coolifyApp) string {
	if strings.Contains(ca.GitFullURL, "://") || strings.HasPrefix(ca.GitFullURL, "git@") {
		return ca.GitFullURL
	}
	r := strings.TrimSpace(ca.GitRepository)
	switch {
	case r == "":
		return ""
	case strings.Contains(r, "://") || strings.HasPrefix(r, "git@"):
		return r
	}
	// owner/repo of a GitHub source.
	return "https://github.com/" + strings.TrimSuffix(r, ".git")
}

// coolifySchedule maps Coolify's named frequencies to cron.
func coolifySchedule(f string) string {
	switch strings.TrimSpace(f) {
	case "every_minute":
		return "* * * * *"
	case "hourly":
		return "0 * * * *"
	case "daily":
		return "0 0 * * *"
	case "weekly":
		return "0 0 * * 0"
	case "monthly":
		return "0 0 1 * *"
	case "yearly":
		return "0 0 1 1 *"
	}
	return f
}

// isSecretKey tells the variables to hide in PortDock's interface.
func isSecretKey(k string) bool {
	k = strings.ToUpper(k)
	for _, w := range []string{"PASSWORD", "SECRET", "TOKEN", "PRIVATE", "_KEY", "CREDENTIAL", "DATABASE_URL", "REDIS_URL"} {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

func coolifyVars(envs []coolifyEnvVar) []Var {
	seen := map[string]int{}
	var out []Var
	for _, e := range envs {
		v := e.RealValue
		if v == "" {
			v = e.Value
		}
		item := Var{Key: e.Key, Value: v, Build: e.Build, Preview: e.Preview, Secret: e.ShownOnce || isSecretKey(e.Key)}
		key := e.Key + map[bool]string{true: "\x00p"}[e.Preview]
		if i, ok := seen[key]; ok {
			out[i] = item
			continue
		}
		seen[key] = len(out)
		out = append(out, item)
	}
	return out
}
