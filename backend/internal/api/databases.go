package api

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/shipyard/shipyard/internal/deploy"
	"github.com/shipyard/shipyard/internal/proxy"
	"github.com/shipyard/shipyard/internal/scheduler"
	"github.com/shipyard/shipyard/internal/secrets"
	"github.com/shipyard/shipyard/internal/store"
	"github.com/shipyard/shipyard/internal/templates"
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
}

func (s *Server) dbOut(ctx context.Context, db store.Database) databaseDTO {
	srv, _ := store.ByID[store.Server](ctx, s.db, "servers", db.ServerID)
	internal, public := s.engine.DatabaseURLs(db, srv)
	eng := templates.Engines[db.Engine]
	dto := databaseDTO{Database: db, Password: s.box.MustDecrypt(db.Password), InternalURL: internal, PublicURL: public,
		Hostname: db.UUID, Port: eng.Port, EngineName: eng.Name, ServerName: srv.Name}
	dto.Environment, _ = store.ByID[store.Environment](ctx, s.db, "environments", db.EnvironmentID)
	dto.Project, _ = store.ByID[store.Project](ctx, s.db, "projects", dto.Environment.ProjectID)
	return dto
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
	return ok(w, dbs)
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
			db.Username = "shipyard"
		}
	}
	if eng.HasDatabase && db.DatabaseName == "" {
		db.DatabaseName = proxy.SafeName(db.Name)
		db.DatabaseName = strings.ReplaceAll(db.DatabaseName, "-", "_")
	}
	pass := in.Password
	if pass == "" {
		pass = secrets.RandomPassword(32)
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

func (s *Server) getDatabase(w http.ResponseWriter, r *http.Request) error {
	db, err := s.loadDB(r)
	if err != nil {
		return err
	}
	return ok(w, s.dbOut(r.Context(), db))
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
	if db.PublicPort < 0 || db.PublicPort > 65535 {
		return badRequest("invalid public port")
	}
	if err := store.Update(r.Context(), s.db, "databases", &db); err != nil {
		return err
	}
	s.activity(r, "database.update", db.Name)
	return ok(w, s.dbOut(r.Context(), db))
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
	bs, err := store.Select[store.Backup](r.Context(), s.db, "SELECT * FROM backups WHERE database_id = ? ORDER BY id", id)
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
	go func() { _, _ = s.engine.RunBackup(context.Background(), b) }()
	s.activity(r, "backup.run", fmt.Sprint(b.ID))
	return ok(w, map[string]bool{"started": true})
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

func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) error {
	run, db, err := s.loadRun(r)
	if err != nil {
		return err
	}
	if err := s.engine.RestoreBackup(r.Context(), db, run); err != nil {
		return badRequest("restore failed: %v", err)
	}
	s.activity(r, "backup.restore", db.Name)
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
}

func (s *Server) svcOut(ctx context.Context, svc store.Service) serviceDTO {
	dto := serviceDTO{Service: svc}
	dto.Environment, _ = store.ByID[store.Environment](ctx, s.db, "environments", svc.EnvironmentID)
	dto.Project, _ = store.ByID[store.Project](ctx, s.db, "projects", dto.Environment.ProjectID)
	srv, _ := store.ByID[store.Server](ctx, s.db, "servers", svc.ServerID)
	dto.ServerName = srv.Name
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
