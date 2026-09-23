package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/shipyard/shipyard/internal/license"
	"github.com/shipyard/shipyard/internal/store"
)

// licenseState resolves the instance's current entitlement.
func (s *Server) licenseState(ctx context.Context) license.State {
	return license.Resolve(s.db.Setting(ctx, "license_key"), s.db.Setting(ctx, "license_trial_started"), time.Now())
}

// requireFeature blocks *new* use of a paid feature. Existing configuration
// keeps working after a downgrade so nobody is locked out.
func (s *Server) requireFeature(ctx context.Context, feature string) error {
	st := s.licenseState(ctx)
	if st.Has(feature) {
		return nil
	}
	plan := "Enterprise"
	if license.Plans["pro"].Features != nil {
		for _, f := range license.Plans["pro"].Features {
			if f == feature {
				plan = "Pro"
			}
		}
	}
	return errStatus(402, "%s requires the %s plan (current plan: %s). Start a trial or install a license in Settings → License.",
		license.FeatureNames[feature], plan, st.Plan.Name)
}

func (s *Server) usage(ctx context.Context) (servers, users int) {
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM servers").Scan(&servers)
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE disabled = 0").Scan(&users)
	return
}

func (s *Server) checkServerLimit(ctx context.Context) error {
	st := s.licenseState(ctx)
	if n, _ := s.usage(ctx); st.MaxServers > 0 && n >= st.MaxServers {
		return errStatus(402, "your %s plan includes %d servers; upgrade the license to add more", st.Plan.Name, st.MaxServers)
	}
	return nil
}

func (s *Server) checkUserLimit(ctx context.Context) error {
	st := s.licenseState(ctx)
	if _, n := s.usage(ctx); st.MaxUsers > 0 && n >= st.MaxUsers {
		return errStatus(402, "your %s plan includes %d active users; upgrade the license or disable unused accounts", st.Plan.Name, st.MaxUsers)
	}
	return nil
}

// licenseSummary is embedded in /api/auth/status for UI gating.
func (s *Server) licenseSummary(ctx context.Context) map[string]any {
	st := s.licenseState(ctx)
	return map[string]any{"plan": st.Plan.ID, "plan_name": st.Plan.Name, "source": st.Source, "features": st.Features,
		"message": st.Message, "in_grace": st.InGrace, "expired": st.Expired}
}

func (s *Server) getLicense(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	st := s.licenseState(ctx)
	servers, users := s.usage(ctx)
	return ok(w, map[string]any{
		"state":           st,
		"usage":           map[string]int{"servers": servers, "users": users},
		"trial_available": s.db.Setting(ctx, "license_trial_started") == "" && st.Source == "community",
		"plans":           []license.Plan{license.Plans["community"], license.Plans["pro"], license.Plans["enterprise"]},
		"feature_names":   license.FeatureNames,
	})
}

func (s *Server) installLicense(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Key string `json:"key"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	key := strings.Join(strings.Fields(in.Key), "")
	l, err := license.Parse(key)
	if err != nil {
		return badRequest("%v", err)
	}
	if exp, _ := time.Parse(time.RFC3339, l.ExpiresAt); time.Now().After(exp.Add(license.GracePeriod)) {
		return badRequest("this license expired on %s", exp.Format("2 Jan 2006"))
	}
	if err := s.db.SetSetting(r.Context(), "license_key", key); err != nil {
		return err
	}
	s.activity(r, "license.install", l.Customer+" · "+l.Plan+" · until "+l.ExpiresAt[:10])
	return s.getLicense(w, r)
}

func (s *Server) removeLicense(w http.ResponseWriter, r *http.Request) error {
	if err := s.db.SetSetting(r.Context(), "license_key", ""); err != nil {
		return err
	}
	s.activity(r, "license.remove", "")
	return s.getLicense(w, r)
}

func (s *Server) startTrial(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if s.db.Setting(ctx, "license_trial_started") != "" {
		return badRequest("the trial has already been used on this instance")
	}
	if s.db.Setting(ctx, "license_key") != "" {
		return badRequest("a license is already installed")
	}
	if err := s.db.SetSetting(ctx, "license_trial_started", store.Now()); err != nil {
		return err
	}
	s.activity(r, "license.trial_start", "Enterprise, 30 days")
	return s.getLicense(w, r)
}
