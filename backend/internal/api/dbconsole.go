package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/deploy"
	"github.com/portdock/portdock/internal/sqlconsole"
	"github.com/portdock/portdock/internal/store"
)

// SQL console: members run reading statements; organization admins (with
// write access to the database) may turn on writes.

func (s *Server) consoleCanWrite(r *http.Request, db store.Database) bool {
	if !auth.UserFrom(r.Context()).IsAdmin() {
		return false
	}
	_, _, err := s.authorizeEnv(r.Context(), db.EnvironmentID, permWrite)
	return err == nil
}

func (s *Server) databaseConsole(w http.ResponseWriter, r *http.Request) error {
	db, err := s.loadDB(r)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{
		"supported": sqlconsole.Supported(db.Engine), "can_write": s.consoleCanWrite(r, db),
		"max_rows": sqlconsole.MaxRows, "timeout": sqlconsole.Timeout,
	})
}

func (s *Server) databaseTables(w http.ResponseWriter, r *http.Request) error {
	db, err := s.loadDB(r)
	if err != nil {
		return err
	}
	res, err := s.engine.DatabaseTables(r.Context(), db)
	var iq *deploy.InvalidQuery
	if errors.As(err, &iq) {
		return badRequest("%v", err)
	}
	if err != nil {
		return errStatus(502, "%v", err)
	}
	return ok(w, res)
}

func (s *Server) runDatabaseQuery(w http.ResponseWriter, r *http.Request) error {
	db, err := s.loadDB(r)
	if err != nil {
		return err
	}
	var in struct {
		Query string `json:"query"`
		Write bool   `json:"write"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if len(in.Query) > 256<<10 {
		return badRequest("the script is too long")
	}
	if in.Write && !s.consoleCanWrite(r, db) {
		return errStatus(403, "only organization admins with write access to the database can run statements that write")
	}
	out, err := s.engine.RunQuery(r.Context(), db, in.Query, !in.Write)
	var iq *deploy.InvalidQuery
	if errors.As(err, &iq) {
		return badRequest("%v", err)
	}
	if err != nil {
		return err
	}
	mode := "read-only"
	if in.Write {
		mode = "write"
	}
	q := strings.Join(strings.Fields(in.Query), " ")
	if len(q) > 300 {
		q = q[:300] + "…"
	}
	s.activity(r, "database.query", db.Name+" ("+mode+"): "+q)
	return ok(w, out)
}
