package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/deploy"
	"github.com/portdock/portdock/internal/store"
)

// deployGate decides whether a user-triggered deployment of app needs approval.
func (s *Server) deployGate(ctx context.Context, app store.Application) (requestedBy int64, needs bool) {
	u := auth.UserFrom(ctx)
	env, _ := store.ByID[store.Environment](ctx, s.db, "environments", app.EnvironmentID)
	return u.ID, needsApproval(s.projectRole(ctx, u, env.ProjectID), env)
}

// automationNeedsApproval: git pushes and deploy hooks wait only where every deployment requires approval.
func (s *Server) automationNeedsApproval(ctx context.Context, app store.Application, pr int) bool {
	if pr > 0 {
		return false // previews never touch the environment's production containers
	}
	env, _ := store.ByID[store.Environment](ctx, s.db, "environments", app.EnvironmentID)
	return env.RequireApproval
}

func (s *Server) reviewDeployment(w http.ResponseWriter, r *http.Request, approve bool) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Comment string `json:"comment"`
	}
	if r.ContentLength > 0 {
		if err := readJSON(r, &in); err != nil {
			return err
		}
	}
	ctx := r.Context()
	d, err := store.DeploymentByID(ctx, s.db, id)
	if err != nil {
		return err
	}
	u := auth.UserFrom(ctx)
	// Four-eyes principle: requesters cannot approve their own deployment
	// (the owner may, as the break-glass account). Rejecting is always allowed.
	if approve && d.RequestedBy != 0 && d.RequestedBy == u.ID && u.Role != "owner" {
		return errStatus(403, "you requested this deployment; another maintainer must approve it")
	}
	nd, err := s.engine.Review(ctx, id, u.ID, approve, strings.TrimSpace(in.Comment))
	if errors.Is(err, deploy.ErrNotPending) {
		return badRequest("%v", err)
	}
	if err != nil {
		return err
	}
	app, _ := store.ByID[store.Application](ctx, s.db, "applications", d.ApplicationID)
	action := "deployment.reject"
	if approve {
		action = "deployment.approve"
	}
	s.activity(r, action, app.Name+" #"+r.PathValue("id"))
	return ok(w, nd)
}

func (s *Server) approveDeployment(w http.ResponseWriter, r *http.Request) error {
	return s.reviewDeployment(w, r, true)
}

func (s *Server) rejectDeployment(w http.ResponseWriter, r *http.Request) error {
	return s.reviewDeployment(w, r, false)
}
