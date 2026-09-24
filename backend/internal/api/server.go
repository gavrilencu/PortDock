// Package api exposes PortDock's REST API, SSE streams, WebSocket terminals,
// git webhooks and serves the embedded web UI.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/portdock/portdock/internal/assistant"
	"io/fs"
	"log/slog"
	"net/http"
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
	"github.com/portdock/portdock/internal/monitor"
	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/scheduler"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
	"github.com/portdock/portdock/internal/uptime"
	"github.com/portdock/portdock/internal/stream"
	"github.com/portdock/portdock/internal/templates"
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
	sec     *security
	updates *updateCache
	monitor *monitor.Monitor
	audit   *audit.Streamer
	ai      *assistant.Client
	uptime  *uptime.Checker
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
}

func New(d Deps) *Server {
	if d.Audit == nil {
		d.Audit = audit.New(d.DB, d.Box)
	}
	if d.Uptime == nil && d.Engine != nil {
		d.Uptime = uptime.New(d.DB, d.Notifier, d.Engine.PublicURL)
	}
	s := &Server{cfg: d.Config, db: d.DB, box: d.Box, auth: d.Auth, pool: d.Pool, hub: d.Hub,
		engine: d.Engine, sched: d.Scheduler, notify: d.Notifier, github: d.GitHub, ui: d.UI, sec: newSecurity(), monitor: d.Monitor, audit: d.Audit, updates: &updateCache{}, ai: assistant.New(), uptime: d.Uptime}
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
		if err != nil {
			return errStatus(401, "authentication required")
		}
		// Users the policy requires to enroll in 2FA may only reach /api/auth/*.
		if !strings.HasPrefix(r.URL.Path, "/api/auth/") && s.needsMFASetup(r.Context(), u, sess) {
			return errStatus(403, "two-factor authentication must be enabled for your account")
		}
		if rl == adminOnly && !u.IsAdmin() {
			return errStatus(403, "admin role required")
		}
		return h(w, r.WithContext(auth.WithSession(auth.WithUser(r.Context(), u), sess)))
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
		}
		next.ServeHTTP(rec, r)
	})
}

// Handler builds the full HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	a := func(pattern string, h handlerFunc) { mux.HandleFunc(pattern, s.authed(anyUser, h)) }
	admin := func(pattern string, h handlerFunc) { mux.HandleFunc(pattern, s.authed(adminOnly, h)) }
	public := func(pattern string, h handlerFunc) { mux.HandleFunc(pattern, s.wrap(h)) }
	// g guards a resource route by project role; gp guards a project route.
	g := func(pattern, kind string, p perm, h handlerFunc) { a(pattern, s.guard(kind, p, h)) }
	gp := func(pattern string, p perm, h handlerFunc) { a(pattern, s.guardProject(p, h)) }

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
	a("POST /api/servers/{id}/validate", s.validateServer)
	admin("POST /api/servers/{id}/setup", s.setupServer)
	a("GET /api/servers/{id}/setup/stream", s.serverSetupStream)
	admin("POST /api/servers/{id}/reset-host-key", s.resetHostKey)
	a("GET /api/servers/{id}/metrics", s.serverMetrics)
	a("GET /api/servers/{id}/stats", s.serverStats)
	a("GET /api/servers/{id}/containers", s.serverContainers)
	a("POST /api/servers/{id}/containers/{cid}/{action}", s.containerAction)
	a("GET /api/servers/{id}/containers/{cid}/inspect", s.containerInspect)
	a("GET /api/servers/{id}/containers/{cid}/env", s.containerEnv)
	a("GET /api/topology", s.topology)
	a("GET /api/servers/{id}/containers/{cid}/logs", s.containerLogs)
	a("GET /api/servers/{id}/containers/{cid}/terminal", s.containerTerminal)
	admin("GET /api/servers/{id}/terminal", s.serverTerminal)
	a("GET /api/servers/{id}/images", s.serverImages)
	admin("DELETE /api/servers/{id}/images/{iid}", s.deleteImage)
	a("GET /api/servers/{id}/volumes", s.serverVolumes)
	admin("DELETE /api/servers/{id}/volumes/{name}", s.deleteVolume)
	a("GET /api/servers/{id}/networks", s.serverNetworks)
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
	g("POST /api/applications/{id}/scan", "application", permWrite, s.scanApplication)
	g("POST /api/applications/{id}/azure-hook", "application", permWrite, s.azureHookInfo)
	g("GET /api/applications/{id}/deployments", "application", permRead, s.listAppDeployments)
	g("GET /api/applications/{id}/containers", "application", permRead, s.appContainers)
	g("GET /api/applications/{id}/logs", "application", permRead, s.appLogs)
	g("GET /api/applications/{id}/previews", "application", permRead, s.listPreviews)
	g("DELETE /api/applications/{id}/previews/{pr}", "application", permWrite, s.deletePreview)
	a("GET /api/deployments", s.listDeployments)
	g("GET /api/deployments/{id}", "deployment", permRead, s.getDeployment)
	g("GET /api/deployments/{id}/stream", "deployment", permRead, s.deploymentStream)
	g("POST /api/deployments/{id}/cancel", "deployment", permDeploy, s.cancelDeployment)
	g("POST /api/deployments/{id}/approve", "deployment", permManage, s.approveDeployment)
	g("POST /api/deployments/{id}/reject", "deployment", permManage, s.rejectDeployment)
	g("POST /api/deployments/{id}/rollback", "deployment", permDeploy, s.rollbackDeployment)

	// environment variables (owner: applications | services | databases)
	for _, owner := range []string{"applications", "services", "databases"} {
		g("GET /api/"+owner+"/{id}/envs", "owner", permWrite, s.listEnvs)
		g("POST /api/"+owner+"/{id}/envs", "owner", permWrite, s.createEnv)
		g("PUT /api/"+owner+"/{id}/envs", "owner", permWrite, s.bulkEnvs)
	}
	g("PATCH /api/envs/{id}", "env_var", permWrite, s.updateEnv)
	g("DELETE /api/envs/{id}", "env_var", permWrite, s.deleteEnv)

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
	admin("POST /api/databases/backup-all", s.backupAllDatabases)
	g("POST /api/backup-runs/{id}/restore", "backup_run", permManage, s.restoreBackup)

	// services
	a("GET /api/service-templates", s.listServiceTemplates)
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
	a("GET /api/chat/status", s.chatStatus)
	admin("GET /api/chat/settings", s.getChatSettings)
	admin("PUT /api/chat/settings", s.updateChatSettings)
	admin("GET /api/chat/models", s.chatModels)
	admin("POST /api/chat/test", s.testChat)
	a("GET /api/chat/conversations", s.listChatConversations)
	a("GET /api/chat/conversations/{id}", s.getChatConversation)
	a("DELETE /api/chat/conversations/{id}", s.deleteChatConversation)
	a("POST /api/chat/send", s.sendChat)

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
	public("GET /api/webhooks/deploy/{uuid}", s.deployHook)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, map[string]string{"error": "no such endpoint"})
	})
	mux.Handle("/", s.spa())
	return s.middleware(mux)
}

// publicURL is the instance URL configured in Settings, or PORTDOCK_PUBLIC_URL.
func (s *Server) publicURL(ctx context.Context) string { return s.engine.PublicURL(ctx) }

// Version is set at build time via -ldflags.
var Version = "dev"

// spa serves the embedded frontend, falling back to index.html for client routes.
func (s *Server) spa() http.Handler {
	files := http.FileServerFS(s.ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(s.ui, p); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			w.Header().Set("Cache-Control", "no-cache")
			files.ServeHTTP(w, r2)
			return
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}
