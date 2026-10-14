package templates

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/portdock/portdock/internal/composefile"
	"go.yaml.in/yaml/v3"
)

// Issue is a problem of a template: an error keeps it out of the built-in
// catalog, a warning is advice.
type Issue struct {
	Error bool   `json:"error"`
	Text  string `json:"text"`
}

var anyPlaceholder = regexp.MustCompile(`\$\{(?:PORTDOCK|SHIPYARD)_[A-Za-z0-9_]*\}`)

// Lint checks a template beyond Parse: it must deploy without host access,
// use only known placeholders and leave the container names and host ports
// to PortDock.
func Lint(t ServiceTemplate) []Issue {
	var out []Issue
	errf := func(f string, a ...any) { out = append(out, Issue{true, fmt.Sprintf(f, a...)}) }
	warnf := func(f string, a ...any) { out = append(out, Issue{false, fmt.Sprintf(f, a...)}) }
	if t.Description == "" {
		errf("x-portdock.description is empty")
	}
	for _, link := range []struct{ name, v string }{{"website", t.Website}, {"logo", t.Logo}} {
		if link.v == "" {
			continue
		}
		if u, err := url.Parse(link.v); err != nil || u.Scheme != "https" || u.Host == "" {
			errf("x-portdock.%s must be an https URL", link.name)
		}
	}
	for _, m := range anyPlaceholder.FindAllString(t.Compose, -1) {
		if !placeholder.MatchString(m) {
			errf("unknown placeholder %s", m)
		}
	}
	rendered := Render(t.Compose, "app.example.com")
	if err := composefile.Check(rendered, false); err != nil {
		errf("needs host access: %v", err)
	}
	var spec struct {
		Services map[string]struct {
			Image         string    `yaml:"image"`
			Build         yaml.Node `yaml:"build"`
			ContainerName string    `yaml:"container_name"`
			Ports         []any     `yaml:"ports"`
			Restart       string    `yaml:"restart"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &spec); err != nil {
		errf("invalid YAML: %v", err)
		return out
	}
	names := make([]string, 0, len(spec.Services))
	for n := range spec.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s := spec.Services[n]
		switch {
		case s.ContainerName != "":
			errf("service %s sets container_name: two copies of the service would collide", n)
		case s.Image == "" && s.Build.Kind == 0:
			errf("service %s has no image", n)
		}
		if len(s.Ports) > 0 {
			warnf("service %s publishes ports on the server: two copies of the service would collide (HTTP goes through the proxy)", n)
		}
		if s.Image != "" {
			ref := s.Image[strings.LastIndex(s.Image, "/")+1:]
			if !strings.Contains(ref, ":") && !strings.Contains(ref, "@") || strings.HasSuffix(ref, ":latest") {
				warnf("service %s uses %s: pin a version so a redeploy gets the same software", n, s.Image)
			}
		}
		if s.Restart == "" {
			warnf("service %s has no restart policy (restart: unless-stopped)", n)
		}
	}
	return out
}
