package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/portdock/portdock/internal/deploy"
	"github.com/portdock/portdock/internal/store"
)

type versionOut struct {
	store.AppVersion
	Tag      string            `json:"tag"`
	Services map[string]string `json:"services"`
	Live     bool              `json:"live"`
	// ExpiresAt is when a previous version is removed (empty for the live one).
	ExpiresAt string `json:"expires_at,omitempty"`
}

// listVersions returns the deployable versions of an application, newest first,
// plus the deployment currently moving to another version, if any.
func (s *Server) listVersions(w http.ResponseWriter, r *http.Request) error {
	a, err := s.loadApp(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	vs, err := store.Select[store.AppVersion](ctx, s.db, "SELECT * FROM app_versions WHERE application_id = ? AND status = 'available' ORDER BY number DESC", a.ID)
	if err != nil {
		return err
	}
	live := s.engine.LiveVersionID(ctx, a.ID)
	out := make([]versionOut, 0, len(vs))
	for _, v := range vs {
		o := versionOut{AppVersion: v, Tag: fmt.Sprintf("v%d", v.Number), Services: deploy.VersionServices(v), Live: v.ID == live}
		if !o.Live {
			if created, err := time.Parse(time.RFC3339, v.CreatedAt); err == nil {
				o.ExpiresAt = created.Add(deploy.VersionMaxAge).UTC().Format(time.RFC3339)
			}
		}
		out = append(out, o)
	}
	res := map[string]any{"versions": out, "live_version_id": live, "kept": deploy.VersionsKept, "max_age_days": int(deploy.VersionMaxAge.Hours() / 24)}
	if d, err := store.Get[store.Deployment](ctx, s.db, `SELECT `+store.DeploymentCols+` FROM deployments WHERE application_id = ? AND pr_number = 0
		AND status IN ('queued', 'running', 'pending_approval') ORDER BY id DESC LIMIT 1`, a.ID); err == nil {
		res["pending"] = map[string]any{"deployment_id": d.ID, "version_id": d.VersionID, "status": d.Status, "trigger": d.Trigger}
	}
	return ok(w, res)
}

// deployVersion puts a saved version live again with the current variables.
func (s *Server) deployVersion(w http.ResponseWriter, r *http.Request) error {
	a, err := s.loadApp(r)
	if err != nil {
		return err
	}
	vid, err := pathID(r, "vid")
	if err != nil {
		return err
	}
	by, needs := s.deployGate(r.Context(), a)
	d, err := s.engine.Enqueue(r.Context(), a, deploy.Options{Trigger: deploy.TriggerPromote, VersionID: vid, RequestedBy: by, NeedsApproval: needs})
	if err != nil {
		return badRequest("%v", err)
	}
	if v, err := store.ByID[store.AppVersion](r.Context(), s.db, "app_versions", vid); err == nil {
		s.activity(r, "application.version_deploy", fmt.Sprintf("%s → v%d", a.Name, v.Number))
	}
	return ok(w, d)
}
