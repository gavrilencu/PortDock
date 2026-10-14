// Package catalogs keeps the service template catalogs that administrators
// add (Settings → Service catalogs): downloaded from a URL, or uploaded, and
// parsed once so the template picker does not convert them on every request.
package catalogs

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/store"
	"github.com/portdock/portdock/internal/templates"
)

// Parsed is what a catalog holds once parsed.
type Parsed struct {
	Templates []templates.ServiceTemplate `json:"templates"`
	Skipped   []templates.Skipped         `json:"skipped"`
}

// Load fetches (or reads the uploaded content of) a catalog and parses it
// into c, without saving.
func Load(ctx context.Context, c *store.TemplateCatalog) error {
	data := []byte(c.Content)
	if c.URL != "" {
		var err error
		if data, err = templates.Fetch(ctx, c.URL); err != nil {
			return err
		}
	}
	tpls, skipped, err := templates.ParseCatalog(c.Format, data, c.URL)
	if err != nil {
		return err
	}
	for i := range tpls {
		tpls[i].Source = c.Name
		for _, is := range templates.Lint(tpls[i]) {
			if is.Error && strings.HasPrefix(is.Text, "needs host access") {
				tpls[i].HostAccess = true
			}
		}
	}
	b, _ := json.Marshal(Parsed{Templates: tpls, Skipped: skipped})
	c.Templates, c.Count, c.FetchedAt, c.FetchError = string(b), len(tpls), store.Now(), ""
	return nil
}

// Refresh reloads a saved catalog. A failure keeps the templates it had.
func Refresh(ctx context.Context, db *store.DB, c store.TemplateCatalog) (store.TemplateCatalog, error) {
	err := Load(ctx, &c)
	if err != nil {
		c.FetchError = err.Error()
	}
	if uerr := store.Update(ctx, db, "template_catalogs", &c); uerr != nil && err == nil {
		err = uerr
	}
	return c, err
}

// RefreshAll reloads the enabled catalogs that come from a URL.
func RefreshAll(ctx context.Context, db *store.DB) {
	cs, err := store.Select[store.TemplateCatalog](ctx, db, "SELECT * FROM template_catalogs WHERE enabled = 1 AND url != ''")
	if err != nil {
		return
	}
	for _, c := range cs {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		if _, err := Refresh(cctx, db, c); err != nil {
			slog.Warn("refresh a service catalog", "catalog", c.Name, "err", err)
		}
		cancel()
	}
}

// Of returns the parsed content of a catalog.
func Of(c store.TemplateCatalog) Parsed {
	var p Parsed
	_ = json.Unmarshal([]byte(c.Templates), &p)
	return p
}

// List returns the built-in templates and those of the enabled catalogs,
// keyed by slug or catalog/slug.
func List(ctx context.Context, db *store.DB) []templates.ServiceTemplate {
	out := templates.ServiceList()
	for i := range out {
		out[i].Key = out[i].Slug
	}
	cs, _ := store.Select[store.TemplateCatalog](ctx, db, "SELECT * FROM template_catalogs WHERE enabled = 1 ORDER BY name")
	for _, c := range cs {
		for _, t := range Of(c).Templates {
			t.Key = c.Name + "/" + t.Slug
			out = append(out, t)
		}
	}
	return out
}

// ErrNotFound reports a template key that matches no template.
var ErrNotFound = errors.New("unknown template")

// Find returns a template by key: a built-in slug or catalog/slug.
func Find(ctx context.Context, db *store.DB, key string) (templates.ServiceTemplate, error) {
	name, slug, inCatalog := strings.Cut(key, "/")
	if !inCatalog {
		t, ok := templates.Services[key]
		if !ok {
			return t, ErrNotFound
		}
		t.Key = key
		return t, nil
	}
	c, err := store.Get[store.TemplateCatalog](ctx, db, "SELECT * FROM template_catalogs WHERE name = ? AND enabled = 1", name)
	if err != nil {
		return templates.ServiceTemplate{}, ErrNotFound
	}
	for _, t := range Of(c).Templates {
		if t.Slug == slug {
			t.Key = key
			return t, nil
		}
	}
	return templates.ServiceTemplate{}, ErrNotFound
}
