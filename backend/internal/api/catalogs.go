package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/catalogs"
	"github.com/portdock/portdock/internal/store"
	"github.com/portdock/portdock/internal/templates"
)

// Service catalogs (Settings → Service catalogs): template catalogs that
// administrators add from a URL (the community catalog, Coolify's, a mirror
// on an internal server) or upload, on top of the built-in templates.

var catalogNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

type catalogOut struct {
	store.TemplateCatalog
	Skipped []templates.Skipped `json:"skipped"`
	Upload  bool                `json:"upload"` // uploaded content instead of a URL
}

func catalogOf(c store.TemplateCatalog) catalogOut {
	p := catalogs.Of(c)
	if p.Skipped == nil {
		p.Skipped = []templates.Skipped{}
	}
	return catalogOut{TemplateCatalog: c, Skipped: p.Skipped, Upload: c.URL == ""}
}

func (s *Server) listCatalogs(w http.ResponseWriter, r *http.Request) error {
	cs, err := store.Select[store.TemplateCatalog](r.Context(), s.db, "SELECT * FROM template_catalogs ORDER BY name")
	if err != nil {
		return err
	}
	out := make([]catalogOut, 0, len(cs))
	for _, c := range cs {
		out = append(out, catalogOf(c))
	}
	return ok(w, out)
}

type catalogInput struct {
	Name    *string `json:"name"`
	URL     *string `json:"url"`
	Format  *string `json:"format"`
	Content *string `json:"content"` // a catalog document, for a catalog without a URL
	Enabled *bool   `json:"enabled"`
}

// applyCatalog merges in into c and reports whether its source changed.
func applyCatalog(c *store.TemplateCatalog, in catalogInput) (bool, error) {
	changed := false
	if in.Name != nil {
		c.Name = strings.TrimSpace(*in.Name)
	}
	if in.URL != nil && strings.TrimSpace(*in.URL) != c.URL {
		c.URL, changed = strings.TrimSpace(*in.URL), true
		if c.URL != "" {
			c.Content = ""
		}
	}
	if in.Content != nil && *in.Content != "" {
		c.Content, c.URL, changed = *in.Content, "", true
	}
	if in.Format != nil && *in.Format != c.Format {
		c.Format, changed = *in.Format, true
	}
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	switch {
	case !catalogNameRe.MatchString(c.Name):
		return false, badRequest("the name must be lowercase letters, digits and dashes (it prefixes the templates' keys, e.g. community/n8n)")
	case c.Format != templates.FormatAuto && c.Format != templates.FormatPortDock && c.Format != templates.FormatCoolify:
		return false, badRequest("the format must be auto, portdock or coolify")
	case c.URL == "" && c.Content == "":
		return false, badRequest("give the catalog's URL, or upload its file")
	case len(c.Content) > templates.MaxCatalogSize:
		return false, badRequest("the catalog is larger than %d MB", templates.MaxCatalogSize>>20)
	}
	if c.URL != "" && !strings.HasPrefix(c.URL, "https://") && !strings.HasPrefix(c.URL, "http://") {
		return false, badRequest("the catalog address must be an http(s) URL")
	}
	return changed, nil
}

func (s *Server) createCatalog(w http.ResponseWriter, r *http.Request) error {
	var in catalogInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	c := store.TemplateCatalog{Format: templates.FormatAuto, Enabled: true, CreatedAt: store.Now()}
	if _, err := applyCatalog(&c, in); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err := catalogs.Load(ctx, &c); err != nil {
		return badRequest("%v", err)
	}
	if _, err := store.Insert(r.Context(), s.db, "template_catalogs", &c); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return badRequest("a catalog named %s already exists", c.Name)
		}
		return err
	}
	s.activity(r, "catalog.create", c.Name)
	return ok(w, catalogOf(c))
}

func (s *Server) updateCatalog(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, err := store.ByID[store.TemplateCatalog](r.Context(), s.db, "template_catalogs", id)
	if err != nil {
		return err
	}
	var in catalogInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	changed, err := applyCatalog(&c, in)
	if err != nil {
		return err
	}
	if changed {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		if err := catalogs.Load(ctx, &c); err != nil {
			return badRequest("%v", err)
		}
	}
	if err := store.Update(r.Context(), s.db, "template_catalogs", &c); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return badRequest("a catalog named %s already exists", c.Name)
		}
		return err
	}
	s.activity(r, "catalog.update", c.Name)
	return ok(w, catalogOf(c))
}

func (s *Server) refreshCatalog(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, err := store.ByID[store.TemplateCatalog](r.Context(), s.db, "template_catalogs", id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	c, err = catalogs.Refresh(ctx, s.db, c)
	if err != nil {
		return badRequest("%v", err)
	}
	return ok(w, catalogOf(c))
}

func (s *Server) deleteCatalog(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, err := store.ByID[store.TemplateCatalog](r.Context(), s.db, "template_catalogs", id)
	if err != nil {
		return err
	}
	if err := store.Delete(r.Context(), s.db, "template_catalogs", id); err != nil {
		return err
	}
	s.activity(r, "catalog.delete", c.Name)
	return ok(w, map[string]bool{"ok": true})
}

// listServiceTemplates lists the templates of the picker, without their
// compose files: a catalog can hold hundreds.
func (s *Server) listServiceTemplates(w http.ResponseWriter, r *http.Request) error {
	tpls := catalogs.List(r.Context(), s.db)
	for i := range tpls {
		tpls[i].Compose, tpls[i].Notes = "", nil
	}
	sort.SliceStable(tpls, func(i, j int) bool {
		if (tpls[i].Source == "") != (tpls[j].Source == "") {
			return tpls[i].Source == "" // built-in ones first
		}
		return strings.ToLower(tpls[i].Name) < strings.ToLower(tpls[j].Name)
	})
	return ok(w, tpls)
}

// getServiceTemplate returns one template with its compose file and what
// the linter says of it.
func (s *Server) getServiceTemplate(w http.ResponseWriter, r *http.Request) error {
	t, err := catalogs.Find(r.Context(), s.db, r.PathValue("key"))
	if errors.Is(err, catalogs.ErrNotFound) {
		return errStatus(404, "unknown template")
	}
	if err != nil {
		return err
	}
	issues := templates.Lint(t)
	if issues == nil {
		issues = []templates.Issue{}
	}
	return ok(w, map[string]any{"template": t, "issues": issues})
}
