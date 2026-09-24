package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/deploy"
	"github.com/portdock/portdock/internal/proxy"
	"github.com/portdock/portdock/internal/scheduler"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
	"github.com/portdock/portdock/internal/templates"
)

func (s *Server) listEngines(w http.ResponseWriter, r *http.Request) error {
	return ok(w, templates.EngineList())
}

type databaseDTO struct {
	store.Database
	Password    string            `json:"password"`
	InternalURL string            `json:"internal_url"`
	PublicURL   string            `json:"public_url"`
	Hostname    string            `json:"hostname"`
	Port        int               `json:"port"`
	EngineName  string            `json:"engine_name"`
	Project     store.Project     `json:"project"`
	Environment store.Environment `json:"environment"`
	ServerName  string            `json:"server_name"`
	Access      Access            `json:"access"`
	// PublicAddress is the host external clients connect to (see dbPublicFallback).
	PublicAddress string `json:"public_address"`
	// Recreated is set by an update that recreated the container to apply a new public port.
	Recreated bool `json:"recreated,omitempty"`
	// DNSStatus/DNSDetail are filled on GET ?dns=1 when a public host is set.
	DNSStatus string `json:"dns_status,omitempty"`
	DNSDetail string `json:"dns_detail,omitempty"`
}

// dbPublicFallback returns the address to advertise for a database without a public
// host on a server whose own host is not reachable from outside: the server's
// public IPv4 (Settings → public IP for the local server), else the instance URL's host.
func (s *Server) dbPublicFallback(ctx context.Context, srv store.Server) string {
	if !srv.IsLocal && srv.Host != "" && !deploy.IsLoopbackHost(srv.Host) {
		return "" // the server host is used as is
	}
	var v6 string
	for _, ip := range s.serverIPs(ctx, srv) {
		if ip.IsLoopback() || ip.IsUnspecified() {
			continue
		}
		if v4 := ip.To4(); v4 != nil {
			return v4.String()
		}
		if v6 == "" {
			v6 = ip.String()
		}
	}
	if v6 != "" {
		return v6
	}
	if u := strings.TrimSpace(s.db.Setting(ctx, "instance_url")); u != "" {
		if !strings.Contains(u, "://") {
			u = "http://" + u
		}
		if pu, err := url.Parse(u); err == nil && pu.Hostname() != "" && !deploy.IsLoopbackHost(pu.Hostname()) {
			return pu.Hostname()
		}
	}
	return ""
}

func (s *Server) dbOut(ctx context.Context, db store.Database) databaseDTO {
	srv, _ := store.ByID[store.Server](ctx, s.db, "servers", db.ServerID)
	fallback := ""
	if db.PublicHost == "" {
		fallback = s.dbPublicFallback(ctx, srv)
	}
	internal, public := s.engine.DatabaseURLs(db, srv, fallback)
	eng := templates.Engines[db.Engine]
	dto := databaseDTO{Database: db, Password: s.box.MustDecrypt(db.Password), InternalURL: internal, PublicURL: public,
		Hostname: db.UUID, Port: eng.Port, EngineName: eng.Name, ServerName: srv.Name,
		PublicAddress: deploy.DatabasePublicHost(db, srv, fallback)}
	dto.Environment, _ = store.ByID[store.Environment](ctx, s.db, "environments", db.EnvironmentID)
	dto.Project, _ = store.ByID[store.Project](ctx, s.db, "projects", dto.Environment.ProjectID)
	dto.Access = s.resourceAccess(ctx, db.EnvironmentID)
	if !dto.Access.CanWrite {
		// Viewers (and developers on protected environments) don't get credentials.
		if dto.Password != "" {
			dto.InternalURL = strings.ReplaceAll(dto.InternalURL, dto.Password, "********")
			dto.PublicURL = strings.ReplaceAll(dto.PublicURL, dto.Password, "********")
		}
		dto.Password = ""
	}
	return dto
}

// normalizePublicHost accepts "db.example.com", "postgres://db.example.com:5432/x",
// "DB.Example.com." and returns the bare lowercase host ("" clears it).
func normalizePublicHost(raw string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(raw))
	if h == "" {
		return "", nil
	}
	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	}
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	if i := strings.LastIndex(h, "@"); i >= 0 {
		h = h[i+1:]
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.TrimSuffix(h, ".")
	if ip := net.ParseIP(strings.Trim(h, "[]")); ip != nil {
		return ip.String(), nil
	}
	if strings.HasPrefix(h, "*.") || !proxy.ValidHost(h) {
		return "", badRequest("invalid public host: use a domain such as db.example.com")
	}
	return h, nil
}

// validatePublicAccess normalizes db.PublicHost and checks that db.PublicPort is a
// valid port not already published by another database on the same server.
func (s *Server) validatePublicAccess(ctx context.Context, db *store.Database) error {
	if db.PublicPort < 0 || db.PublicPort > 65535 {
		return badRequest("invalid public port")
	}
	host, err := normalizePublicHost(db.PublicHost)
	if err != nil {
		return err
	}
	db.PublicHost = host
	if db.PublicPort == 0 {
		return nil
	}
	var name string
	err = s.db.QueryRowContext(ctx, "SELECT name FROM databases WHERE server_id = ? AND public_port = ? AND id != ? LIMIT 1",
		db.ServerID, db.PublicPort, db.ID).Scan(&name)
	switch {
	case err == nil:
		return errStatus(http.StatusConflict, "port %d is already used by database %q on this server", db.PublicPort, name)
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	return nil
}

func (s *Server) loadDB(r *http.Request) (store.Database, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.Database{}, err
	}
	return store.ByID[store.Database](r.Context(), s.db, "databases", id)
}

func (s *Server) listDatabases(w http.ResponseWriter, r *http.Request) error {
	dbs, err := store.Select[store.Database](r.Context(), s.db, "SELECT * FROM databases ORDER BY name")
	if err != nil {
		return err
	}
	visible := s.envVisibility(r.Context())
	return ok(w, filterBy(dbs, func(d store.Database) bool { return visible(d.EnvironmentID) }))
}

func (s *Server) createDatabase(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		store.Database
		Password string `json:"password"`
		Start    *bool  `json:"start"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	db := in.Database
	eng, found := templates.Engines[db.Engine]
	if !found {
		return badRequest("unknown engine %q", db.Engine)
	}
	if _, err := store.ByID[store.Server](r.Context(), s.db, "servers", db.ServerID); err != nil {
		return badRequest("server not found")
	}
	if err := s.requireEnv(r, db.EnvironmentID, permWrite); err != nil {
		return err
	}
	if _, err := store.ByID[store.Environment](r.Context(), s.db, "environments", db.EnvironmentID); err != nil {
		return badRequest("environment not found")
	}
	db.Name = strings.TrimSpace(db.Name)
	if db.Name == "" {
		db.Name = eng.ID
	}
	if db.Image == "" {
		db.Image = eng.DefaultImage
	}
	if eng.HasUser && db.Username == "" {
		db.Username = map[string]string{"postgres": "postgres", "mongodb": "root", "clickhouse": "default"}[eng.ID]
		if db.Username == "" {
			db.Username = "portdock"
		}
	}
	if eng.HasDatabase && db.DatabaseName == "" {
		db.DatabaseName = proxy.SafeName(db.Name)
		db.DatabaseName = strings.ReplaceAll(db.DatabaseName, "-", "_")
	}
	pass := in.Password
	if eng.ID == "mssql" {
		db.Username = "sa"
		if !mssqlNameRe.MatchString(db.DatabaseName) {
			return badRequest("the SQL Server database name may only contain letters, digits and underscores")
		}
		if pass == "" {
			for pass = secrets.RandomPassword(32); !mssqlPasswordOK(pass); pass = secrets.RandomPassword(32) {
			}
		} else if !mssqlPasswordOK(pass) {
			return badRequest("SQL Server needs a password of at least 8 characters with three of: uppercase, lowercase, digits, symbols")
		}
	}
	if pass == "" {
		pass = secrets.RandomPassword(32)
	}
	db.ID = 0
	if err := s.validatePublicAccess(r.Context(), &db); err != nil {
		return err
	}
	db.ID, db.UUID, db.Password, db.Status, db.CreatedAt = 0, secrets.RandomToken(8), s.box.Encrypt(pass), "stopped", store.Now()
	if _, err := store.Insert(r.Context(), s.db, "databases", &db); err != nil {
		return err
	}
	s.activity(r, "database.create", db.Name)
	if in.Start == nil || *in.Start {
		go func() { _ = s.engine.StartDatabase(context.Background(), db, true) }()
		db.Status = "starting"
	}
	return ok(w, s.dbOut(r.Context(), db))
}

var mssqlNameRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,128}$`)

// mssqlPasswordOK applies SQL Server's password policy for the sa login.
func mssqlPasswordOK(p string) bool {
	if len(p) < 8 {
		return false
	}
	var upper, lower, digit, other int
	for _, c := range p {
		switch {
		case c >= 'A' && c <= 'Z':
			upper = 1
		case c >= 'a' && c <= 'z':
			lower = 1
		case c >= '0' && c <= '9':
			digit = 1
		default:
			other = 1
		}
	}
	return upper+lower+digit+other >= 3
}

func (s *Server) getDatabase(w http.ResponseWriter, r *http.Request) error {
	db, err := s.loadDB(r)
	if err != nil {
		return err
	}
	dto := s.dbOut(r.Context(), db)
	if r.URL.Query().Get("dns") == "1" && db.PublicHost != "" && net.ParseIP(db.PublicHost) == nil {
		srv, _ := store.ByID[store.Server](r.Context(), s.db, "servers", db.ServerID)
		dto.DNSStatus, dto.DNSDetail = checkDNS(r.Context(), db.PublicHost, s.serverIPs(r.Context(), srv))
	}
	return ok(w, dto)
}

func (s *Server) updateDatabase(w http.ResponseWriter, r *http.Request) error {
	db, err := s.loadDB(r)
	if err != nil {
		return err
	}
	orig := db
	var in struct {
		*store.Database
		Password *string `json:"password"`
	}
	in.Database = &db
	if err := readJSON(r, &in); err != nil {
		return err
	}
	db.ID, db.UUID, db.Engine, db.ServerID, db.CreatedAt, db.Status, db.Password =
		orig.ID, orig.UUID, orig.Engine, orig.ServerID, orig.CreatedAt, orig.Status, orig.Password
	// Credentials are baked into the data volume at init time; changing them later has no effect.
	db.Username, db.DatabaseName = orig.Username, orig.DatabaseName
	if err := s.validatePublicAccess(r.Context(), &db); err != nil {
		return err
	}
	if err := store.Update(r.Context(), s.db, "databases", &db); err != nil {
		return err
	}
	s.activity(r, "database.update", db.Name)
	// A new public port only takes effect in a new container. A stopped database
	// picks it up on its next start (StartDatabase compares the published port).
	recreated := false
	if db.PublicPort != orig.PublicPort && orig.Status == "running" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if err := s.engine.StartDatabase(ctx, db, true); err != nil {
			return badRequest("settings saved, but the container could not be recreated with the new port: %v", err)
		}
		recreated = true
		db, _ = store.ByID[store.Database](r.Context(), s.db, "databases", db.ID)
	}
	dto := s.dbOut(r.Context(), db)
	dto.Recreated = recreated
	return ok(w, dto)
}

func (s *Server) deleteDatabase(w http.ResponseWriter, r *http.Request) error {
	db, err := s.loadDB(r)
	if err != nil {
		return err
	}
	volumes := r.URL.Query().Get("volumes") == "true"
	if err := s.engine.DeleteDatabase(r.Context(), db, volumes); err != nil {
		return badRequest("%v", err)
	}
	if err := store.Delete(r.Context(), s.db, "databases", db.ID); err != nil {
		return err
	}
	s.sched.Reload(r.Context())
	s.activity(r, "database.delete", db.Name)
	s.hub.Event("database", db.ID, "deleted")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) databaseAction(w http.ResponseWriter, r *http.Request) error {
	db, err := s.loadDB(r)
	if err != nil {
		return err
	}
	action := r.PathValue("action")
	switch action {
	case "start", "stop", "restart":
		err = s.engine.DatabaseAction(r.Context(), db, action)
	case "recreate":
		err = s.engine.StartDatabase(r.Context(), db, true)
	default:
		return errStatus(404, "unknown action")
	}
	if err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "database."+action, db.Name)
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) databaseLogs(w http.ResponseWriter, r *http.Request) error {
	db, err := s.loadDB(r)
	if err != nil {
		return err
	}
	x, _, err := s.pool.ForID(r.Context(), db.ServerID)
	if err != nil {
		return badRequest("%v", err)
	}
	return s.sseContainerLogs(w, r, x, db.UUID)
}

// ---- backups ----

func (s *Server) listBackups(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	bs, err := store.Select[store.Backup](r.Context(), s.db, "SELECT * FROM backups WHERE database_id = ? AND schedule != '' ORDER BY id", id)
	if err != nil {
		return err
	}
	return ok(w, bs)
}

func (s *Server) createBackup(w http.ResponseWriter, r *http.Request) error {
	db, err := s.loadDB(r)
	if err != nil {
		return err
	}
	b := store.Backup{Schedule: "0 3 * * *", Retention: 7, Enabled: true}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	if err := scheduler.Validate(b.Schedule); err != nil {
		return badRequest("invalid cron schedule: %v", err)
	}
	b.ID, b.DatabaseID, b.CreatedAt, b.LastRunAt = 0, db.ID, store.Now(), ""
	if _, err := store.Insert(r.Context(), s.db, "backups", &b); err != nil {
		return err
	}
	s.sched.Reload(r.Context())
	return ok(w, b)
}

func (s *Server) loadBackup(r *http.Request) (store.Backup, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.Backup{}, err
	}
	return store.ByID[store.Backup](r.Context(), s.db, "backups", id)
}

func (s *Server) updateBackup(w http.ResponseWriter, r *http.Request) error {
	b, err := s.loadBackup(r)
	if err != nil {
		return err
	}
	orig := b
	if err := readJSON(r, &b); err != nil {
		return err
	}
	b.ID, b.DatabaseID, b.CreatedAt, b.LastRunAt = orig.ID, orig.DatabaseID, orig.CreatedAt, orig.LastRunAt
	if err := scheduler.Validate(b.Schedule); err != nil {
		return badRequest("invalid cron schedule: %v", err)
	}
	if err := store.Update(r.Context(), s.db, "backups", &b); err != nil {
		return err
	}
	s.sched.Reload(r.Context())
	return ok(w, b)
}

func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) error {
	b, err := s.loadBackup(r)
	if err != nil {
		return err
	}
	if err := store.Delete(r.Context(), s.db, "backups", b.ID); err != nil {
		return err
	}
	s.sched.Reload(r.Context())
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) runBackup(w http.ResponseWriter, r *http.Request) error {
	b, err := s.loadBackup(r)
	if err != nil {
		return err
	}
	run, err := s.engine.EnqueueBackup(r.Context(), b, deploy.BackupScheduled)
	if err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "backup.run", fmt.Sprint(b.ID))
	return ok(w, run)
}

func (s *Server) listBackupRuns(w http.ResponseWriter, r *http.Request) error {
	b, err := s.loadBackup(r)
	if err != nil {
		return err
	}
	runs, err := store.Select[store.BackupRun](r.Context(), s.db, "SELECT * FROM backup_runs WHERE backup_id = ? ORDER BY id DESC LIMIT 100", b.ID)
	if err != nil {
		return err
	}
	return ok(w, runs)
}

func (s *Server) loadRun(r *http.Request) (store.BackupRun, store.Database, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.BackupRun{}, store.Database{}, err
	}
	run, err := store.ByID[store.BackupRun](r.Context(), s.db, "backup_runs", id)
	if err != nil {
		return run, store.Database{}, err
	}
	if run.Status != "success" || run.File == "" {
		return run, store.Database{}, badRequest("backup run has no file")
	}
	b, err := store.ByID[store.Backup](r.Context(), s.db, "backups", run.BackupID)
	if err != nil {
		return run, store.Database{}, err
	}
	db, err := store.ByID[store.Database](r.Context(), s.db, "databases", b.DatabaseID)
	return run, db, err
}

func (s *Server) downloadBackup(w http.ResponseWriter, r *http.Request) error {
	run, db, err := s.loadRun(r)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s"`, proxy.SafeName(db.Name), path.Base(run.File)))
	return s.engine.StreamBackup(r.Context(), db, run, w)
}

// restoreBackup queues the restore: it runs after the database's other backup
// jobs, and starts with a safety backup of the current data.
func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) error {
	run, db, err := s.loadRun(r)
	if err != nil {
		return err
	}
	if err := s.engine.EnqueueRestore(r.Context(), db, run); err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "backup.restore", db.Name+": "+path.Base(run.File))
	return ok(w, map[string]bool{"queued": true})
}

// backupRunsOf lists every backup of a database (scheduled, manual and safety
// backups), newest first, with where they are stored.
func (s *Server) backupRunsOf(w http.ResponseWriter, r *http.Request) error {
	db, err := s.loadDB(r)
	if err != nil {
		return err
	}
	runs, err := store.Select[store.BackupRun](r.Context(), s.db,
		"SELECT r.* FROM backup_runs r JOIN backups b ON b.id = r.backup_id WHERE b.database_id = ? ORDER BY r.id DESC LIMIT 200", db.ID)
	if err != nil {
		return err
	}
	if runs == nil {
		runs = []store.BackupRun{}
	}
	srv, _ := store.ByID[store.Server](r.Context(), s.db, "servers", db.ServerID)
	eng := templates.Engines[db.Engine]
	return ok(w, map[string]any{
		"runs": runs, "directory": s.engine.BackupDir(db), "server": srv.Name,
		"supported": deploy.BackupSupported(db), "restore": eng.Restore, "format": eng.BackupExt,
	})
}

// backupNow queues a manual backup of the database.
func (s *Server) backupNow(w http.ResponseWriter, r *http.Request) error {
	db, err := s.loadDB(r)
	if err != nil {
		return err
	}
	b, err := s.engine.ManualBackup(r.Context(), db.ID)
	if err != nil {
		return err
	}
	run, err := s.engine.EnqueueBackup(r.Context(), b, deploy.BackupManual)
	if err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "backup.manual", db.Name)
	return ok(w, run)
}

// backupAllDatabases queues a manual backup of every database.
func (s *Server) backupAllDatabases(w http.ResponseWriter, r *http.Request) error {
	n, err := s.engine.EnqueueAllBackups(r.Context())
	if err != nil {
		return err
	}
	s.activity(r, "backup.all", fmt.Sprintf("%d database(s)", n))
	return ok(w, map[string]int{"queued": n})
}

func (s *Server) deleteBackupRun(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	run, err := store.ByID[store.BackupRun](r.Context(), s.db, "backup_runs", id)
	if err != nil {
		return err
	}
	b, err := store.ByID[store.Backup](r.Context(), s.db, "backups", run.BackupID)
	if err != nil {
		return err
	}
	db, err := store.ByID[store.Database](r.Context(), s.db, "databases", b.DatabaseID)
	if err != nil {
		return err
	}
	if err := s.engine.DeleteBackupRun(r.Context(), db, run); err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "backup.delete", db.Name+": "+path.Base(run.File))
	return ok(w, map[string]bool{"ok": true})
}

// ---- services ----

func (s *Server) listServiceTemplates(w http.ResponseWriter, r *http.Request) error {
	return ok(w, templates.ServiceList())
}

type serviceDTO struct {
	store.Service
	Project     store.Project     `json:"project"`
	Environment store.Environment `json:"environment"`
	ServerName  string            `json:"server_name"`
	Access      Access            `json:"access"`
}

func (s *Server) svcOut(ctx context.Context, svc store.Service) serviceDTO {
	dto := serviceDTO{Service: svc}
	dto.Environment, _ = store.ByID[store.Environment](ctx, s.db, "environments", svc.EnvironmentID)
	dto.Project, _ = store.ByID[store.Project](ctx, s.db, "projects", dto.Environment.ProjectID)
	srv, _ := store.ByID[store.Server](ctx, s.db, "servers", svc.ServerID)
	dto.ServerName = srv.Name
	dto.Access = s.resourceAccess(ctx, svc.EnvironmentID)
	return dto
}

func (s *Server) loadService(r *http.Request) (store.Service, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.Service{}, err
	}
	return store.ByID[store.Service](r.Context(), s.db, "services", id)
}

func (s *Server) listServices(w http.ResponseWriter, r *http.Request) error {
	svcs, err := store.Select[store.Service](r.Context(), s.db, "SELECT * FROM services ORDER BY name")
	if err != nil {
		return err
	}
	visible := s.envVisibility(r.Context())
	svcs = filterBy(svcs, func(x store.Service) bool { return visible(x.EnvironmentID) })
	for i := range svcs {
		svcs[i].Compose = ""
	}
	return ok(w, svcs)
}

func (s *Server) createService(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		store.Service
		Deploy *bool `json:"deploy"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	svc := in.Service
	if _, err := store.ByID[store.Server](r.Context(), s.db, "servers", svc.ServerID); err != nil {
		return badRequest("server not found")
	}
	if err := s.requireEnv(r, svc.EnvironmentID, permWrite); err != nil {
		return err
	}
	if _, err := store.ByID[store.Environment](r.Context(), s.db, "environments", svc.EnvironmentID); err != nil {
		return badRequest("environment not found")
	}
	if svc.Template == "" {
		svc.Template = "custom"
	}
	if svc.Template != "custom" {
		tpl, found := templates.Services[svc.Template]
		if !found {
			return badRequest("unknown template %q", svc.Template)
		}
		if svc.Name == "" {
			svc.Name = tpl.Name
		}
		if svc.Description == "" {
			svc.Description = tpl.Description
		}
		svc.Compose = tpl.Compose
	}
	if strings.TrimSpace(svc.Compose) == "" {
		return badRequest("compose content is required")
	}
	if strings.TrimSpace(svc.Name) == "" {
		return badRequest("name is required")
	}
	svc.Compose = templates.Render(svc.Compose, firstDomain(svc.Domain))
	svc.ID, svc.UUID, svc.Status, svc.CreatedAt = 0, secrets.RandomToken(8), "stopped", store.Now()
	if _, err := store.Insert(r.Context(), s.db, "services", &svc); err != nil {
		return err
	}
	s.activity(r, "service.create", svc.Name)
	if in.Deploy == nil || *in.Deploy {
		go func() { _, _ = s.engine.DeployService(context.Background(), svc) }()
		svc.Status = "deploying"
	}
	return ok(w, s.svcOut(r.Context(), svc))
}

func firstDomain(d string) string {
	if l := store.SplitList(d); len(l) > 0 {
		host, _ := proxy.NormalizeDomain(l[0])
		return host
	}
	return ""
}

func (s *Server) getService(w http.ResponseWriter, r *http.Request) error {
	svc, err := s.loadService(r)
	if err != nil {
		return err
	}
	return ok(w, s.svcOut(r.Context(), svc))
}

func (s *Server) updateService(w http.ResponseWriter, r *http.Request) error {
	svc, err := s.loadService(r)
	if err != nil {
		return err
	}
	orig := svc
	if err := readJSON(r, &svc); err != nil {
		return err
	}
	svc.ID, svc.UUID, svc.ServerID, svc.CreatedAt, svc.Status, svc.Template = orig.ID, orig.UUID, orig.ServerID, orig.CreatedAt, orig.Status, orig.Template
	if strings.TrimSpace(svc.Compose) == "" {
		return badRequest("compose content is required")
	}
	if err := store.Update(r.Context(), s.db, "services", &svc); err != nil {
		return err
	}
	s.activity(r, "service.update", svc.Name)
	return ok(w, s.svcOut(r.Context(), svc))
}

func (s *Server) deleteService(w http.ResponseWriter, r *http.Request) error {
	svc, err := s.loadService(r)
	if err != nil {
		return err
	}
	if err := s.engine.DeleteService(r.Context(), svc, r.URL.Query().Get("volumes") == "true"); err != nil {
		return badRequest("%v", err)
	}
	if err := store.Delete(r.Context(), s.db, "services", svc.ID); err != nil {
		return err
	}
	_, _ = s.db.ExecContext(r.Context(), "DELETE FROM env_vars WHERE owner_type = 'service' AND owner_id = ?", svc.ID)
	s.activity(r, "service.delete", svc.Name)
	s.hub.Event("service", svc.ID, "deleted")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) deployService(w http.ResponseWriter, r *http.Request) error {
	svc, err := s.loadService(r)
	if err != nil {
		return err
	}
	go func() { _, _ = s.engine.DeployService(context.Background(), svc) }()
	s.activity(r, "service.deploy", svc.Name)
	return ok(w, map[string]bool{"started": true})
}

func (s *Server) serviceAction(w http.ResponseWriter, r *http.Request) error {
	svc, err := s.loadService(r)
	if err != nil {
		return err
	}
	action := r.PathValue("action")
	if action != "start" && action != "stop" && action != "restart" {
		return errStatus(404, "unknown action")
	}
	if err := s.engine.ServiceAction(r.Context(), svc, action); err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "service."+action, svc.Name)
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) serviceContainers(w http.ResponseWriter, r *http.Request) error {
	svc, err := s.loadService(r)
	if err != nil {
		return err
	}
	cs, err := s.engine.ServiceContainers(r.Context(), svc)
	if err != nil {
		return badRequest("%v", err)
	}
	return ok(w, cs)
}

func (s *Server) serviceStream(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	return s.sseTopic(w, r, deploy.ServiceTopic(id), "")
}
