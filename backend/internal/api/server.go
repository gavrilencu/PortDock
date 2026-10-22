// Package api exposes PortDock's REST API, SSE streams, WebSocket terminals,
// git webhooks and serves the embedded web UI.
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/portdock/portdock/internal/assistant"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/audit"
	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/config"
	"github.com/portdock/portdock/internal/deploy"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/github"
	"github.com/portdock/portdock/internal/instancebackup"
	"github.com/portdock/portdock/internal/logdrain"
	"github.com/portdock/portdock/internal/overlay"
	"github.com/portdock/portdock/internal/monitor"
	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/scheduler"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
	"github.com/portdock/portdock/internal/stream"
	"github.com/portdock/portdock/internal/templates"
	"github.com/portdock/portdock/internal/uptime"
)

type Server struct {
	cfg     config.Config
	db      *store.DB
	box     *secrets.Box
	auth    *auth.Auth
	pool    *executor.Pool
	hub     *stream.Hub
	engine  *deploy.Engine
	sched   *scheduler.Scheduler
	notify  *notify.Notifier
	github  *github.Client
	ui      fs.FS
	cache   *ttlCache // short-lived results of per-server docker calls
	sec     *security
	updates *updateCache
	backups *instancebackup.Service
	restart func()
	routes  []routeInfo
	self    http.Handler // the full handler, for internal calls (gitops apply)
	monitor *monitor.Monitor
	audit   *audit.Streamer
	ai      *assistant.Client
	uptime  *uptime.Checker
	drains  *logdrain.Manager
	overlay *overlay.Manager
	// chatActs holds the actions the assistant proposed, until the user
	// confirms or cancels them in the chat.
	chatActs *chatActions
}

type Deps struct {
	Config    config.Config
	DB        *store.DB
	Box       *secrets.Box
	Auth      *auth.Auth
	Pool      *executor.Pool
	Hub       *stream.Hub
	Engine    *deploy.Engine
	Scheduler *scheduler.Scheduler
	Notifier  *notify.Notifier
	GitHub    *github.Client
	Monitor   *monitor.Monitor
	Audit     *audit.Streamer
	Uptime    *uptime.Checker
	UI        fs.FS
	// InstanceBackups backs up and restores PortDock itself.
	InstanceBackups *instancebackup.Service
	// Restart stops PortDock so that Docker starts it again (after a restore).
	Restart func()
	// LogDrains keeps the log agents of the servers.
	LogDrains *logdrain.Manager
	// Overlay keeps the private network between the servers.
	Overlay *overlay.Manager
}

func New(d Deps) *Server {
	if d.Audit == nil {
		d.Audit = audit.New(d.DB, d.Box)
	}
	if d.Uptime == nil && d.Engine != nil {
		d.Uptime = uptime.New(d.DB, d.Notifier, d.Engine.PublicURL)
	}
	if d.InstanceBackups == nil {
		d.InstanceBackups = instancebackup.New(d.Config.DataDir, d.Config.SecretKey, Version, d.DB, d.Box, d.Engine, d.Notifier)
	}
	if d.Restart == nil {
		d.Restart = func() {}
	}
	if d.LogDrains == nil {
		d.LogDrains = logdrain.New(d.DB, d.Box, d.Pool, d.Config.ProxyNetwork, d.Config.DataDir)
	}
	if d.Overlay == nil {
		d.Overlay = overlay.New(d.DB, d.Box, d.Pool, d.Config.DataDir, Version)
	}
	s := &Server{cfg: d.Config, db: d.DB, box: d.Box, auth: d.Auth, pool: d.Pool, hub: d.Hub,
		engine: d.Engine, sched: d.Scheduler, notify: d.Notifier, github: d.GitHub, ui: d.UI, sec: newSecurity(), cache: newTTLCache(), monitor: d.Monitor, audit: d.Audit, updates: &updateCache{}, ai: assistant.New(), uptime: d.Uptime,
		backups: d.InstanceBackups, restart: d.Restart, drains: d.LogDrains, overlay: d.Overlay, chatActs: newChatActions()}
	s.ai.Referer = func() string { return s.engine.PublicURL(context.Background()) }
	// An OpenRouter-compatible gateway (or a fake one in tests) can replace openrouter.ai.
	if u := strings.TrimRight(config.Env("OPENROUTER_URL", ""), "/"); u != "" {
		assistant.BaseURL = u
	}
	return s
}

// handlerFunc returns an error that is rendered as JSON.
type handlerFunc func(w http.ResponseWriter, r *http.Request) error

type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string { return e.Message }

func errStatus(status int, format string, args ...any) error {
	return &apiError{Status: status, Message: fmt.Sprintf(format, args...)}
}

func badRequest(format string, args ...any) error { return errStatus(400, format, args...) }

func (s *Server) wrap(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := h(w, r)
		if err == nil {
			return
		}
		var ae *apiError
		switch {
		case errors.As(err, &ae):
			writeJSON(w, ae.Status, map[string]string{"error": ae.Message})
		case errors.Is(err, store.ErrNotFound):
			writeJSON(w, 404, map[string]string{"error": "not found"})
		default:
			slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
			writeJSON(w, 500, map[string]string{"error": err.Error()})
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, v any) error {
	writeJSON(w, 200, v)
	return nil
}

func readJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 8<<20)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return badRequest("invalid JSON body: %v", err)
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		return 0, badRequest("invalid %s", name)
	}
	return id, nil
}

// ---- middleware ----

type role int

const (
	anyUser role = iota
	adminOnly
)

func (s *Server) authed(rl role, h handlerFunc) http.HandlerFunc {
	return s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		u, sess, err := s.auth.AuthenticateSession(r)
		if errors.Is(err, auth.ErrTokenExpired) {
			return errStatus(401, "this API token has expired")
		}
		if err != nil {
			return errStatus(401, "authentication required")
		}
		ctx := r.Context()
		if t := sess.Token; t != nil {
			if err := tokenRouteAllowed(r, *t); err != nil {
				return err
			}
			// A limited token is no administrator: the organization routes
			// and every administrator shortcut stay closed to it. Inside its
			// projects, projectRole still uses the real role.
			if t.Limited() && u.IsAdmin() {
				ctx = auth.WithRealRole(ctx, u.Role)
				u.Role = "member"
			}
		}
		// Users the policy requires to enroll in 2FA may only reach /api/auth/*.
		if !strings.HasPrefix(r.URL.Path, "/api/auth/") && s.needsMFASetup(r.Context(), u, sess) {
			return errStatus(403, "two-factor authentication must be enabled for your account")
		}
		if rl == adminOnly && !u.IsAdmin() {
			return errStatus(403, "admin role required")
		}
		return h(w, r.WithContext(auth.WithSession(auth.WithUser(ctx, u), sess)))
	})
}

func (s *Server) activity(r *http.Request, action, target string) {
	s.db.LogActivity(r.Context(), auth.UserFrom(r.Context()).ID, action, target)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		defer func() {
			if p := recover(); p != nil {
				slog.Error("panic", "err", p, "stack", string(debug.Stack()))
				http.Error(rec, "internal error", 500)
			}
			if strings.HasPrefix(r.URL.Path, "/api/") && !strings.Contains(r.URL.Path, "/stream") && !strings.HasSuffix(r.URL.Path, "/logs") {
				slog.Debug("http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "dur", time.Since(start))
			}
		}()
		r = r.WithContext(store.WithClientIP(r.Context(), auth.ClientIP(r)))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if s.cfg.DevMode {
			if origin := r.Header.Get("Origin"); origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(204)
				return
			}
		} else if s.crossSite(r) {
			writeJSON(rec, 403, map[string]string{"error": "cross-site request refused"})
			return
		}
		next.ServeHTTP(rec, r)
	})
}

// crossSite reports a state-changing API request that a browser sent from
// another site (CSRF). The session cookie is SameSite=Lax, which still lets
// sites on sibling subdomains, such as the applications PortDock hosts, send
// it. Webhooks (server to server) and token-authenticated calls are exempt.
func (s *Server) crossSite(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/api/webhooks/") || strings.HasPrefix(r.URL.Path, "/api/forward-auth/") ||
		strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return false
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return false
	case "same-site", "cross-site":
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false // not a browser, or one that sends neither header
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" { // "null": sandboxed frames, file:// pages
		return true
	}
	if strings.EqualFold(u.Host, r.Host) {
		return false
	}
	iu, err := url.Parse(s.db.Setting(r.Context(), "instance_url"))
	return err != nil || !strings.EqualFold(u.Host, iu.Host)
}

// handlerChain wraps the mux with the request middleware and JSON compression.
func (s *Server) handlerChain(mux http.Handler) http.Handler {
	return s.middleware(gzipMiddleware(mux))
}

// Handler builds the full HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Every route is recorded with its access rule: the OpenAPI document
	// (GET /api/openapi.json) is generated from this list.
	s.routes = nil
	record := func(pattern, access string, p perm) {
		s.routes = append(s.routes, routeInfo{Pattern: pattern, Access: access, Perm: p})
	}
	a := func(pattern string, h handlerFunc) {
		record(pattern, "user", 0)
		mux.HandleFunc(pattern, s.authed(anyUser, tokenWrite(h)))
	}
	admin := func(pattern string, h handlerFunc) {
		record(pattern, "admin", 0)
		mux.HandleFunc(pattern, s.authed(adminOnly, h))
	}
	public := func(pattern string, h handlerFunc) {
		record(pattern, "public", 0)
		mux.HandleFunc(pattern, s.wrap(h))
	}
	// g guards a resource route and gp a project route by the project role;
	// their permission also caps API tokens. Other routes that change
	// something need a token with the write scope (tokenWrite).
	guarded := func(pattern string, h handlerFunc) { mux.HandleFunc(pattern, s.authed(anyUser, h)) }
	g := func(pattern, kind string, p perm, h handlerFunc) {
		record(pattern, "project", p)
		guarded(pattern, s.guard(kind, p, h))
	}
	gp := func(pattern string, p perm, h handlerFunc) {
		record(pattern, "project", p)
		guarded(pattern, s.guardProject(p, h))
	}

	public("GET /api/openapi.json", s.openAPI)
	public("GET /api/health", func(w http.ResponseWriter, r *http.Request) error {
		return ok(w, map[string]string{"status": "ok", "version": Version})
	})

	// auth
	public("GET /api/auth/status", s.authStatus)
	public("POST /api/auth/register", s.register)
	public("POST /api/auth/login", s.login)
	public("POST /api/auth/logout", s.logout)
	public("POST /api/auth/login/mfa", s.loginMFA)
	public("GET /api/auth/providers", s.listLoginProviders)
	public("GET /api/auth/sso/{slug}/start", s.ssoStart)
	public("GET /api/auth/sso/{slug}/callback", s.ssoCallback)
	a("GET /api/auth/me", s.me)
	a("PATCH /api/auth/me", s.updateMe)
	a("GET /api/auth/2fa", s.twoFactorStatus)
	a("POST /api/auth/2fa/setup", s.twoFactorSetup)
	a("POST /api/auth/2fa/enable", s.twoFactorEnable)
	a("POST /api/auth/2fa/disable", s.twoFactorDisable)
	a("POST /api/auth/2fa/recovery-codes", s.regenerateRecoveryCodes)
	a("GET /api/auth/identities", s.listIdentities)
	a("DELETE /api/auth/identities/{id}", s.deleteIdentity)
	a("GET /api/auth/sessions", s.listSessions)
	a("DELETE /api/auth/sessions/{sid}", s.revokeSession)
	a("POST /api/auth/sessions/revoke-others", s.revokeOtherSessions)

	// dashboard & events
	a("GET /api/dashboard", s.dashboard)
	a("GET /api/events", s.events)
	a("GET /api/activity", s.listActivity)
	admin("GET /api/activity/export", s.exportActivity)
	a("GET /api/certificates", s.listCertificates)
	admin("POST /api/certificates/check", s.checkCertificatesNow)
	g("POST /api/domains/{id}/renew", "domain", permWrite, s.renewDomainCertificate)
	admin("POST /api/custom-certificates", s.uploadCustomCertificate)
	admin("DELETE /api/custom-certificates/{id}", s.deleteCustomCertificate)
	admin("GET /api/settings/certificates", s.getCertSettings)
	admin("PUT /api/settings/certificates", s.updateCertSettings)
	a("GET /api/system/update", s.getUpdate)
	admin("POST /api/system/update/check", s.checkUpdateNow)
	admin("PUT /api/system/update/settings", s.updateUpdateSettings)
	admin("POST /api/system/update", s.runUpdate)
	admin("GET /api/compliance/report", s.complianceReport)
	// external secret stores (HashiCorp Vault, Azure Key Vault)
	admin("GET /api/secret-stores", s.listSecretStores)
	admin("POST /api/secret-stores", s.createSecretStore)
	admin("POST /api/secret-stores/test", s.testSecretStore)
	admin("PATCH /api/secret-stores/{id}", s.updateSecretStore)
	admin("DELETE /api/secret-stores/{id}", s.deleteSecretStore)
	a("GET /api/secret-stores/names", s.secretStoreNames)
	// the private network between servers (WireGuard)
	admin("GET /api/overlay", s.getOverlay)
	admin("PUT /api/overlay", s.putOverlay)
	admin("GET /api/overlay/status", s.overlayStatus)
	admin("GET /api/log-drains", s.listLogDrains)
	admin("POST /api/log-drains", s.createLogDrain)
	admin("GET /api/log-drains/status", s.logDrainStatus)
	admin("POST /api/log-drains/test", s.testLogDrains)
	admin("PATCH /api/log-drains/{id}", s.updateLogDrain)
	admin("DELETE /api/log-drains/{id}", s.deleteLogDrain)
	admin("GET /api/settings/audit", s.getAuditSettings)
	admin("PUT /api/settings/audit", s.updateAuditSettings)
	admin("POST /api/settings/audit/test", s.testAuditStream)

	// ssh keys
	a("GET /api/ssh-keys", s.listSSHKeys)
	admin("POST /api/ssh-keys", s.createSSHKey)
	admin("DELETE /api/ssh-keys/{id}", s.deleteSSHKey)

	// servers
	a("GET /api/servers", s.listServers)
	admin("POST /api/servers", s.createServer)
	a("GET /api/servers/{id}", s.getServer)
	admin("PATCH /api/servers/{id}", s.updateServer)
	admin("DELETE /api/servers/{id}", s.deleteServer)
	admin("POST /api/servers/{id}/validate", s.validateServer)
	admin("POST /api/servers/{id}/setup", s.setupServer)
	admin("GET /api/servers/{id}/docker-network", s.serverDockerNetwork)
	admin("POST /api/servers/{id}/docker-network", s.applyServerDockerNetwork)
	admin("POST /api/network/test", s.testNetwork)
	admin("GET /api/servers/{id}/setup/stream", s.serverSetupStream)
	admin("POST /api/servers/{id}/reset-host-key", s.resetHostKey)
	a("GET /api/servers/{id}/metrics", s.serverMetrics)
	a("GET /api/servers/{id}/stats", s.serverStats)
	a("GET /api/fleet/containers", s.fleetContainers)
	admin("GET /api/fleet/images", s.fleetImages)
	admin("GET /api/fleet/volumes", s.fleetVolumes)
	a("GET /api/servers/{id}/containers", s.serverContainers)
	a("POST /api/servers/{id}/containers/{cid}/{action}", s.containerAction)
	a("GET /api/servers/{id}/containers/{cid}/inspect", s.containerInspect)
	a("GET /api/servers/{id}/containers/{cid}/env", s.containerEnv)
	a("GET /api/topology", s.topology)
	a("GET /api/servers/{id}/containers/{cid}/logs", s.containerLogs)
	a("GET /api/servers/{id}/containers/{cid}/terminal", s.containerTerminal)
	admin("GET /api/servers/{id}/terminal", s.serverTerminal)
	admin("GET /api/servers/{id}/images", s.serverImages)
	admin("DELETE /api/servers/{id}/images/{iid}", s.deleteImage)
	admin("GET /api/servers/{id}/volumes", s.serverVolumes)
	admin("DELETE /api/servers/{id}/volumes/{name}", s.deleteVolume)
	admin("GET /api/servers/{id}/networks", s.serverNetworks)
	admin("POST /api/servers/{id}/prune", s.pruneServer)
	admin("POST /api/servers/{id}/proxy/{action}", s.proxyAction)

	// projects & environments
	a("GET /api/projects", s.listProjects)
	a("POST /api/projects", s.createProject)
	gp("GET /api/projects/{id}", permRead, s.getProject)
	gp("PATCH /api/projects/{id}", permManage, s.updateProject)
	gp("DELETE /api/projects/{id}", permManage, s.deleteProject)
	gp("POST /api/projects/{id}/environments", permManage, s.createEnvironment)
	gp("GET /api/projects/{id}/members", permRead, s.listProjectMembers)
	gp("PUT /api/projects/{id}/members", permManage, s.setProjectMembers)
	gp("GET /api/projects/{id}/member-candidates", permManage, s.memberCandidates)
	g("DELETE /api/environments/{id}", "environment", permManage, s.deleteEnvironment)
	g("PATCH /api/environments/{id}", "environment", permManage, s.updateEnvironment)
	g("GET /api/environments/{id}/resources", "environment", permRead, s.environmentResources)

	// applications & deployments
	a("GET /api/applications", s.listApplications)
	a("POST /api/applications", s.createApplication)
	g("GET /api/applications/{id}", "application", permRead, s.getApplication)
	g("PATCH /api/applications/{id}", "application", permWrite, s.updateApplication)
	g("DELETE /api/applications/{id}", "application", permManage, s.deleteApplication)
	g("POST /api/applications/{id}/deploy", "application", permDeploy, s.deployApplication)
	g("POST /api/applications/{id}/{action}", "application", permWrite, s.applicationAction)
	g("POST /api/applications/{id}/webhook-secret", "application", permWrite, s.regenerateWebhookSecret)
	g("GET /api/applications/{id}/azure-hook", "application", permRead, s.azureHookInfo)
	g("GET /api/applications/{id}/scans", "application", permRead, s.listScans)
	g("GET /api/applications/{id}/scans/{scan}", "application", permRead, s.getScan)
	g("GET /api/applications/{id}/scans/{scan}/reports/{file}", "application", permRead, s.scanReport)
	g("GET /api/applications/{id}/scans/{scan}/reports.zip", "application", permRead, s.scanReportsZip)
	g("POST /api/applications/{id}/scan", "application", permWrite, s.scanApplication)
	g("POST /api/applications/{id}/azure-hook", "application", permWrite, s.azureHookInfo)
	g("GET /api/applications/{id}/deployments", "application", permRead, s.listAppDeployments)
	g("GET /api/applications/{id}/containers", "application", permRead, s.appContainers)
	g("GET /api/applications/{id}/logs", "application", permRead, s.appLogs)
	g("GET /api/applications/{id}/previews", "application", permRead, s.listPreviews)
	g("DELETE /api/applications/{id}/previews/{pr}", "application", permWrite, s.deletePreview)
	a("GET /api/deployments", s.listDeployments)
	g("GET /api/deployments/{id}", "deployment", permRead, s.getDeployment)
	g("GET /api/deployments/{id}/reports", "deployment", permRead, s.deploymentReports)
	g("GET /api/deployments/{id}/reports.zip", "deployment", permRead, s.deploymentReportsZip)
	g("GET /api/deployments/{id}/stream", "deployment", permRead, s.deploymentStream)
	g("POST /api/deployments/{id}/cancel", "deployment", permDeploy, s.cancelDeployment)
	g("POST /api/deployments/{id}/canary", "deployment", permDeploy, s.canaryDecision)
	g("POST /api/deployments/{id}/approve", "deployment", permManage, s.approveDeployment)
	g("POST /api/deployments/{id}/reject", "deployment", permManage, s.rejectDeployment)
	g("POST /api/deployments/{id}/rollback", "deployment", permDeploy, s.rollbackDeployment)

	// environment variables (owner: applications | services | databases), and
	// shared ones (environments, projects, the organization)
	for _, owner := range []string{"applications", "services", "databases", "environments"} {
		g("GET /api/"+owner+"/{id}/envs", "owner", permWrite, s.listEnvs)
		g("POST /api/"+owner+"/{id}/envs", "owner", permWrite, s.createEnv)
		g("PUT /api/"+owner+"/{id}/envs", "owner", permWrite, s.bulkEnvs)
	}
	gp("GET /api/projects/{id}/envs", permWrite, s.listEnvs)
	gp("POST /api/projects/{id}/envs", permWrite, s.createEnv)
	gp("PUT /api/projects/{id}/envs", permWrite, s.bulkEnvs)
	admin("GET /api/organization/envs", s.listEnvs)
	admin("POST /api/organization/envs", s.createEnv)
	admin("PUT /api/organization/envs", s.bulkEnvs)
	for _, owner := range []string{"applications", "services"} {
		g("GET /api/"+owner+"/{id}/envs/inherited", "owner", permWrite, s.inheritedEnvs)
	}
	g("GET /api/applications/{id}/envs/compare", "application", permWrite, s.compareEnvs)
	g("POST /api/applications/{id}/envs/promote", "application", permWrite, s.promoteEnvs)
	// secret store references: does one resolve, and which keys its path holds
	for _, owner := range []string{"applications", "services", "environments"} {
		g("POST /api/"+owner+"/{id}/envs/check-ref", "owner", permWrite, s.checkSecretRef)
	}
	gp("POST /api/projects/{id}/envs/check-ref", permWrite, s.checkSecretRef)
	admin("POST /api/organization/envs/check-ref", s.checkSecretRef)
	a("PATCH /api/envs/{id}", s.guardEnvVar(permWrite, s.updateEnv))
	a("DELETE /api/envs/{id}", s.guardEnvVar(permWrite, s.deleteEnv))

	// databases & backups
	a("GET /api/database-engines", s.listEngines)
	a("GET /api/databases", s.listDatabases)
	a("POST /api/databases", s.createDatabase)
	g("GET /api/databases/{id}", "database", permRead, s.getDatabase)
	g("PATCH /api/databases/{id}", "database", permWrite, s.updateDatabase)
	g("DELETE /api/databases/{id}", "database", permManage, s.deleteDatabase)
	g("POST /api/databases/{id}/{action}", "database", permWrite, s.databaseAction)
	g("GET /api/databases/{id}/logs", "database", permRead, s.databaseLogs)
	g("GET /api/databases/{id}/console", "database", permRead, s.databaseConsole)
	g("GET /api/databases/{id}/tables", "database", permRead, s.databaseTables)
	g("POST /api/databases/{id}/query", "database", permRead, s.runDatabaseQuery)
	g("GET /api/databases/{id}/backups", "database", permRead, s.listBackups)
	g("POST /api/databases/{id}/backups", "database", permWrite, s.createBackup)
	g("PATCH /api/backups/{id}", "backup", permWrite, s.updateBackup)
	g("DELETE /api/backups/{id}", "backup", permWrite, s.deleteBackup)
	g("POST /api/backups/{id}/run", "backup", permWrite, s.runBackup)
	g("GET /api/backups/{id}/runs", "backup", permRead, s.listBackupRuns)
	g("GET /api/backup-runs/{id}/download", "backup_run", permWrite, s.downloadBackup)
	g("DELETE /api/backup-runs/{id}", "backup_run", permWrite, s.deleteBackupRun)
	g("GET /api/databases/{id}/backup-runs", "database", permRead, s.backupRunsOf)
	g("POST /api/databases/{id}/backup-now", "database", permWrite, s.backupNow)
	g("GET /api/databases/{id}/pitr", "database", permRead, s.getPITR)
	g("PUT /api/databases/{id}/pitr", "database", permManage, s.updatePITR)
	g("POST /api/databases/{id}/pitr/backup", "database", permWrite, s.pitrBackupNow)
	g("POST /api/databases/{id}/pitr/restore", "database", permManage, s.pitrRestore)
	g("POST /api/applications/{id}/deploy-key", "application", permWrite, s.createDeployKey)
	g("DELETE /api/applications/{id}/deploy-key", "application", permWrite, s.deleteDeployKey)
	a("GET /api/git/known-hosts", s.listGitHosts)
	admin("POST /api/git/known-hosts", s.addGitHost)
	admin("DELETE /api/git/known-hosts", s.deleteGitHost)
	admin("POST /api/git/known-hosts/scan", s.scanGitHost)
	admin("PUT /api/git/known-hosts/strict", s.setGitStrict)
	g("GET /api/databases/{id}/replicas", "database", permRead, s.listReplicas)
	g("POST /api/databases/{id}/replicas", "database", permManage, s.createReplica)
	g("PUT /api/databases/{id}/failover", "database", permManage, s.updateFailover)
	g("DELETE /api/replicas/{id}", "replica", permManage, s.deleteReplica)
	g("POST /api/replicas/{id}/promote", "replica", permManage, s.promoteReplica)
	admin("POST /api/databases/backup-all", s.backupAllDatabases)
	// backup of PortDock itself
	admin("GET /api/system/backup", s.instanceBackupStatus)
	admin("PUT /api/system/backup", s.saveInstanceBackup)
	admin("POST /api/system/backup/run", s.runInstanceBackup)
	admin("GET /api/system/backup/s3", s.listInstanceBackupsS3)
	admin("POST /api/system/backup/s3/{name}/fetch", s.fetchInstanceBackupS3)
	admin("GET /api/system/backup/files/{name}", s.downloadInstanceBackup)
	admin("DELETE /api/system/backup/files/{name}", s.deleteInstanceBackup)
	admin("POST /api/system/backup/files/{name}/restore", s.restoreInstanceBackup)
	public("POST /api/setup/restore", s.setupRestore)
	g("POST /api/backup-runs/{id}/restore", "backup_run", permManage, s.restoreBackup)
	g("POST /api/backup-runs/{id}/verify", "backup_run", permWrite, s.verifyBackup)

	// services
	a("GET /api/service-templates", s.listServiceTemplates)
	a("GET /api/service-templates/{key...}", s.getServiceTemplate)
	// import from Coolify, Heroku or Dokku
	admin("POST /api/migrations/plan", s.planMigration)
	admin("POST /api/migrations/apply", s.applyMigration)
	// catalogs of service templates (community, Coolify's, internal mirrors)
	admin("GET /api/template-catalogs", s.listCatalogs)
	admin("POST /api/template-catalogs", s.createCatalog)
	admin("PATCH /api/template-catalogs/{id}", s.updateCatalog)
	admin("POST /api/template-catalogs/{id}/refresh", s.refreshCatalog)
	admin("DELETE /api/template-catalogs/{id}", s.deleteCatalog)
	a("GET /api/image-catalog", func(w http.ResponseWriter, r *http.Request) error { return ok(w, templates.ImageCatalog()) })
	a("GET /api/services", s.listServices)
	a("POST /api/services", s.createService)
	g("GET /api/services/{id}", "service", permRead, s.getService)
	g("PATCH /api/services/{id}", "service", permWrite, s.updateService)
	g("DELETE /api/services/{id}", "service", permManage, s.deleteService)
	g("POST /api/services/{id}/deploy", "service", permWrite, s.deployService)
	g("POST /api/services/{id}/{action}", "service", permWrite, s.serviceAction)
	g("GET /api/services/{id}/containers", "service", permRead, s.serviceContainers)
	g("GET /api/services/{id}/stream", "service", permRead, s.serviceStream)

	// settings, users, tokens, notifications
	a("GET /api/settings", s.getSettings)
	admin("PATCH /api/settings", s.updateSettings)
	// AI assistant
	// MCP: every tool calls the API with the caller's token, so its scope applies per tool.
	record("POST /api/mcp", "user", 0)
	guarded("POST /api/mcp", s.mcp)
	a("GET /api/chat/status", s.chatStatus)
	admin("GET /api/chat/settings", s.getChatSettings)
	admin("PUT /api/chat/settings", s.updateChatSettings)
	admin("GET /api/chat/models", s.chatModels)
	admin("POST /api/chat/test", s.testChat)
	a("GET /api/chat/conversations", s.listChatConversations)
	a("GET /api/chat/conversations/{id}", s.getChatConversation)
	a("DELETE /api/chat/conversations/{id}", s.deleteChatConversation)
	a("POST /api/chat/send", s.sendChat)
	// actions the assistant proposed run only after the user confirms them
	a("POST /api/chat/actions/{id}/confirm", s.confirmChatAction)
	a("POST /api/chat/actions/{id}/cancel", s.cancelChatAction)

	admin("GET /api/users", s.listUsers)
	admin("POST /api/users", s.createUser)
	admin("PATCH /api/users/{id}", s.updateUser)
	admin("DELETE /api/users/{id}", s.deleteUser)
	admin("POST /api/users/{id}/2fa/reset", s.resetUserTwoFactor)
	admin("POST /api/users/{id}/sessions/revoke", s.revokeUserSessions)
	admin("GET /api/sso/providers", s.listSSOProviders)
	admin("POST /api/sso/providers", s.createSSOProvider)
	admin("PATCH /api/sso/providers/{id}", s.updateSSOProvider)
	admin("DELETE /api/sso/providers/{id}", s.deleteSSOProvider)
	admin("POST /api/sso/discover", s.discoverSSO)
	admin("GET /api/ldap/directories", s.listLDAP)
	admin("POST /api/ldap/directories", s.createLDAP)
	admin("PATCH /api/ldap/directories/{id}", s.updateLDAP)
	admin("DELETE /api/ldap/directories/{id}", s.deleteLDAP)
	admin("POST /api/ldap/test", s.testLDAP)
	a("GET /api/metrics", s.prometheusMetrics)
	a("GET /api/metrics/{scope}/{id}", s.metricsSeries)
	a("GET /api/alerts", s.listAlerts)
	a("POST /api/alerts/{id}/ack", s.ackAlert)
	a("GET /api/alert-rules", s.listAlertRules)
	admin("POST /api/alert-rules", s.createAlertRule)
	admin("PATCH /api/alert-rules/{id}", s.updateAlertRule)
	admin("DELETE /api/alert-rules/{id}", s.deleteAlertRule)
	admin("PUT /api/notifications/{id}/alerts", s.setChannelAlerts)
	a("GET /api/registries", s.listRegistries)
	admin("POST /api/registries", s.createRegistry)
	admin("PATCH /api/registries/{id}", s.updateRegistry)
	admin("DELETE /api/registries/{id}", s.deleteRegistry)
	admin("POST /api/registries/{id}/test", s.testRegistry)
	admin("POST /api/registries/test", s.testRegistryDraft)
	a("GET /api/tokens", s.listTokens)
	a("POST /api/tokens", s.createToken)
	// service accounts (automation users that only act through tokens)
	admin("GET /api/service-accounts", s.listServiceAccounts)
	admin("POST /api/service-accounts", s.createServiceAccount)
	admin("PATCH /api/service-accounts/{id}", s.updateServiceAccount)
	admin("DELETE /api/service-accounts/{id}", s.deleteServiceAccount)
	admin("POST /api/service-accounts/{id}/tokens", s.createServiceAccountToken)
	admin("DELETE /api/service-accounts/{id}/tokens/{tid}", s.deleteServiceAccountToken)
	a("DELETE /api/tokens/{id}", s.deleteToken)
	admin("GET /api/notifications", s.listNotifications)
	admin("POST /api/notifications", s.createNotification)
	admin("PATCH /api/notifications/{id}", s.updateNotification)
	admin("DELETE /api/notifications/{id}", s.deleteNotification)
	admin("POST /api/notifications/{id}/test", s.testNotification)

	// domains
	g("GET /api/applications/{id}/domains", "application", permRead, s.listDomains)
	g("POST /api/applications/{id}/domains", "application", permWrite, s.createDomain)
	g("POST /api/applications/{id}/domains/check", "application", permRead, s.checkAllDomains)
	g("POST /api/applications/{id}/domains/https", "application", permWrite, s.setAppHTTPS)
	g("GET /api/applications/{id}/domains/suggest", "application", permRead, s.suggestDomain)
	// Includes each service's environment from the compose file, so it needs write access like the variables.
	g("GET /api/applications/{id}/compose-services", "application", permWrite, s.composeServices)
	g("POST /api/applications/{id}/apply-env", "application", permDeploy, s.applyEnv)
	g("GET /api/applications/{id}/versions", "application", permRead, s.listVersions)
	g("POST /api/applications/{id}/versions/{vid}/deploy", "application", permDeploy, s.deployVersion)
	g("GET /api/applications/{id}/dns-records", "application", permRead, s.dnsRecords)
	g("PATCH /api/domains/{id}", "domain", permWrite, s.updateDomain)
	g("DELETE /api/domains/{id}", "domain", permWrite, s.deleteDomain)
	g("POST /api/domains/{id}/check", "domain", permRead, s.checkDomainDNS)
	// configuration as code (portdock.yaml)
	a("POST /api/gitops/apply", s.gitopsApply)
	g("GET /api/applications/{id}/export", "application", permWrite, s.exportApplication)
	// scheduled jobs (cron)
	g("GET /api/applications/{id}/jobs", "application", permRead, s.listJobs)
	g("POST /api/applications/{id}/jobs", "application", permWrite, s.createJob)
	g("PATCH /api/jobs/{id}", "job", permWrite, s.updateJob)
	g("DELETE /api/jobs/{id}", "job", permWrite, s.deleteJob)
	g("POST /api/jobs/{id}/run", "job", permDeploy, s.runJobNow)
	g("GET /api/jobs/{id}/runs", "job", permRead, s.listJobRuns)
	g("GET /api/job-runs/{id}", "job_run", permRead, s.getJobRun)
	// access protection (IP allow list, rate limit, password or single sign-on)
	g("GET /api/applications/{id}/protection", "application", permRead, s.getProtection)
	g("PUT /api/applications/{id}/protection", "application", permWrite, s.updateProtection)
	public("/api/forward-auth/{uuid}", s.forwardAuth) // asked by the proxy, any method
	public("GET /api/forward-auth/{uuid}/login", s.forwardAuthLogin)

	// persistent storage
	g("GET /api/applications/{id}/volumes", "application", permRead, s.listAppVolumes)
	g("GET /api/applications/{id}/volumes/usage", "application", permRead, s.appVolumeUsage)
	g("POST /api/applications/{id}/volumes", "application", permWrite, s.createAppVolume)
	g("DELETE /api/applications/{id}/volume-data/{name}", "application", permManage, s.deleteAppVolumeData)
	g("PATCH /api/volumes/{id}", "volume", permWrite, s.updateAppVolume)
	g("DELETE /api/volumes/{id}", "volume", permWrite, s.deleteAppVolume)
	admin("POST /api/settings/detect-ip", s.detectPublicIPs)
	a("GET /api/dns/check", s.checkHostDNS)

	// GitHub App integration
	admin("GET /api/github/apps", s.listGitHubApps)
	admin("POST /api/github/apps", s.registerGitHubApp)
	admin("POST /api/github/apps/manifest", s.createGitHubManifest)
	admin("POST /api/github/apps/{id}/sync", s.syncGitHubApp)
	admin("POST /api/github/apps/{id}/webhook-secret", s.rotateGitHubWebhookSecret)
	admin("DELETE /api/github/apps/{id}", s.deleteGitHubApp)
	public("GET /api/github/callback", s.githubCallback)
	public("GET /api/github/setup", s.githubSetup)
	a("GET /api/github/repositories", s.listGitHubRepositories)
	a("GET /api/github/branches", s.listGitHubBranches)
	a("GET /api/github/detect", s.detectGitHubRepo)
	public("POST /api/webhooks/github-app", s.githubAppWebhook)
	a("GET /api/monitors", s.listMonitors)
	a("GET /api/monitors/{id}", s.getMonitor)
	admin("POST /api/monitors", s.createMonitor)
	admin("PATCH /api/monitors/{id}", s.updateMonitor)
	admin("DELETE /api/monitors/{id}", s.deleteMonitor)
	admin("POST /api/monitors/{id}/check", s.checkMonitor)
	public("GET /api/public/status", s.publicStatus)
	a("GET /api/azure/connections", s.listAzureConnections)
	admin("POST /api/azure/connections", s.createAzureConnection)
	admin("PATCH /api/azure/connections/{id}", s.updateAzureConnection)
	admin("DELETE /api/azure/connections/{id}", s.deleteAzureConnection)
	a("GET /api/azure/repositories", s.listAzureRepositories)
	a("GET /api/azure/branches", s.listAzureBranches)
	a("GET /api/azure/detect", s.detectAzureRepo)

	// webhooks (authenticated by per-application secret)
	public("POST /api/webhooks/github/{uuid}", s.githubWebhook)
	public("POST /api/webhooks/gitea/{uuid}", s.githubWebhook)
	public("POST /api/webhooks/gitlab/{uuid}", s.gitlabWebhook)
	public("POST /api/webhooks/azure/{uuid}", s.azureWebhook)
	public("POST /api/webhooks/deploy/{uuid}", s.deployHook)

	// Streamable HTTP: no server-sent event stream and no sessions to end.
	mux.HandleFunc("/api/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "POST")
		writeJSON(w, 405, map[string]string{"error": "the MCP endpoint only takes POST"})
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, map[string]string{"error": "no such endpoint"})
	})
	mux.Handle("/", s.spa())
	s.self = s.handlerChain(mux)
	return s.self
}

// publicURL is the instance URL configured in Settings, or PORTDOCK_PUBLIC_URL.
func (s *Server) publicURL(ctx context.Context) string { return s.engine.PublicURL(ctx) }

// Version is set at build time via -ldflags.
var Version = "dev"

// spa serves the embedded frontend, falling back to index.html for client routes.
func (s *Server) spa() http.Handler {
	files := http.FileServerFS(s.ui)
	scriptHashes := inlineScriptHashes(s.ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			files.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(s.ui, p); err != nil {
			p = "index.html"
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		if p == "index.html" {
			// Always revalidated, so an update is picked up on the next load.
			w.Header().Set("Cache-Control", "no-cache")
			s.pageSecurityHeaders(w, r, scriptHashes)
		}
		files.ServeHTTP(w, r)
	})
}

// pageSecurityHeaders locks the interface down: only its own scripts (plus the
// theme bootstrap inlined in index.html, by hash), styles, images and API.
func (s *Server) pageSecurityHeaders(w http.ResponseWriter, r *http.Request, scriptHashes []string) {
	host := r.Host // what the browser connected to; proxies forward the Host header
	csp := "default-src 'self'; script-src 'self'" + strings.Join(scriptHashes, "") +
		"; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; font-src 'self' data:" +
		"; connect-src 'self' ws://" + host + " wss://" + host +
		"; frame-ancestors 'none'; base-uri 'self'; form-action 'self'; object-src 'none'; worker-src 'self' blob:"
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
	if s.auth.SecureCookies() {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	}
}

// inlineScriptHashes returns the CSP hash sources of the inline scripts of
// index.html (the theme bootstrap), so they run under the policy.
func inlineScriptHashes(ui fs.FS) []string {
	if ui == nil {
		return nil
	}
	b, err := fs.ReadFile(ui, "index.html")
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range inlineScriptRe.FindAllSubmatch(b, -1) {
		// The HTML parser turns CR and CRLF into LF before the browser hashes
		// the script, so the hash is taken over the same normalized text.
		src := bytes.ReplaceAll(bytes.ReplaceAll(m[1], []byte("\r\n"), []byte("\n")), []byte("\r"), []byte("\n"))
		sum := sha256.Sum256(src)
		out = append(out, " 'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return out
}

var inlineScriptRe = regexp.MustCompile(`(?s)<script(?:\s+(?:type="module"|nonce="[^"]*"))?>(.*?)</script>`)
