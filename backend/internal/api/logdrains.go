package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/logdrain"
	"github.com/portdock/portdock/internal/store"
)

// Log drains: where the agents on the servers ship the containers' logs
// (Settings → Observability). Administrators only; credentials are stored
// encrypted and never returned.

type logDrainInput struct {
	Name       *string `json:"name"`
	Type       *string `json:"type"`
	URL        *string `json:"url"`
	Username   *string `json:"username"`
	Password   *string `json:"password"` // empty: unchanged
	Token      *string `json:"token"`    // empty: unchanged
	Tenant     *string `json:"tenant"`
	Index      *string `json:"index"`
	SourceType *string `json:"sourcetype"`
	// Headers replaces the HTTP headers; an empty value keeps a header's
	// current value.
	Headers      map[string]string `json:"headers"`
	SkipVerify   *bool             `json:"skip_verify"`
	ProjectIDs   []int64           `json:"project_ids"`
	IncludeProxy *bool             `json:"include_proxy"`
	Enabled      *bool             `json:"enabled"`
}

type logDrainOut struct {
	store.LogDrain
	Username    string   `json:"username"`
	PasswordSet bool     `json:"password_set"`
	TokenSet    bool     `json:"token_set"`
	Tenant      string   `json:"tenant"`
	Index       string   `json:"index"`
	SourceType  string   `json:"sourcetype"`
	HeaderNames []string `json:"header_names"`
	SkipVerify  bool     `json:"skip_verify"`
	ProjectIDs  []int64  `json:"project_ids"`
}

func (s *Server) logDrainOut(d store.LogDrain) logDrainOut {
	c, _ := s.drains.ConfigOf(d)
	out := logDrainOut{LogDrain: d, Username: c.Username, PasswordSet: c.Password != "", TokenSet: c.Token != "",
		Tenant: c.Tenant, Index: c.Index, SourceType: c.SourceType, SkipVerify: c.SkipVerify,
		HeaderNames: []string{}, ProjectIDs: d.Projects()}
	for k := range c.Headers {
		out.HeaderNames = append(out.HeaderNames, k)
	}
	slices.Sort(out.HeaderNames)
	if out.ProjectIDs == nil {
		out.ProjectIDs = []int64{}
	}
	return out
}

func (s *Server) listLogDrains(w http.ResponseWriter, r *http.Request) error {
	ds, err := store.Select[store.LogDrain](r.Context(), s.db, "SELECT * FROM log_drains ORDER BY name, id")
	if err != nil {
		return err
	}
	out := make([]logDrainOut, 0, len(ds))
	for _, d := range ds {
		out = append(out, s.logDrainOut(d))
	}
	return ok(w, out)
}

// applyLogDrain merges the input into d and its configuration, then checks them.
func (s *Server) applyLogDrain(r *http.Request, d *store.LogDrain, in logDrainInput) error {
	c, err := s.drains.ConfigOf(*d)
	if err != nil {
		return err
	}
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	set(&d.Name, in.Name)
	set(&d.Type, in.Type)
	set(&d.URL, in.URL)
	set(&c.Username, in.Username)
	set(&c.Tenant, in.Tenant)
	set(&c.Index, in.Index)
	set(&c.SourceType, in.SourceType)
	if in.Password != nil && *in.Password != "" {
		c.Password = *in.Password
	}
	if in.Token != nil && *in.Token != "" {
		c.Token = strings.TrimSpace(*in.Token)
	}
	if c.Username == "" {
		c.Password = ""
	}
	if in.Headers != nil {
		headers := map[string]string{}
		for k, v := range in.Headers {
			k = strings.TrimSpace(k)
			if v == "" {
				v = c.Headers[k]
			}
			headers[k] = v
		}
		c.Headers = headers
	}
	if in.SkipVerify != nil {
		c.SkipVerify = *in.SkipVerify
	}
	if in.IncludeProxy != nil {
		d.IncludeProxy = *in.IncludeProxy
	}
	if in.Enabled != nil {
		d.Enabled = *in.Enabled
	}
	if in.ProjectIDs != nil {
		ids := make([]string, 0, len(in.ProjectIDs))
		for _, id := range in.ProjectIDs {
			if _, err := store.ByID[store.Project](r.Context(), s.db, "projects", id); err != nil {
				return badRequest("project %d does not exist", id)
			}
			ids = append(ids, strconv.FormatInt(id, 10))
		}
		d.ProjectIDs = strings.Join(ids, ",")
	}
	if d.Name == "" {
		return badRequest("name is required")
	}
	if err := logdrain.Validate(d.Type, d.URL, c); err != nil {
		return badRequest("%v", err)
	}
	b, _ := json.Marshal(c)
	d.Config = s.box.Encrypt(string(b))
	return nil
}

func (s *Server) createLogDrain(w http.ResponseWriter, r *http.Request) error {
	var in logDrainInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	d := store.LogDrain{Enabled: true, CreatedAt: store.Now()}
	if err := s.applyLogDrain(r, &d, in); err != nil {
		return err
	}
	if _, err := store.Insert(r.Context(), s.db, "log_drains", &d); err != nil {
		return err
	}
	s.activity(r, "log_drain.create", d.Name+" ("+d.Type+")")
	s.drains.Trigger()
	return ok(w, s.logDrainOut(d))
}

func (s *Server) updateLogDrain(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	d, err := store.ByID[store.LogDrain](r.Context(), s.db, "log_drains", id)
	if err != nil {
		return err
	}
	var in logDrainInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.applyLogDrain(r, &d, in); err != nil {
		return err
	}
	if err := store.Update(r.Context(), s.db, "log_drains", &d); err != nil {
		return err
	}
	s.activity(r, "log_drain.update", d.Name)
	s.drains.Trigger()
	return ok(w, s.logDrainOut(d))
}

func (s *Server) deleteLogDrain(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	d, err := store.ByID[store.LogDrain](r.Context(), s.db, "log_drains", id)
	if err != nil {
		return err
	}
	if err := store.Delete(r.Context(), s.db, "log_drains", id); err != nil {
		return err
	}
	s.activity(r, "log_drain.delete", d.Name)
	s.drains.Trigger()
	return ok(w, map[string]bool{"ok": true})
}

// logDrainStatus reports the agent of each server and what each destination received.
func (s *Server) logDrainStatus(w http.ResponseWriter, r *http.Request) error {
	st, err := s.drains.Status(r.Context())
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"servers": st, "image": logdrain.Image})
}

// testLogDrains applies the configuration now and writes a test line on every server.
func (s *Server) testLogDrains(w http.ResponseWriter, r *http.Request) error {
	if err := s.drains.Reconcile(r.Context()); err != nil {
		return err
	}
	msg := "PortDock log drain test " + time.Now().UTC().Format(time.RFC3339)
	servers, err := s.drains.Test(r.Context(), msg)
	if err != nil {
		return badRequest("%v", err)
	}
	if len(servers) == 0 {
		return badRequest("no agent is running: enable a destination and check the servers below")
	}
	return ok(w, map[string]any{"message": msg, "servers": servers})
}
