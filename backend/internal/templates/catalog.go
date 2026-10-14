package templates

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// A catalog is a document of templates that PortDock downloads from a URL
// (or that an administrator uploads), so the community can publish templates
// without a PortDock release:
//
//	portdock  {"version": 1, "name": "…", "templates": [{"slug": "n8n", "compose": "<template file>"}]}
//	          (JSON or YAML; `pdctl template bundle DIR` writes one), or a single template file
//	coolify   Coolify's templates/service-templates.json, converted on the fly

// Catalog formats.
const (
	FormatAuto     = "auto"
	FormatPortDock = "portdock"
	FormatCoolify  = "coolify"
)

// CatalogDoc is a catalog in the PortDock format.
type CatalogDoc struct {
	Version   int           `json:"version" yaml:"version"`
	Name      string        `json:"name,omitempty" yaml:"name,omitempty"`
	Templates []CatalogItem `json:"templates" yaml:"templates"`
}

// CatalogItem is one template of a catalog: its file, as written.
type CatalogItem struct {
	Slug    string `json:"slug" yaml:"slug"`
	Compose string `json:"compose" yaml:"compose"`
}

// Skipped is a template of a catalog that could not be used, and why.
type Skipped struct {
	Slug   string `json:"slug"`
	Reason string `json:"reason"`
}

// MaxCatalogSize bounds a downloaded or uploaded catalog.
const MaxCatalogSize = 16 << 20

// ParseCatalog reads a catalog. location (the catalog's URL) resolves
// relative logos and names a single-template file.
func ParseCatalog(format string, data []byte, location string) ([]ServiceTemplate, []Skipped, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if format == "" || format == FormatAuto {
		format = detect(data)
	}
	var out []ServiceTemplate
	var skipped []Skipped
	switch format {
	case FormatCoolify:
		var entries map[string]CoolifyEntry
		if err := json.Unmarshal(data, &entries); err != nil {
			return nil, nil, fmt.Errorf("not a Coolify service-templates.json: %w", err)
		}
		base := coolifyLogoBase(location)
		for slug, e := range entries {
			t, err := ConvertCoolifyEntry(slug, e, base)
			if err != nil {
				skipped = append(skipped, Skipped{slug, err.Error()})
				continue
			}
			out = append(out, t)
		}
	case FormatPortDock:
		var doc CatalogDoc
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, nil, fmt.Errorf("invalid catalog: %w", err)
		}
		if len(doc.Templates) == 0 && bytes.Contains(data, []byte("services:")) {
			// A single template file.
			slug := strings.TrimSuffix(strings.TrimSuffix(path.Base(location), ".yml"), ".yaml")
			if u, err := url.Parse(location); err == nil {
				slug = strings.TrimSuffix(strings.TrimSuffix(path.Base(u.Path), ".yml"), ".yaml")
			}
			doc.Templates = []CatalogItem{{Slug: slug, Compose: string(data)}}
		}
		if doc.Version > 1 {
			return nil, nil, fmt.Errorf("catalog version %d needs a newer PortDock", doc.Version)
		}
		for _, item := range doc.Templates {
			t, err := Parse(item.Slug, item.Compose)
			if err != nil {
				skipped = append(skipped, Skipped{item.Slug, err.Error()})
				continue
			}
			out = append(out, t)
		}
	default:
		return nil, nil, fmt.Errorf("unknown catalog format %q", format)
	}
	if len(out) == 0 && len(skipped) == 0 {
		return nil, nil, errNoTemplates
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].Slug < skipped[j].Slug })
	return out, skipped, nil
}

// detect tells a Coolify catalog (an object of entries with base64 compose)
// from a PortDock one.
func detect(data []byte) string {
	var probe map[string]json.RawMessage
	if json.Unmarshal(data, &probe) == nil {
		if _, ok := probe["templates"]; !ok {
			for _, v := range probe {
				var e CoolifyEntry
				if json.Unmarshal(v, &e) == nil && e.Compose != "" {
					return FormatCoolify
				}
				break
			}
		}
	}
	return FormatPortDock
}

// coolifyLogoBase is where the logos of Coolify's catalog live: public/ next
// to templates/ in the repository.
func coolifyLogoBase(location string) string {
	if i := strings.Index(location, "/templates/service-templates"); i > 0 {
		return location[:i] + "/public"
	}
	return ""
}

// Bundle writes a catalog of template files in the PortDock format.
func Bundle(name string, items []CatalogItem) ([]byte, error) {
	sort.Slice(items, func(i, j int) bool { return items[i].Slug < items[j].Slug })
	for i := range items {
		items[i].Compose = strings.ReplaceAll(items[i].Compose, "\r\n", "\n")
	}
	return json.MarshalIndent(CatalogDoc{Version: 1, Name: name, Templates: items}, "", "  ")
}

// Fetch downloads a catalog (through the corporate proxy, when one is set).
func Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("the catalog address must be an http(s) URL")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, application/yaml, text/plain")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download the catalog: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download the catalog: HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, MaxCatalogSize+1))
	if err != nil {
		return nil, fmt.Errorf("download the catalog: %w", err)
	}
	if len(b) > MaxCatalogSize {
		return nil, fmt.Errorf("the catalog is larger than %d MB", MaxCatalogSize>>20)
	}
	return b, nil
}
