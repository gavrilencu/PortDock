package api

import (
	"context"
	"fmt"
	"github.com/portdock/portdock/internal/auth"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/portdock/portdock/internal/deploy"
	"github.com/portdock/portdock/internal/proxy"
	"github.com/portdock/portdock/internal/registry"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
	"regexp"
)

var buildPacks = []string{"auto", "dockerfile", "compose", "node", "bun", "python", "go", "static", "shell", "nixpacks", "image"}

type appDTO struct {
	store.Application
	Webhooks    map[string]string `json:"webhooks"`
	Project     store.Project     `json:"project"`
	Environment store.Environment `json:"environment"`
	ServerName  string            `json:"server_name"`
	ServerHost  string            `json:"server_host"` // address for published host ports
	// Servers lists every server the application runs on (primary first).
	Servers      []serverRef `json:"servers"`
	RegistryName string      `json:"registry_name"`
	RegistryRepo string      `json:"registry_repository"`
	Access       Access      `json:"access"`
}

type serverRef struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Host   string `json:"host"`
	Status string `json:"status"`
}

func (s *Server) appOut(ctx context.Context, a store.Application) appDTO {
	a.HasGitToken = a.GitToken != ""
	base := s.publicURL(ctx) + "/api/webhooks/"
	dto := appDTO{Application: a, Webhooks: map[string]string{
		"github": base + "github/" + a.UUID,
		"gitlab": base + "gitlab/" + a.UUID,
		"gitea":  base + "gitea/" + a.UUID,
		"azure":  base + "azure/" + a.UUID,
		"deploy": base + "deploy/" + a.UUID + "?token=" + a.WebhookSecret,
	}}
	dto.Environment, _ = store.ByID[store.Environment](ctx, s.db, "environments", a.EnvironmentID)
	dto.Project, _ = store.ByID[store.Project](ctx, s.db, "projects", dto.Environment.ProjectID)
	srv, _ := store.ByID[store.Server](ctx, s.db, "servers", a.ServerID)
	dto.ServerName = srv.Name
	dto.ServerHost = srv.Host
	if srv.IsLocal {
		dto.ServerHost = "localhost"
	}
	for _, id := range s.engine.AppServerIDs(ctx, a) {
		if x, err := store.ByID[store.Server](ctx, s.db, "servers", id); err == nil {
			dto.Servers = append(dto.Servers, serverRef{ID: x.ID, Name: x.Name, Host: x.Host, Status: x.Status})
		}
	}
	dto.Access = s.resourceAccess(ctx, a.EnvironmentID)
	if reg, err := s.engine.Registry(ctx, a.RegistryID); err == nil && reg != nil {
		dto.RegistryName = reg.Name
		dto.RegistryRepo = registry.Repository(*reg, a.RegistryImage, a.Name)
	}
	return dto
}

func (s *Server) loadApp(r *http.Request) (store.Application, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.Application{}, err
	}
	return store.ByID[store.Application](r.Context(), s.db, "applications", id)
}

func (s *Server) validateApp(ctx context.Context, a *store.Application) error {
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" {
		return badRequest("name is required")
	}
	if !slices.Contains(buildPacks, a.BuildPack) {
		return badRequest("build_pack must be one of %s", strings.Join(buildPacks, ", "))
	}
	if a.BuildPack == "image" {
		a.SourceType = "image"
		a.GitHubAppID = 0
		a.AzureConnectionID = 0
	}
	if a.AzureConnectionID != 0 {
		a.GitHubAppID = 0
	}
	if err := s.validAzureSource(ctx, a); err != nil {
		return err
	}
	if err := s.ensureGitHubSource(ctx, a); err != nil {
		return err
	}
	if a.SourceType == "image" {
		a.BuildPack = "image"
		if strings.TrimSpace(a.DockerImage) == "" {
			return badRequest("docker_image is required")
		}
	} else {
		a.SourceType = "git"
		if strings.TrimSpace(a.GitRepository) == "" {
			return badRequest("git_repository is required")
		}
		if a.GitBranch == "" {
			a.GitBranch = "main"
		}
	}
	if a.Port < 0 || a.Port > 65535 {
		return badRequest("invalid port")
	}
	if !slices.Contains([]string{"", "critical", "high"}, a.ScanBlock) {
		return badRequest("scan_block must be empty, critical or high")
	}
	if a.Replicas < 1 {
		a.Replicas = 1
	}
	pm, err := store.NormalizePortMappings(a.PortsMapping)
	if err != nil {
		return badRequest("%v", err)
	}
	a.PortsMapping = pm
	if a.Replicas > 20 {
		return badRequest("replicas must be between 1 and 20")
	}
	if a.HealthCheckTimeout <= 0 {
		a.HealthCheckTimeout = 60
	}
	if a.BaseDirectory == "" {
		a.BaseDirectory = "/"
	}
	if a.DockerfilePath == "" {
		a.DockerfilePath = "Dockerfile"
	}
	if a.ComposePath == "" {
		a.ComposePath = "docker-compose.yml"
	}
	var clean []string
	for _, d := range store.SplitList(a.Domains) {
		host, _ := proxy.NormalizeDomain(d)
		if host == "" || strings.ContainsAny(host, " `'\"") {
			return badRequest("invalid domain %q", d)
		}
		clean = append(clean, d)
	}
	a.Domains = strings.Join(clean, ",")
	if _, err := store.ByID[store.Server](ctx, s.db, "servers", a.ServerID); err != nil {
		return badRequest("server not found")
	}
	if a.BuildServerID == a.ServerID {
		a.BuildServerID = 0
	}
	if a.BuildServerID != 0 {
		if _, err := store.ByID[store.Server](ctx, s.db, "servers", a.BuildServerID); err != nil {
			return badRequest("build server not found")
		}
	}
	if a.RegistryID != 0 {
		if _, err := store.ByID[store.Registry](ctx, s.db, "registries", a.RegistryID); err != nil {
			return badRequest("registry not found")
		}
	}
	a.RegistryImage = strings.Trim(strings.ToLower(strings.TrimSpace(a.RegistryImage)), "/")
	if a.RegistryImage != "" && !registryImageRe.MatchString(a.RegistryImage) {
		return badRequest("image name may contain lowercase letters, digits, '.', '_', '-' and '/' (e.g. team/api)")
	}
	if _, err := store.ByID[store.Environment](ctx, s.db, "environments", a.EnvironmentID); err != nil {
		return badRequest("environment not found")
	}
	return nil
}

type appInput struct {
	*store.Application
	GitToken *string `json:"git_token"`
	// ServerIDs are the additional servers to deploy to (besides server_id).
	ServerIDs *[]int64 `json:"server_ids"`
	// DomainRoutes adds domains that target a service of a compose application
	// (e.g. the frontend and the backend of a monorepo), on top of Domains.
	DomainRoutes []domainRouteInput `json:"domain_routes"`
}

type domainRouteInput struct {
	Domain  string `json:"domain"`
	Service string `json:"service"`
	Port    int    `json:"port"`
}

var registryImageRe = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*$`)

// applyAppServers validates and stores the additional servers, then undeploys
// the application from servers that were removed.
func (s *Server) applyAppServers(r *http.Request, a store.Application, ids *[]int64) error {
	if ids == nil {
		return nil
	}
	ctx := r.Context()
	for _, id := range *ids {
		if _, err := store.ByID[store.Server](ctx, s.db, "servers", id); err != nil {
			return badRequest("server #%d not found", id)
		}
	}
	before := s.engine.AppServerIDs(ctx, a)
	if err := s.engine.SetAppServers(ctx, a, *ids); err != nil {
		return err
	}
	after := s.engine.AppServerIDs(ctx, a)
	for _, id := range before {
		if !slices.Contains(after, id) {
			if err := s.engine.RemoveAppFromServer(ctx, a, id); err != nil {
				s.activity(r, "application.undeploy_failed", fmt.Sprintf("%s from server #%d: %v", a.Name, id, err))
			}
		}
	}
	if len(after) != len(before) {
		s.activity(r, "application.servers", fmt.Sprintf("%s: %d server(s)", a.Name, len(after)))
	}
	return nil
}

func (s *Server) listApplications(w http.ResponseWriter, r *http.Request) error {
	apps, err := store.Select[store.Application](r.Context(), s.db, "SELECT * FROM applications ORDER BY name")
	if err != nil {
		return err
	}
	visible := s.envVisibility(r.Context())
	apps = filterBy(apps, func(a store.Application) bool { return visible(a.EnvironmentID) })
	for i := range apps {
		apps[i].HasGitToken = apps[i].GitToken != ""
	}
	return ok(w, apps)
}

func (s *Server) createApplication(w http.ResponseWriter, r *http.Request) error {
	a := store.Application{
		SourceType: "git", GitBranch: "main", BuildPack: "auto", BaseDirectory: "/", DockerfilePath: "Dockerfile",
		ComposePath: "docker-compose.yml", PublishDirectory: "dist", Port: 3000, HTTPS: true, AutoDeploy: true,
		HealthCheckTimeout: 60, Replicas: 1,
	}
	in := appInput{Application: &a}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	a.ID, a.Status, a.CurrentImage = 0, "stopped", ""
	a.UUID = secrets.RandomToken(8)
	a.WebhookSecret = secrets.RandomToken(20)
	a.CreatedAt, a.UpdatedAt = store.Now(), store.Now()
	a.AzureHookID, a.AzureHookError = "", ""
	if in.GitToken != nil {
		a.GitToken = s.box.Encrypt(*in.GitToken)
	}
	if err := s.validateApp(r.Context(), &a); err != nil {
		return err
	}
	if err := s.requireEnv(r, a.EnvironmentID, permWrite); err != nil {
		return err
	}
	// Suggest a domain from the server's wildcard when none was given.
	if a.Domains == "" && a.BuildPack != "compose" {
		if srv, err := store.ByID[store.Server](r.Context(), s.db, "servers", a.ServerID); err == nil && srv.WildcardDomain != "" {
			a.Domains = proxy.SafeName(a.Name) + "." + strings.TrimPrefix(srv.WildcardDomain, "*.")
		}
	}
	var rows []store.AppDomain
	routes := make([]domainRouteInput, 0, len(in.DomainRoutes))
	for _, raw := range store.SplitList(a.Domains) {
		routes = append(routes, domainRouteInput{Domain: raw})
	}
	for _, dr := range in.DomainRoutes {
		for _, raw := range store.SplitList(dr.Domain) {
			routes = append(routes, domainRouteInput{Domain: raw, Service: dr.Service, Port: dr.Port})
		}
	}
	if len(in.DomainRoutes) > 0 && a.BuildPack != "compose" {
		return badRequest("domains per service are only available for docker compose applications")
	}
	seen := map[string]bool{}
	for _, dr := range routes {
		host, path, schemeHTTPS, err := parseDomain(dr.Domain)
		if err != nil {
			return err
		}
		if seen[host+path] {
			return badRequest("%s%s is listed twice", host, path)
		}
		seen[host+path] = true
		if err := s.domainConflict(r.Context(), host, path, 0); err != nil {
			return err
		}
		d := store.AppDomain{Host: host, Path: path, HTTPS: a.HTTPS, WWWRedirect: "none", Indexing: true, DNSStatus: "unknown", CertMode: "auto", CreatedAt: store.Now()}
		if schemeHTTPS != nil {
			d.HTTPS = *schemeHTTPS
		}
		svc, port := dr.Service, dr.Port
		if err := applyDomainInput(&d, domainInput{Service: &svc, Port: &port}); err != nil {
			return err
		}
		rows = append(rows, d)
	}
	if _, err := store.Insert(r.Context(), s.db, "applications", &a); err != nil {
		return err
	}
	for i := range rows {
		rows[i].ApplicationID = a.ID
		if _, err := store.Insert(r.Context(), s.db, "app_domains", &rows[i]); err != nil {
			return err
		}
	}
	_ = s.db.SyncDomainsColumn(r.Context(), a.ID)
	a.Domains = ""
	if ds, _ := s.db.Domains(r.Context(), a.ID); len(ds) > 0 {
		a, _ = store.ByID[store.Application](r.Context(), s.db, "applications", a.ID)
	}
	if err := s.applyAppServers(r, a, in.ServerIDs); err != nil {
		return err
	}
	if a.AzureConnectionID != 0 {
		_ = s.ensureAzureHook(r.Context(), a) // the outcome is shown on the CI/CD tab
		a, _ = store.ByID[store.Application](r.Context(), s.db, "applications", a.ID)
	}
	s.activity(r, "application.create", a.Name)
	return ok(w, s.appOut(r.Context(), a))
}

func (s *Server) getApplication(w http.ResponseWriter, r *http.Request) error {
	a, err := s.loadApp(r)
	if err != nil {
		return err
	}
	return ok(w, s.appOut(r.Context(), a))
}

func (s *Server) updateApplication(w http.ResponseWriter, r *http.Request) error {
	a, err := s.loadApp(r)
	if err != nil {
		return err
	}
	orig := a
	in := appInput{Application: &a}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	// Immutable / system-managed fields. Domains are managed via /domains.
	a.ID, a.UUID, a.CreatedAt, a.Status, a.CurrentImage, a.WebhookSecret, a.GitToken =
		orig.ID, orig.UUID, orig.CreatedAt, orig.Status, orig.CurrentImage, orig.WebhookSecret, orig.GitToken
	a.Domains, a.HTTPS = orig.Domains, orig.HTTPS
	a.AzureHookID, a.AzureHookError = orig.AzureHookID, orig.AzureHookError
	if in.GitToken != nil {
		a.GitToken = s.box.Encrypt(*in.GitToken)
	}
	if a.ServerID != orig.ServerID && orig.Status != "stopped" {
		return badRequest("stop the application before moving it to another server")
	}
	a.UpdatedAt = store.Now()
	if err := s.validateApp(r.Context(), &a); err != nil {
		return err
	}
	if a.EnvironmentID != orig.EnvironmentID {
		if err := s.requireEnv(r, a.EnvironmentID, permWrite); err != nil {
			return err
		}
	}
	if err := store.Update(r.Context(), s.db, "applications", &a); err != nil {
		return err
	}
	if err := s.applyAppServers(r, a, in.ServerIDs); err != nil {
		return err
	}
	// A new Azure DevOps repository needs its own service hook.
	if a.AzureConnectionID != orig.AzureConnectionID || a.AzureProjectID != orig.AzureProjectID || a.AzureRepoID != orig.AzureRepoID {
		s.removeAzureHook(r.Context(), orig)
		a.AzureHookID = ""
		if a.AzureConnectionID != 0 {
			_ = s.ensureAzureHook(r.Context(), a)
		} else {
			_, _ = s.db.ExecContext(r.Context(), "UPDATE applications SET azure_hook_id = '', azure_hook_error = '' WHERE id = ?", a.ID)
		}
		a, _ = store.ByID[store.Application](r.Context(), s.db, "applications", a.ID)
	}
	s.activity(r, "application.update", a.Name)
	s.hub.Event("application", a.ID, a.Status)
	return ok(w, s.appOut(r.Context(), a))
}

func (s *Server) deleteApplication(w http.ResponseWriter, r *http.Request) error {
	a, err := s.loadApp(r)
	if err != nil {
		return err
	}
	if err := s.engine.DeleteApp(r.Context(), a); err != nil {
		return badRequest("cleanup failed: %v", err)
	}
	if err := store.Delete(r.Context(), s.db, "applications", a.ID); err != nil {
		return err
	}
	s.removeAzureHook(r.Context(), a)
	_, _ = s.db.ExecContext(r.Context(), "DELETE FROM env_vars WHERE owner_type = 'application' AND owner_id = ?", a.ID)
	s.activity(r, "application.delete", a.Name)
	s.hub.Event("application", a.ID, "deleted")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) deployApplication(w http.ResponseWriter, r *http.Request) error {
	a, err := s.loadApp(r)
	if err != nil {
		return err
	}
	var in struct {
		ForceRebuild bool   `json:"force_rebuild"`
		CommitSHA    string `json:"commit_sha"`
		Branch       string `json:"branch"`
	}
	if r.ContentLength > 0 {
		if err := readJSON(r, &in); err != nil {
			return err
		}
	}
	trigger := "manual"
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		trigger = "api"
	}
	by, needs := s.deployGate(r.Context(), a)
	d, err := s.engine.Enqueue(r.Context(), a, deploy.Options{
		Trigger: trigger, ForceRebuild: in.ForceRebuild, CommitSHA: strings.TrimSpace(in.CommitSHA), Branch: strings.TrimSpace(in.Branch),
		RequestedBy: by, NeedsApproval: needs,
	})
	if err != nil {
		return err
	}
	if needs {
		s.activity(r, "deployment.request_approval", a.Name)
	} else {
		s.activity(r, "application.deploy", a.Name)
	}
	return ok(w, d)
}

func (s *Server) applicationAction(w http.ResponseWriter, r *http.Request) error {
	a, err := s.loadApp(r)
	if err != nil {
		return err
	}
	action := r.PathValue("action")
	if action != "start" && action != "stop" && action != "restart" {
		return errStatus(404, "unknown action")
	}
	if err := s.engine.AppAction(r.Context(), a, action); err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "application."+action, a.Name)
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) regenerateWebhookSecret(w http.ResponseWriter, r *http.Request) error {
	a, err := s.loadApp(r)
	if err != nil {
		return err
	}
	a.WebhookSecret = secrets.RandomToken(20)
	if _, err := s.db.ExecContext(r.Context(), "UPDATE applications SET webhook_secret = ? WHERE id = ?", a.WebhookSecret, a.ID); err != nil {
		return err
	}
	if a.AzureConnectionID != 0 {
		_ = s.ensureAzureHook(r.Context(), a) // the service hook sends the secret
		a, _ = store.ByID[store.Application](r.Context(), s.db, "applications", a.ID)
	}
	return ok(w, s.appOut(r.Context(), a))
}

const deploymentCols = "id, uuid, application_id, status, trigger, commit_sha, commit_message, branch, pr_number, image, rollback_of, force_rebuild, started_at, finished_at, created_at, requested_by, reviewed_by, reviewed_at, review_comment"

func (s *Server) listAppDeployments(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	limit := 50
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 500 {
		limit = l
	}
	ds, err := store.Select[store.Deployment](r.Context(), s.db,
		"SELECT "+deploymentCols+" FROM deployments WHERE application_id = ? ORDER BY id DESC LIMIT ?", id, limit)
	if err != nil {
		return err
	}
	return ok(w, ds)
}

func (s *Server) listDeployments(w http.ResponseWriter, r *http.Request) error {
	type row struct {
		store.Deployment
		ApplicationName string `db:"application_name" json:"application_name"`
		EnvironmentID   int64  `db:"environment_id" json:"environment_id"`
	}
	q := "SELECT d.id, d.uuid, d.application_id, d.status, d.trigger, d.commit_sha, d.commit_message, d.branch, d.pr_number, d.image, " +
		"d.rollback_of, d.started_at, d.finished_at, d.created_at, d.requested_by, a.name AS application_name, a.environment_id AS environment_id FROM deployments d JOIN applications a ON a.id = d.application_id"
	var args []any
	if st := r.URL.Query().Get("status"); st != "" {
		q += " WHERE d.status = ?"
		args = append(args, st)
	}
	q += " ORDER BY d.id DESC LIMIT 200"
	rows, err := store.Select[row](r.Context(), s.db, q, args...)
	if err != nil {
		return err
	}
	visible := s.envVisibility(r.Context())
	return ok(w, filterBy(rows, func(d row) bool { return visible(d.EnvironmentID) }))
}

func (s *Server) getDeployment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	d, err := store.ByID[store.Deployment](ctx, s.db, "deployments", id)
	if err != nil {
		return err
	}
	out := struct {
		store.Deployment
		RequestedByName string `json:"requested_by_name"`
		ReviewedByName  string `json:"reviewed_by_name"`
		CanReview       bool   `json:"can_review"`
		CanSelfApprove  bool   `json:"can_self_approve"`
	}{Deployment: d}
	_ = s.db.QueryRowContext(ctx, "SELECT name FROM users WHERE id = ?", d.RequestedBy).Scan(&out.RequestedByName)
	_ = s.db.QueryRowContext(ctx, "SELECT name FROM users WHERE id = ?", d.ReviewedBy).Scan(&out.ReviewedByName)
	if envID, err := s.envOf(ctx, "deployment", id); err == nil {
		_, _, err := s.authorizeEnv(ctx, envID, permManage)
		out.CanReview = err == nil && d.Status == "pending_approval"
	}
	u := auth.UserFrom(ctx)
	out.CanSelfApprove = d.RequestedBy == 0 || d.RequestedBy != u.ID || u.Role == "owner"
	return ok(w, out)
}

func (s *Server) deploymentStream(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	d, err := store.ByID[store.Deployment](r.Context(), s.db, "deployments", id)
	if err != nil {
		return err
	}
	done := d.Status != "queued" && d.Status != "running"
	return s.sseDeployment(w, r, d, done)
}

func (s *Server) cancelDeployment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.engine.Cancel(r.Context(), id); err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "deployment.cancel", strconv.FormatInt(id, 10))
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) rollbackDeployment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	d, err := store.ByID[store.Deployment](r.Context(), s.db, "deployments", id)
	if err != nil {
		return err
	}
	a, err := store.ByID[store.Application](r.Context(), s.db, "applications", d.ApplicationID)
	if err != nil {
		return err
	}
	by, needs := s.deployGate(r.Context(), a)
	o := deploy.Options{Trigger: "rollback", RollbackOf: d.ID, RequestedBy: by, NeedsApproval: needs}
	if v, err := store.ByID[store.AppVersion](r.Context(), s.db, "app_versions", d.VersionID); err == nil && v.Status == "available" {
		o.VersionID = v.ID // its images are kept even after the build tags are cleaned up
	}
	nd, err := s.engine.Enqueue(r.Context(), a, o)
	if err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "application.rollback", fmt.Sprintf("%s → #%d", a.Name, d.ID))
	return ok(w, nd)
}

func (s *Server) appContainers(w http.ResponseWriter, r *http.Request) error {
	a, err := s.loadApp(r)
	if err != nil {
		return err
	}
	cs, err := s.engine.AppContainers(r.Context(), a)
	if err != nil {
		return badRequest("%v", err)
	}
	return ok(w, cs)
}

// appLogs streams runtime logs of the application's (first running) container.
func (s *Server) appLogs(w http.ResponseWriter, r *http.Request) error {
	a, err := s.loadApp(r)
	if err != nil {
		return err
	}
	cs, err := s.engine.AppContainers(r.Context(), a)
	if err != nil {
		return badRequest("%v", err)
	}
	name := r.URL.Query().Get("container")
	var pick *dockerContainer
	for i, c := range cs {
		switch {
		case name != "" && (c.Name == name || c.ID == name):
			pick = &cs[i]
		case name == "" && pick == nil && c.State == "running" && (c.Labels["portdock.pr"] == "0" || c.Labels["portdock.pr"] == ""):
			pick = &cs[i]
		}
	}
	if pick == nil && name == "" && len(cs) > 0 {
		pick = &cs[0]
	}
	if pick == nil {
		if name != "" {
			// Only allow containers that belong to this application.
			return errStatus(403, "container does not belong to this application")
		}
		return badRequest("no containers; deploy the application first")
	}
	serverID := pick.ServerID
	if serverID == 0 {
		serverID = a.ServerID
	}
	x, _, err := s.pool.ForID(r.Context(), serverID)
	if err != nil {
		return badRequest("%v", err)
	}
	return s.sseContainerLogs(w, r, x, pick.Name)
}

type previewDTO struct {
	PRNumber   int              `json:"pr_number"`
	Branch     string           `json:"branch"`
	Domains    []string         `json:"domains"`
	Deployment store.Deployment `json:"last_deployment"`
}

func (s *Server) listPreviews(w http.ResponseWriter, r *http.Request) error {
	a, err := s.loadApp(r)
	if err != nil {
		return err
	}
	ds, err := store.Select[store.Deployment](r.Context(), s.db,
		"SELECT "+deploymentCols+" FROM deployments WHERE application_id = ? AND pr_number > 0 AND id IN (SELECT MAX(id) FROM deployments WHERE application_id = ? AND pr_number > 0 GROUP BY pr_number) ORDER BY pr_number DESC",
		a.ID, a.ID)
	if err != nil {
		return err
	}
	srv, _ := store.ByID[store.Server](r.Context(), s.db, "servers", a.ServerID)
	out := make([]previewDTO, 0, len(ds))
	for _, d := range ds {
		if d.Status == "cancelled" && d.Trigger == "preview-closed" {
			continue
		}
		out = append(out, previewDTO{PRNumber: d.PRNumber, Branch: d.Branch, Domains: s.engine.PreviewDomains(a, srv, d.PRNumber), Deployment: d})
	}
	return ok(w, out)
}

func (s *Server) deletePreview(w http.ResponseWriter, r *http.Request) error {
	a, err := s.loadApp(r)
	if err != nil {
		return err
	}
	pr, err := strconv.Atoi(r.PathValue("pr"))
	if err != nil {
		return badRequest("invalid pr")
	}
	if err := s.engine.CleanupPreview(r.Context(), a, pr); err != nil {
		return badRequest("%v", err)
	}
	_, _ = s.db.ExecContext(r.Context(), "DELETE FROM deployments WHERE application_id = ? AND pr_number = ?", a.ID, pr)
	return ok(w, map[string]bool{"ok": true})
}

// ---- environment variables ----

var ownerTypes = map[string]string{"applications": "application", "services": "service", "databases": "database"}

func (s *Server) envOwner(r *http.Request) (string, int64, error) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // api/{owner}/{id}/envs
	if len(parts) < 3 {
		return "", 0, badRequest("invalid path")
	}
	typ, ok := ownerTypes[parts[1]]
	if !ok {
		return "", 0, errStatus(404, "unknown resource type")
	}
	id, err := pathID(r, "id")
	if err != nil {
		return "", 0, err
	}
	var n int
	_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM "+parts[1]+" WHERE id = ?", id).Scan(&n)
	if n == 0 {
		return "", 0, store.ErrNotFound
	}
	return typ, id, nil
}

func (s *Server) envOut(vs []store.EnvVar) []store.EnvVar {
	for i := range vs {
		vs[i].Value = s.box.MustDecrypt(vs[i].Value)
	}
	return vs
}

func validEnvKey(k string) bool {
	if k == "" {
		return false
	}
	for i, c := range k {
		if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// validEnvService checks the compose service a variable is scoped to.
func (s *Server) validEnvService(ctx context.Context, typ string, id int64, service string) error {
	if service == "" {
		return nil
	}
	if !composeServiceRe.MatchString(service) {
		return badRequest("invalid compose service name")
	}
	var pack string
	if typ == "application" {
		_ = s.db.QueryRowContext(ctx, "SELECT build_pack FROM applications WHERE id = ?", id).Scan(&pack)
	}
	if pack != "compose" {
		return badRequest("variables per service are only available for docker compose applications")
	}
	return nil
}

// applyEnv redeploys the running version with the current variables, without
// building: the same image, or the same compose checkout.
func (s *Server) applyEnv(w http.ResponseWriter, r *http.Request) error {
	a, err := s.loadApp(r)
	if err != nil {
		return err
	}
	last, err := store.Get[store.Deployment](r.Context(), s.db,
		"SELECT * FROM deployments WHERE application_id = ? AND pr_number = 0 AND status = 'success' AND image != '' ORDER BY id DESC LIMIT 1", a.ID)
	if err != nil {
		return badRequest("the application has no successful deployment yet; deploy it first")
	}
	by, needs := s.deployGate(r.Context(), a)
	// The live version (when versions exist) keeps running, only with the new variables.
	d, err := s.engine.Enqueue(r.Context(), a, deploy.Options{Trigger: deploy.TriggerConfig, RollbackOf: last.ID, VersionID: s.engine.LiveVersionID(r.Context(), a.ID), RequestedBy: by, NeedsApproval: needs})
	if err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "application.apply_env", a.Name)
	return ok(w, d)
}

func (s *Server) listEnvs(w http.ResponseWriter, r *http.Request) error {
	typ, id, err := s.envOwner(r)
	if err != nil {
		return err
	}
	vs, err := store.Select[store.EnvVar](r.Context(), s.db, "SELECT * FROM env_vars WHERE owner_type = ? AND owner_id = ? ORDER BY is_preview, key", typ, id)
	if err != nil {
		return err
	}
	return ok(w, s.envOut(vs))
}

func (s *Server) createEnv(w http.ResponseWriter, r *http.Request) error {
	typ, id, err := s.envOwner(r)
	if err != nil {
		return err
	}
	var v store.EnvVar
	if err := readJSON(r, &v); err != nil {
		return err
	}
	v.Key = strings.TrimSpace(v.Key)
	if !validEnvKey(v.Key) {
		return badRequest("invalid variable name %q", v.Key)
	}
	if err := s.validEnvService(r.Context(), typ, id, v.Service); err != nil {
		return err
	}
	v.ID, v.OwnerType, v.OwnerID, v.CreatedAt = 0, typ, id, store.Now()
	plain := v.Value
	v.Value = s.box.Encrypt(plain)
	if _, err := store.Insert(r.Context(), s.db, "env_vars", &v); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return badRequest("variable %s already exists", v.Key)
		}
		return err
	}
	v.Value = plain
	return ok(w, v)
}

// bulkEnvs replaces all variables of one scope (regular or preview) from .env content.
func (s *Server) bulkEnvs(w http.ResponseWriter, r *http.Request) error {
	typ, id, err := s.envOwner(r)
	if err != nil {
		return err
	}
	var in struct {
		Content   string `json:"content"`
		IsPreview bool   `json:"is_preview"`
		Service   string `json:"service"` // compose service whose variables are replaced ("" = the application's)
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.validEnvService(r.Context(), typ, id, in.Service); err != nil {
		return err
	}
	existing, err := store.Select[store.EnvVar](r.Context(), s.db, "SELECT * FROM env_vars WHERE owner_type = ? AND owner_id = ? AND is_preview = ? AND service = ?", typ, id, in.IsPreview, in.Service)
	if err != nil {
		return err
	}
	prev := map[string]store.EnvVar{}
	for _, v := range existing {
		prev[v.Key] = v
	}
	parsed, err := parseDotEnv(in.Content)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), "DELETE FROM env_vars WHERE owner_type = ? AND owner_id = ? AND is_preview = ? AND service = ?", typ, id, in.IsPreview, in.Service); err != nil {
		return err
	}
	for _, kv := range parsed {
		old := prev[kv[0]]
		if _, err := tx.ExecContext(r.Context(),
			"INSERT INTO env_vars(owner_type, owner_id, service, key, value, is_build, is_preview, is_secret, created_at) VALUES(?,?,?,?,?,?,?,?,?)",
			typ, id, in.Service, kv[0], s.box.Encrypt(kv[1]), old.IsBuild, in.IsPreview, old.IsSecret, store.Now()); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.listEnvs(w, r)
}

func parseDotEnv(content string) ([][2]string, error) {
	var out [][2]string
	seen := map[string]int{}
	for i, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, found := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !found || !validEnvKey(k) {
			return nil, badRequest("line %d: expected KEY=value", i+1)
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			if v[0] == '"' {
				v = strings.ReplaceAll(v[1:len(v)-1], `\n`, "\n")
			} else {
				v = v[1 : len(v)-1]
			}
		}
		if idx, ok := seen[k]; ok {
			out[idx][1] = v
			continue
		}
		seen[k] = len(out)
		out = append(out, [2]string{k, v})
	}
	return out, nil
}

func (s *Server) updateEnv(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	v, err := store.ByID[store.EnvVar](r.Context(), s.db, "env_vars", id)
	if err != nil {
		return err
	}
	var in struct {
		Key      *string `json:"key"`
		Value    *string `json:"value"`
		IsBuild  *bool   `json:"is_build"`
		IsSecret *bool   `json:"is_secret"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.Key != nil {
		if !validEnvKey(*in.Key) {
			return badRequest("invalid variable name")
		}
		v.Key = *in.Key
	}
	if in.Value != nil {
		v.Value = s.box.Encrypt(*in.Value)
	}
	if in.IsBuild != nil {
		v.IsBuild = *in.IsBuild
	}
	if in.IsSecret != nil {
		v.IsSecret = *in.IsSecret
	}
	if err := store.Update(r.Context(), s.db, "env_vars", &v); err != nil {
		return err
	}
	v.Value = s.box.MustDecrypt(v.Value)
	return ok(w, v)
}

func (s *Server) deleteEnv(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := store.Delete(r.Context(), s.db, "env_vars", id); err != nil {
		return err
	}
	return ok(w, map[string]bool{"ok": true})
}
