// Package api exposes Shipyard's REST API, SSE streams, WebSocket terminals,
// git webhooks and serves the embedded web UI.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/shipyard/shipyard/internal/auth"
	"github.com/shipyard/shipyard/internal/config"
	"github.com/shipyard/shipyard/internal/deploy"
	"github.com/shipyard/shipyard/internal/executor"
	"github.com/shipyard/shipyard/internal/github"
	"github.com/shipyard/shipyard/internal/notify"
	"github.com/shipyard/shipyard/internal/scheduler"
	"github.com/shipyard/shipyard/internal/secrets"
	"github.com/shipyard/shipyard/internal/store"
	"github.com/shipyard/shipyard/internal/stream"
	"github.com/shipyard/shipyard/internal/templates"
)

type Server struct {
	cfg    config.Config
	db     *store.DB
	box    *secrets.Box
	auth   *auth.Auth
	pool   *executor.Pool
	hub    *stream.Hub
	engine *deploy.Engine
	sched  *scheduler.Scheduler
	notify *notify.Notifier
	github *github.Client
	ui     fs.FS
	sec    *security
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
	UI        fs.FS
}

func New(d Deps) *Server {
	return &Server{cfg: d.Config, db: d.DB, box: d.Box, auth: d.Auth, pool: d.Pool, hub: d.Hub,
		engine: d.Engine, sched: d.Scheduler, notify: d.Notifier, github: d.GitHub, ui: d.UI, sec: newSecurity()}
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
	a("GET /api/servers/{id}/containers/{cid}/logs", s.containerLogs)
	a("GET /api/servers/{id}/containers/{cid}/terminal", s.containerTerminal)
	admin("GET /api/servers/{id}/terminal", s.serverTerminal)
	a("GET /api/servers/{id}/images", s.serverImages)
	a("DELETE /api/servers/{id}/images/{iid}", s.deleteImage)
	a("GET /api/servers/{id}/volumes", s.serverVolumes)
	a("DELETE /api/servers/{id}/volumes/{name}", s.deleteVolume)
	a("GET /api/servers/{id}/networks", s.serverNetworks)
	admin("POST /api/servers/{id}/prune", s.pruneServer)
	admin("POST /api/servers/{id}/proxy/{action}", s.proxyAction)

	// projects & environments
	a("GET /api/projects", s.listProjects)
	a("POST /api/projects", s.createProject)
	a("GET /api/projects/{id}", s.getProject)
	a("PATCH /api/projects/{id}", s.updateProject)
	a("DELETE /api/projects/{id}", s.deleteProject)
	a("POST /api/projects/{id}/environments", s.createEnvironment)
	a("DELETE /api/environments/{id}", s.deleteEnvironment)
	a("GET /api/environments/{id}/resources", s.environmentResources)

	// applications & deployments
	a("GET /api/applications", s.listApplications)
	a("POST /api/applications", s.createApplication)
	a("GET /api/applications/{id}", s.getApplication)
	a("PATCH /api/applications/{id}", s.updateApplication)
	a("DELETE /api/applications/{id}", s.deleteApplication)
	a("POST /api/applications/{id}/deploy", s.deployApplication)
	a("POST /api/applications/{id}/{action}", s.applicationAction)
	a("POST /api/applications/{id}/webhook-secret", s.regenerateWebhookSecret)
	a("GET /api/applications/{id}/deployments", s.listAppDeployments)
	a("GET /api/applications/{id}/containers", s.appContainers)
	a("GET /api/applications/{id}/logs", s.appLogs)
	a("GET /api/applications/{id}/previews", s.listPreviews)
	a("DELETE /api/applications/{id}/previews/{pr}", s.deletePreview)
	a("GET /api/deployments", s.listDeployments)
	a("GET /api/deployments/{id}", s.getDeployment)
	a("GET /api/deployments/{id}/stream", s.deploymentStream)
	a("POST /api/deployments/{id}/cancel", s.cancelDeployment)
	a("POST /api/deployments/{id}/rollback", s.rollbackDeployment)

	// environment variables (owner: applications | services | databases)
	for _, owner := range []string{"applications", "services", "databases"} {
		a("GET /api/"+owner+"/{id}/envs", s.listEnvs)
		a("POST /api/"+owner+"/{id}/envs", s.createEnv)
		a("PUT /api/"+owner+"/{id}/envs", s.bulkEnvs)
	}
	a("PATCH /api/envs/{id}", s.updateEnv)
	a("DELETE /api/envs/{id}", s.deleteEnv)

	// databases & backups
	a("GET /api/database-engines", s.listEngines)
	a("GET /api/databases", s.listDatabases)
	a("POST /api/databases", s.createDatabase)
	a("GET /api/databases/{id}", s.getDatabase)
	a("PATCH /api/databases/{id}", s.updateDatabase)
	a("DELETE /api/databases/{id}", s.deleteDatabase)
	a("POST /api/databases/{id}/{action}", s.databaseAction)
	a("GET /api/databases/{id}/logs", s.databaseLogs)
	a("GET /api/databases/{id}/backups", s.listBackups)
	a("POST /api/databases/{id}/backups", s.createBackup)
	a("PATCH /api/backups/{id}", s.updateBackup)
	a("DELETE /api/backups/{id}", s.deleteBackup)
	a("POST /api/backups/{id}/run", s.runBackup)
	a("GET /api/backups/{id}/runs", s.listBackupRuns)
	a("GET /api/backup-runs/{id}/download", s.downloadBackup)
	a("POST /api/backup-runs/{id}/restore", s.restoreBackup)

	// services
	a("GET /api/service-templates", s.listServiceTemplates)
	a("GET /api/image-catalog", func(w http.ResponseWriter, r *http.Request) error { return ok(w, templates.ImageCatalog()) })
	a("GET /api/services", s.listServices)
	a("POST /api/services", s.createService)
	a("GET /api/services/{id}", s.getService)
	a("PATCH /api/services/{id}", s.updateService)
	a("DELETE /api/services/{id}", s.deleteService)
	a("POST /api/services/{id}/deploy", s.deployService)
	a("POST /api/services/{id}/{action}", s.serviceAction)
	a("GET /api/services/{id}/containers", s.serviceContainers)
	a("GET /api/services/{id}/stream", s.serviceStream)

	// settings, users, tokens, notifications
	a("GET /api/settings", s.getSettings)
	admin("PATCH /api/settings", s.updateSettings)
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
	a("GET /api/tokens", s.listTokens)
	a("POST /api/tokens", s.createToken)
	a("DELETE /api/tokens/{id}", s.deleteToken)
	admin("GET /api/notifications", s.listNotifications)
	admin("POST /api/notifications", s.createNotification)
	admin("PATCH /api/notifications/{id}", s.updateNotification)
	admin("DELETE /api/notifications/{id}", s.deleteNotification)
	admin("POST /api/notifications/{id}/test", s.testNotification)

	// domains
	a("GET /api/applications/{id}/domains", s.listDomains)
	a("POST /api/applications/{id}/domains", s.createDomain)
	a("POST /api/applications/{id}/domains/check", s.checkAllDomains)
	a("POST /api/applications/{id}/domains/https", s.setAppHTTPS)
	a("GET /api/applications/{id}/domains/suggest", s.suggestDomain)
	a("GET /api/applications/{id}/dns-records", s.dnsRecords)
	a("PATCH /api/domains/{id}", s.updateDomain)
	a("DELETE /api/domains/{id}", s.deleteDomain)
	a("POST /api/domains/{id}/check", s.checkDomainDNS)
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

	// webhooks (authenticated by per-application secret)
	public("POST /api/webhooks/github/{uuid}", s.githubWebhook)
	public("POST /api/webhooks/gitea/{uuid}", s.githubWebhook)
	public("POST /api/webhooks/gitlab/{uuid}", s.gitlabWebhook)
	public("POST /api/webhooks/deploy/{uuid}", s.deployHook)
	public("GET /api/webhooks/deploy/{uuid}", s.deployHook)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, map[string]string{"error": "no such endpoint"})
	})
	mux.Handle("/", s.spa())
	return s.middleware(mux)
}

// publicURL is the instance URL configured in Settings, or SHIPYARD_PUBLIC_URL.
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
