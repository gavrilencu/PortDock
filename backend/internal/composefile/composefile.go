// Package composefile reads the services of a Docker Compose file.
package composefile

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Service is a service of a compose file with the container port it most
// likely serves HTTP on (0 when it can't be told).
type Service struct {
	Name string `json:"name"`
	Port int    `json:"port"`
	// Env is the service's `environment:` from the compose file (values as written,
	// ${VAR} references included).
	Env map[string]string `json:"env,omitempty"`
	// Build is set when the service is built from source (it has `build:`).
	Build bool `json:"build,omitempty"`
}

// Services lists the services of a compose file, sorted by name.
func Services(raw string) ([]Service, error) {
	var spec struct {
		Services map[string]yaml.Node `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(raw), &spec); err != nil {
		return nil, fmt.Errorf("invalid compose file: %w", err)
	}
	out := make([]Service, 0, len(spec.Services))
	for name, node := range spec.Services {
		var b struct {
			Build yaml.Node `yaml:"build"`
		}
		_ = node.Decode(&b)
		out = append(out, Service{Name: name, Port: ServicePort(node), Env: serviceEnv(node), Build: b.Build.Kind != 0})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ServicePort returns the container port of a service from `ports:` or `expose:`,
// when exactly one is declared.
func ServicePort(node yaml.Node) int {
	var svc struct {
		Ports  []yaml.Node `yaml:"ports"`
		Expose []yaml.Node `yaml:"expose"`
	}
	if node.Decode(&svc) != nil {
		return 0
	}
	seen := map[int]bool{}
	for _, p := range svc.Ports {
		if p.Kind == yaml.MappingNode {
			var long struct {
				Target int `yaml:"target"`
			}
			if p.Decode(&long) == nil && long.Target > 0 {
				seen[long.Target] = true
			}
			continue
		}
		// "8080:80", "127.0.0.1:8080:80/tcp", "80", "3000-3001" (ranges are skipped)
		s := strings.Trim(p.Value, `"'`)
		s, _, _ = strings.Cut(s, "/")
		parts := strings.Split(s, ":")
		if n, err := strconv.Atoi(parts[len(parts)-1]); err == nil && n > 0 {
			seen[n] = true
		}
	}
	for _, e := range svc.Expose {
		s, _, _ := strings.Cut(strings.Trim(e.Value, `"'`), "/")
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			seen[n] = true
		}
	}
	if len(seen) != 1 {
		return 0
	}
	for p := range seen {
		return p
	}
	return 0
}

// serviceEnv reads `environment:` in its map or list ("KEY=value") form.
func serviceEnv(node yaml.Node) map[string]string {
	var svc struct {
		Environment yaml.Node `yaml:"environment"`
	}
	if node.Decode(&svc) != nil {
		return nil
	}
	env := map[string]string{}
	switch n := svc.Environment; n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			v := n.Content[i+1]
			if v.Tag == "!!null" {
				env[n.Content[i].Value] = ""
			} else {
				env[n.Content[i].Value] = v.Value
			}
		}
	case yaml.SequenceNode:
		for _, item := range n.Content {
			k, v, _ := strings.Cut(item.Value, "=")
			env[k] = v
		}
	}
	if len(env) == 0 {
		return nil
	}
	return env
}
