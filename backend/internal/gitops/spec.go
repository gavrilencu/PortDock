// Package gitops describes portdock.yaml, a file that declares applications
// as code: kept in the repository, reviewed in pull requests and applied with
// `pdctl apply` (or POST /api/gitops/apply) so the configuration can be
// recreated at any time.
package gitops

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Version is the format version this PortDock reads.
const Version = 1

// File is a portdock.yaml.
type File struct {
	Version     int    `yaml:"version" json:"version"`
	Project     string `yaml:"project" json:"project"`
	Environment string `yaml:"environment,omitempty" json:"environment,omitempty"` // default: production
	Server      string `yaml:"server,omitempty" json:"server,omitempty"`           // default server of the applications
	// Prune deletes what the file no longer lists: domains, non-secret
	// variables, volumes (keeping their data) and jobs. Off by default.
	Prune        bool  `yaml:"prune,omitempty" json:"prune,omitempty"`
	Applications []App `yaml:"applications" json:"applications"`
}

// App declares one application.
type App struct {
	Name        string      `yaml:"name" json:"name"`
	Description string      `yaml:"description,omitempty" json:"description,omitempty"`
	Server      string      `yaml:"server,omitempty" json:"server,omitempty"`
	Git         *Git        `yaml:"git,omitempty" json:"git,omitempty"`
	Image       string      `yaml:"image,omitempty" json:"image,omitempty"`
	Build       *Build      `yaml:"build,omitempty" json:"build,omitempty"`
	Port        int         `yaml:"port,omitempty" json:"port,omitempty"`
	Replicas    int         `yaml:"replicas,omitempty" json:"replicas,omitempty"`
	Resources   *Resources  `yaml:"resources,omitempty" json:"resources,omitempty"`
	HealthCheck *Health     `yaml:"health_check,omitempty" json:"health_check,omitempty"`
	Deploy      *Deploy     `yaml:"deploy,omitempty" json:"deploy,omitempty"`
	Domains     []Domain    `yaml:"domains,omitempty" json:"domains,omitempty"`
	Env         Env         `yaml:"env,omitempty" json:"env,omitempty"`
	Secrets     []string    `yaml:"secrets,omitempty" json:"secrets,omitempty"` // variables set in PortDock, never in the file
	// SecretRefs reads variables from an external secret store: KEY: store:path#key.
	SecretRefs map[string]string `yaml:"secret_refs,omitempty" json:"secret_refs,omitempty"`
	Volumes     []Volume    `yaml:"volumes,omitempty" json:"volumes,omitempty"`
	Jobs        []Job       `yaml:"jobs,omitempty" json:"jobs,omitempty"`
	Protection  *Protection `yaml:"protection,omitempty" json:"protection,omitempty"`
}

type Git struct {
	Repository string `yaml:"repository" json:"repository"`
	Branch     string `yaml:"branch,omitempty" json:"branch,omitempty"`
	Commit     string `yaml:"commit,omitempty" json:"commit,omitempty"`
}

type Build struct {
	Pack             string `yaml:"pack,omitempty" json:"pack,omitempty"` // auto | dockerfile | compose | node | … | nixpacks
	BaseDirectory    string `yaml:"base_directory,omitempty" json:"base_directory,omitempty"`
	Dockerfile       string `yaml:"dockerfile,omitempty" json:"dockerfile,omitempty"`
	ComposeFile      string `yaml:"compose_file,omitempty" json:"compose_file,omitempty"`
	ComposeService   string `yaml:"compose_service,omitempty" json:"compose_service,omitempty"`
	Install          string `yaml:"install,omitempty" json:"install,omitempty"`
	Command          string `yaml:"command,omitempty" json:"command,omitempty"`
	Start            string `yaml:"start,omitempty" json:"start,omitempty"`
	PublishDirectory string `yaml:"publish_directory,omitempty" json:"publish_directory,omitempty"`
}

type Resources struct {
	CPU    string `yaml:"cpu,omitempty" json:"cpu,omitempty"`
	Memory string `yaml:"memory,omitempty" json:"memory,omitempty"`
}

type Health struct {
	Path    string `yaml:"path,omitempty" json:"path,omitempty"`
	Timeout int    `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

type Deploy struct {
	Auto          *bool    `yaml:"auto,omitempty" json:"auto,omitempty"`
	PreCommand    string   `yaml:"pre_command,omitempty" json:"pre_command,omitempty"`
	PostCommand   string   `yaml:"post_command,omitempty" json:"post_command,omitempty"`
	WatchPaths    []string `yaml:"watch_paths,omitempty" json:"watch_paths,omitempty"`
	Previews      *bool    `yaml:"previews,omitempty" json:"previews,omitempty"`
	PreviewDomain string   `yaml:"preview_domain,omitempty" json:"preview_domain,omitempty"`
}

type Domain struct {
	Domain      string `yaml:"domain" json:"domain"` // https://app.example.com/path
	Port        int    `yaml:"port,omitempty" json:"port,omitempty"`
	Service     string `yaml:"service,omitempty" json:"service,omitempty"`
	WWWRedirect string `yaml:"www_redirect,omitempty" json:"www_redirect,omitempty"`
	Indexing    *bool  `yaml:"indexing,omitempty" json:"indexing,omitempty"`
	Certificate string `yaml:"certificate,omitempty" json:"certificate,omitempty"` // auto | dns | custom
}

// Env is the plain (non-secret) variables of an application.
type Env map[string]string

type Volume struct {
	Name     string `yaml:"name" json:"name"`
	Path     string `yaml:"path" json:"path"`
	ReadOnly bool   `yaml:"read_only,omitempty" json:"read_only,omitempty"`
	HostPath string `yaml:"host_path,omitempty" json:"host_path,omitempty"`
}

type Job struct {
	Name     string `yaml:"name" json:"name"`
	Schedule string `yaml:"schedule" json:"schedule"`
	Command  string `yaml:"command" json:"command"`
	Service  string `yaml:"service,omitempty" json:"service,omitempty"`
	Timeout  int    `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Enabled  *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}

type Protection struct {
	Auth      string     `yaml:"auth,omitempty" json:"auth,omitempty"` // "" | password | sso
	SSOAccess string     `yaml:"sso_access,omitempty" json:"sso_access,omitempty"`
	SSOEmails []string   `yaml:"sso_emails,omitempty" json:"sso_emails,omitempty"`
	Users     []string   `yaml:"users,omitempty" json:"users,omitempty"` // password users, set in PortDock
	IPAllow   []string   `yaml:"ip_allow,omitempty" json:"ip_allow,omitempty"`
	IPDepth   int        `yaml:"ip_depth,omitempty" json:"ip_depth,omitempty"`
	RateLimit *RateLimit `yaml:"rate_limit,omitempty" json:"rate_limit,omitempty"`
}

type RateLimit struct {
	Average int `yaml:"average" json:"average"`
	Burst   int `yaml:"burst,omitempty" json:"burst,omitempty"`
}

// Parse reads a portdock.yaml (or the same document in JSON). Unknown fields
// are errors, so a typo cannot be silently ignored.
func Parse(b []byte) (File, error) {
	var f File
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return f, fmt.Errorf("portdock.yaml: %w", err)
	}
	return f, f.Validate()
}

// Validate checks the structure; the values are checked by PortDock as for
// any change made in the interface.
func (f *File) Validate() error {
	if f.Version != Version {
		return fmt.Errorf("portdock.yaml: version must be %d", Version)
	}
	if strings.TrimSpace(f.Project) == "" {
		return errors.New("portdock.yaml: project is required")
	}
	if f.Environment == "" {
		f.Environment = "production"
	}
	seen := map[string]bool{}
	for i, a := range f.Applications {
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("portdock.yaml: applications[%d] has no name", i)
		}
		if seen[a.Name] {
			return fmt.Errorf("portdock.yaml: application %s is declared twice", a.Name)
		}
		seen[a.Name] = true
		if (a.Git == nil) == (a.Image == "") {
			return fmt.Errorf("portdock.yaml: application %s needs either git or image", a.Name)
		}
		for _, k := range a.Secrets {
			if _, dup := a.Env[k]; dup {
				return fmt.Errorf("portdock.yaml: %s of %s is both a variable and a secret", k, a.Name)
			}
		}
		for k, ref := range a.SecretRefs {
			if _, dup := a.Env[k]; dup || slices.Contains(a.Secrets, k) {
				return fmt.Errorf("portdock.yaml: %s of %s is listed twice (secret_refs and env or secrets)", k, a.Name)
			}
			if _, _, _, err := ParseRef(ref); err != nil {
				return fmt.Errorf("portdock.yaml: secret_refs %s of %s: %w", k, a.Name, err)
			}
		}
	}
	return nil
}

// Marshal renders f as YAML.
func Marshal(f File) []byte {
	var buf bytes.Buffer
	buf.WriteString("# PortDock configuration as code: pdctl apply -f portdock.yaml\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	_ = enc.Encode(f)
	return buf.Bytes()
}

// ParseRef splits a secret store reference "store:path#key" (the key is
// optional for a whole Key Vault secret).
func ParseRef(ref string) (storeName, path, key string, err error) {
	storeName, rest, found := strings.Cut(strings.TrimSpace(ref), ":")
	path, key, _ = strings.Cut(rest, "#")
	path = strings.Trim(strings.TrimSpace(path), "/")
	if !found || storeName == "" || path == "" {
		return "", "", "", errors.New("expected store:path#key, e.g. vault-prod:shop/db#password")
	}
	return storeName, path, strings.TrimSpace(key), nil
}

// Ref renders a secret store reference.
func Ref(storeName, path, key string) string {
	if key == "" {
		return storeName + ":" + path
	}
	return storeName + ":" + path + "#" + key
}
