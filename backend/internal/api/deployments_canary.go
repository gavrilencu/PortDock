package api

import "net/http"

// canaryDecision promotes or rolls back the canary of a running deployment.
func (s *Server) canaryDecision(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Action string `json:"action"` // promote | rollback
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.engine.CanaryDecision(id, in.Action); err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "deployment.canary_"+in.Action, "")
	return ok(w, map[string]bool{"ok": true})
}
