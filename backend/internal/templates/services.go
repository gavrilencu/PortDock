package templates

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/portdock/portdock/internal/secrets"
	"go.yaml.in/yaml/v3"
)

// ServiceTemplate is a one-click service: a docker-compose file whose
// top-level `x-portdock` key describes it and names the public service:
//
//	x-portdock:
//	  name: n8n
//	  description: Workflow automation with 400+ integrations.
//	  category: Low-code & Automation
//	  website: https://n8n.io
//	  logo: https://example.com/n8n.svg   # optional
//	  tags: [automation, workflows]       # optional
//	  service: n8n                        # the service the domain points to (omit both
//	  port: 5678                          # for a background service): its port inside the container
//	services:
//	  …
//
// Placeholders rendered when the service is created:
//
//	${PORTDOCK_FQDN}               the service domain (host only)
//	${PORTDOCK_URL}                https://<domain>
//	${PORTDOCK_PASSWORD_<NAME>}    random 24-character password (the same for every use of NAME)
//	${PORTDOCK_PASSWORD64_<NAME>}  random 64-character password
//	${PORTDOCK_USER_<NAME>}        random 12-character lowercase user name
//	${PORTDOCK_SECRET_<NAME>}      random 64-character hex secret
//	${PORTDOCK_HEX32_<NAME>}       random hex of 32 (or HEX64, HEX128) characters
//	${PORTDOCK_RANDOM32_<NAME>}    random letters and digits, 32 (or RANDOM64, RANDOM128) characters
//	${PORTDOCK_BASE64_<NAME>}      base64 of 32 random bytes (or BASE64x64, BASE64x128 bytes)
type ServiceTemplate struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Website     string   `json:"website"`
	Logo        string   `json:"logo,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Service     string   `json:"service,omitempty"` // "" = no public address
	Port        int      `json:"port,omitempty"`
	// Key identifies the template to create a service from: its slug, or
	// catalog/slug for a template of a catalog.
	Key string `json:"key,omitempty"`
	// Source is the catalog the template comes from ("" = built in).
	Source string `json:"source,omitempty"`
	// HostAccess tells that the template only deploys with host access.
	HostAccess bool   `json:"host_access,omitempty"`
	Compose    string `json:"compose,omitempty"`
	// Notes are what a conversion changed or could not keep.
	Notes []string `json:"notes,omitempty"`
	// Required are the variables (${NAME:?…}) to set in the service's
	// environment before it can start.
	Required []string `json:"required,omitempty"`
}

var requiredVar = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*):?\?[^}]*\}`)

// required lists the variables a compose file needs a value for.
func required(compose string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range requiredVar.FindAllStringSubmatch(compose, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

var placeholder = regexp.MustCompile(`\$\{(?:PORTDOCK|SHIPYARD)_(FQDN|URL|PASSWORD64|PASSWORD|USER|SECRET|HEX32|HEX64|HEX128|RANDOM32|RANDOM64|RANDOM128|BASE64x64|BASE64x128|BASE64)(?:_([A-Z0-9_]+))?\}`)

// Render substitutes placeholders in a compose file.
func Render(compose, domain string) string {
	host := strings.TrimPrefix(strings.TrimPrefix(domain, "https://"), "http://")
	generated := map[string]string{}
	return placeholder.ReplaceAllStringFunc(compose, func(m string) string {
		parts := placeholder.FindStringSubmatch(m)
		kind, name := parts[1], parts[2]
		switch kind {
		case "FQDN":
			return host
		case "URL":
			if host == "" {
				return "http://localhost"
			}
			return "https://" + host
		}
		key := kind + "_" + name
		if v, ok := generated[key]; ok {
			return v
		}
		v := random(kind)
		generated[key] = v
		return v
	})
}

func random(kind string) string {
	switch kind {
	case "PASSWORD":
		return secrets.RandomPassword(24)
	case "PASSWORD64":
		return secrets.RandomPassword(64)
	case "USER":
		return strings.ToLower(secrets.RandomPassword(12))
	case "SECRET", "HEX64":
		return secrets.RandomToken(32)
	case "HEX32":
		return secrets.RandomToken(16)
	case "HEX128":
		return secrets.RandomToken(64)
	case "RANDOM32":
		return secrets.RandomPassword(32)
	case "RANDOM64":
		return secrets.RandomPassword(64)
	case "RANDOM128":
		return secrets.RandomPassword(128)
	}
	n := map[string]int{"BASE64": 32, "BASE64x64": 64, "BASE64x128": 128}[kind]
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return base64.StdEncoding.EncodeToString(buf)
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Parse reads a template: a compose file with its x-portdock description.
func Parse(slug, compose string) (ServiceTemplate, error) {
	var spec struct {
		X struct {
			Name        string   `yaml:"name"`
			Description string   `yaml:"description"`
			Category    string   `yaml:"category"`
			Website     string   `yaml:"website"`
			Logo        string   `yaml:"logo"`
			Tags        []string `yaml:"tags"`
			Service     string   `yaml:"service"`
			Port        int      `yaml:"port"`
		} `yaml:"x-portdock"`
		Services map[string]yaml.Node `yaml:"services"`
	}
	t := ServiceTemplate{Slug: slug, Compose: compose}
	if !slugRe.MatchString(slug) {
		return t, fmt.Errorf("template %q: the slug must be lowercase letters, digits and dashes", slug)
	}
	if err := yaml.Unmarshal([]byte(compose), &spec); err != nil {
		return t, fmt.Errorf("template %s: invalid YAML: %w", slug, err)
	}
	x := spec.X
	t.Name, t.Description, t.Category, t.Website, t.Logo, t.Tags, t.Service, t.Port =
		strings.TrimSpace(x.Name), strings.TrimSpace(x.Description), strings.TrimSpace(x.Category), x.Website, x.Logo, x.Tags, x.Service, x.Port
	switch {
	case len(spec.Services) == 0:
		return t, fmt.Errorf("template %s: no services", slug)
	case t.Name == "":
		return t, fmt.Errorf("template %s: x-portdock.name is required", slug)
	case (t.Service == "") != (t.Port == 0) || t.Port < 0 || t.Port > 65535:
		return t, fmt.Errorf("template %s: x-portdock.service and x-portdock.port name the public service (both, or neither for a service without a domain)", slug)
	}
	if _, ok := spec.Services[t.Service]; t.Service != "" && !ok {
		return t, fmt.Errorf("template %s: x-portdock.service %q is not a service of the file", slug, t.Service)
	}
	if t.Category == "" {
		t.Category = "Other"
	}
	t.Required = required(compose)
	return t, nil
}

//go:embed services/*.yml
var builtin embed.FS

// Services are the built-in templates, by slug (services/<slug>.yml).
var Services = func() map[string]ServiceTemplate {
	out := map[string]ServiceTemplate{}
	files, _ := builtin.ReadDir("services")
	for _, f := range files {
		b, err := builtin.ReadFile("services/" + f.Name())
		if err != nil {
			panic(err)
		}
		// The same text on every checkout (Git may convert line endings).
		t, err := Parse(strings.TrimSuffix(path.Base(f.Name()), ".yml"), strings.ReplaceAll(string(b), "\r\n", "\n"))
		if err != nil {
			panic("templates: " + err.Error())
		}
		out[t.Slug] = t
	}
	return out
}()

func ServiceList() []ServiceTemplate {
	out := make([]ServiceTemplate, 0, len(Services))
	for _, s := range Services {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
