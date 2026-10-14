package api

import (
	"net/http"

	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/store"
)

// Streaming replicas and failover of PostgreSQL databases (deploy/replicas.go).

type replicaOut struct {
	store.DBReplica
	ServerName  string `json:"server_name"`
	InternalURL string `json:"internal_url"`
	PublicURL   string `json:"public_url"`
}

func (s *Server) loadReplica(r *http.Request) (store.DBReplica, store.Database, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.DBReplica{}, store.Database{}, err
	}
	rep, err := store.ByID[store.DBReplica](r.Context(), s.db, "db_replicas", id)
	if err != nil {
		return rep, store.Database{}, err
	}
	db, err := store.ByID[store.Database](r.Context(), s.db, "databases", rep.DatabaseID)
	return rep, db, err
}

func (s *Server) listReplicas(w http.ResponseWriter, r *http.Request) error {
	db, err := s.loadDB(r)
	if err != nil {
		return err
	}
	rs, err := store.Select[store.DBReplica](r.Context(), s.db, "SELECT * FROM db_replicas WHERE database_id = ? ORDER BY id", db.ID)
	if err != nil {
		return err
	}
	access := s.resourceAccess(r.Context(), db.EnvironmentID)
	out := make([]replicaOut, 0, len(rs))
	for _, rep := range rs {
		srv, _ := store.ByID[store.Server](r.Context(), s.db, "servers", rep.ServerID)
		o := replicaOut{DBReplica: rep, ServerName: srv.Name}
		if access.CanWrite {
			o.InternalURL, o.PublicURL = s.engine.ReplicaURLs(db, rep, srv, s.dbPublicFallback(r.Context(), srv))
		}
		out = append(out, o)
	}
	return ok(w, map[string]any{"replicas": out, "supported": db.Engine == "postgres", "failover_auto": db.FailoverAuto, "failover_after": db.FailoverAfter})
}

func (s *Server) createReplica(w http.ResponseWriter, r *http.Request) error {
	db, err := s.loadDB(r)
	if err != nil {
		return err
	}
	var in struct {
		ServerID   int64 `json:"server_id"`
		PublicPort int   `json:"public_port"`
	}
	in.ServerID = db.ServerID
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.PublicPort != 0 {
		if in.PublicPort < 1024 || in.PublicPort > 65535 {
			return badRequest("the public port must be between 1024 and 65535")
		}
		if err := s.portFree(r, in.ServerID, in.PublicPort); err != nil {
			return err
		}
	}
	rep, err := s.engine.CreateReplica(r.Context(), db, in.ServerID, in.PublicPort)
	if err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "database.replica_create", db.Name)
	return ok(w, rep)
}

// portFree refuses a public port another database or replica of the server already publishes.
func (s *Server) portFree(r *http.Request, serverID int64, port int) error {
	var n int
	_ = s.db.QueryRowContext(r.Context(), `SELECT (SELECT COUNT(*) FROM databases WHERE server_id = ? AND public_port = ?) +
		(SELECT COUNT(*) FROM db_replicas WHERE server_id = ? AND public_port = ?)`, serverID, port, serverID, port).Scan(&n)
	if n > 0 {
		return badRequest("port %d is already published on that server", port)
	}
	return nil
}

func (s *Server) deleteReplica(w http.ResponseWriter, r *http.Request) error {
	rep, db, err := s.loadReplica(r)
	if err != nil {
		return err
	}
	if err := s.engine.DeleteReplica(r.Context(), db, rep); err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "database.replica_delete", db.Name)
	return ok(w, map[string]bool{"ok": true})
}

// promoteReplica runs a failover to the replica.
func (s *Server) promoteReplica(w http.ResponseWriter, r *http.Request) error {
	rep, db, err := s.loadReplica(r)
	if err != nil {
		return err
	}
	var in struct {
		Force bool `json:"force"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	u := auth.UserFrom(r.Context())
	if err := s.engine.PromoteReplica(r.Context(), db, rep, in.Force, u.Email); err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "database.failover", db.Name+" → replica "+rep.UUID)
	db, _ = store.ByID[store.Database](r.Context(), s.db, "databases", db.ID)
	return ok(w, s.dbOut(r.Context(), db))
}

func (s *Server) updateFailover(w http.ResponseWriter, r *http.Request) error {
	db, err := s.loadDB(r)
	if err != nil {
		return err
	}
	in := struct {
		Auto  bool `json:"auto"`
		After int  `json:"after"`
	}{db.FailoverAuto, db.FailoverAfter}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.After < 30 || in.After > 3600 {
		return badRequest("the delay must be between 30 and 3600 seconds")
	}
	if _, err := s.db.ExecContext(r.Context(), "UPDATE databases SET failover_auto = ?, failover_after = ? WHERE id = ?", in.Auto, in.After, db.ID); err != nil {
		return err
	}
	s.activity(r, "database.failover_settings", db.Name)
	return ok(w, map[string]any{"failover_auto": in.Auto, "failover_after": in.After})
}
