// Package migrate reads the applications, databases and services of another
// platform (Coolify, Heroku, Dokku) into a plan that PortDock can create:
// projects and environments, applications with their source, build, port,
// domains, variables, volumes, replicas and jobs, empty databases of the same
// engines, and one-click services as compose files. Data is not copied.
package migrate

import (
	"context"
	"regexp"
	"sort"
	"strings"
)

// Sources.
const (
	Coolify = "coolify"
	Heroku  = "heroku"
	Dokku   = "dokku"
)

// Plan is what an import would create.
type Plan struct {
	Source    string     `json:"source"`
	Apps      []App      `json:"apps"`
	Databases []Database `json:"databases"`
	Services  []Service  `json:"services"`
	Warnings  []string   `json:"warnings"`
}

// Var is an environment variable.
type Var struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Build   bool   `json:"is_build"`
	Preview bool   `json:"is_preview"`
	Secret  bool   `json:"is_secret"`
}

// Volume is persistent storage of an application.
type Volume struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Job is a scheduled command of an application.
type Job struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
}

// App is an application to create.
type App struct {
	Ref         string `json:"ref"` // its id in the source
	Name        string `json:"name"`
	Project     string `json:"project"`
	Environment string `json:"environment"`
	SourceType  string `json:"source_type"` // git | image
	GitRepo     string `json:"git_repository,omitempty"`
	GitBranch   string `json:"git_branch,omitempty"`
	GitCommit   string `json:"git_commit,omitempty"`
	// GitToken authenticates the clone (Heroku Git); never shown.
	GitToken       string   `json:"-"`
	Image          string   `json:"docker_image,omitempty"`
	BuildPack      string   `json:"build_pack"`
	BaseDirectory  string   `json:"base_directory,omitempty"`
	Dockerfile     string   `json:"dockerfile_path,omitempty"`
	ComposePath    string   `json:"compose_path,omitempty"`
	Install        string   `json:"install_command,omitempty"`
	Build          string   `json:"build_command,omitempty"`
	Start          string   `json:"start_command,omitempty"`
	Publish        string   `json:"publish_directory,omitempty"`
	PreDeploy      string   `json:"pre_deploy_command,omitempty"`
	PostDeploy     string   `json:"post_deploy_command,omitempty"`
	Port           int      `json:"port"`
	Domains        []string `json:"domains"`
	HealthPath     string   `json:"health_check_path,omitempty"`
	Replicas       int      `json:"replicas"`
	CPU            string   `json:"cpu_limit,omitempty"`
	Memory         string   `json:"memory_limit,omitempty"`
	Env            []Var    `json:"env"`
	Volumes        []Volume `json:"volumes"`
	Jobs           []Job    `json:"jobs"`
	Notes          []string `json:"notes"`
	WatchPaths     string   `json:"watch_paths,omitempty"`
	PreviewEnabled bool     `json:"preview_enabled,omitempty"`
}

// Database is an empty database to create, of the same engine.
type Database struct {
	Ref         string   `json:"ref"`
	Name        string   `json:"name"`
	Project     string   `json:"project"`
	Environment string   `json:"environment"`
	Engine      string   `json:"engine"` // a PortDock engine
	Image       string   `json:"image,omitempty"`
	Notes       []string `json:"notes"`
}

// Service is a compose stack to create as a PortDock service.
type Service struct {
	Ref         string   `json:"ref"`
	Name        string   `json:"name"`
	Project     string   `json:"project"`
	Environment string   `json:"environment"`
	Compose     string   `json:"compose"`
	Domain      string   `json:"domain,omitempty"`
	Env         []Var    `json:"env"`
	Notes       []string `json:"notes"`
}

// Runner runs a command on the server of a source (Dokku) and returns its output.
type Runner func(ctx context.Context, cmd string) (string, error)

// Credentials reach a source.
type Credentials struct {
	URL   string // Coolify
	Token string // Coolify API token, Heroku API key
	Run   Runner // Dokku
}

// Read builds the plan of a source.
func Read(ctx context.Context, source string, c Credentials) (Plan, error) {
	var (
		p   Plan
		err error
	)
	switch source {
	case Coolify:
		p, err = readCoolify(ctx, c.URL, c.Token)
	case Heroku:
		p, err = readHeroku(ctx, herokuAPI, c.Token)
	case Dokku:
		p, err = readDokku(ctx, c.Run)
	default:
		return p, errUnknownSource
	}
	if err != nil {
		return p, err
	}
	p.Source = source
	p.normalize()
	return p, nil
}

func (p *Plan) normalize() {
	for i := range p.Apps {
		a := &p.Apps[i]
		a.Name = resourceName(a.Name)
		a.Project, a.Environment = orDefault(a.Project, "imported"), orDefault(a.Environment, "production")
		if a.Replicas < 1 {
			a.Replicas = 1
		}
		if a.Domains == nil {
			a.Domains = []string{}
		}
		sort.Slice(a.Env, func(x, y int) bool { return a.Env[x].Key < a.Env[y].Key })
		if a.Env == nil {
			a.Env = []Var{}
		}
		if a.Volumes == nil {
			a.Volumes = []Volume{}
		}
		if a.Jobs == nil {
			a.Jobs = []Job{}
		}
		if a.Notes == nil {
			a.Notes = []string{}
		}
	}
	for i := range p.Databases {
		d := &p.Databases[i]
		d.Name = resourceName(d.Name)
		d.Project, d.Environment = orDefault(d.Project, "imported"), orDefault(d.Environment, "production")
		if d.Notes == nil {
			d.Notes = []string{}
		}
	}
	for i := range p.Services {
		s := &p.Services[i]
		s.Name = resourceName(s.Name)
		s.Project, s.Environment = orDefault(s.Project, "imported"), orDefault(s.Environment, "production")
		if s.Env == nil {
			s.Env = []Var{}
		}
		if s.Notes == nil {
			s.Notes = []string{}
		}
	}
	if p.Apps == nil {
		p.Apps = []App{}
	}
	if p.Databases == nil {
		p.Databases = []Database{}
	}
	if p.Services == nil {
		p.Services = []Service{}
	}
	if p.Warnings == nil {
		p.Warnings = []string{}
	}
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return strings.TrimSpace(s)
}

var nameJunk = regexp.MustCompile(`[^a-z0-9-]+`)

// resourceName makes a name PortDock accepts for an application or a database.
func resourceName(s string) string {
	n := strings.Trim(nameJunk.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-"), "-")
	if n == "" {
		n = "app"
	}
	if len(n) > 50 {
		n = strings.TrimRight(n[:50], "-")
	}
	return n
}

// engineOf maps an image or a type name to a PortDock database engine.
func engineOf(s string) (engine, note string) {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "postgres") || strings.Contains(s, "postgis") || strings.Contains(s, "timescale"):
		return "postgres", ""
	case strings.Contains(s, "mariadb") || strings.Contains(s, "jawsdb-maria"):
		return "mariadb", ""
	case strings.Contains(s, "mysql") || strings.Contains(s, "jawsdb") || strings.Contains(s, "cleardb"):
		return "mysql", ""
	case strings.Contains(s, "mongo"):
		return "mongodb", ""
	case strings.Contains(s, "valkey"):
		return "valkey", ""
	case strings.Contains(s, "keydb") || strings.Contains(s, "dragonfly"):
		return "redis", "created as Redis, which speaks the same protocol"
	case strings.Contains(s, "redis"):
		return "redis", ""
	case strings.Contains(s, "clickhouse"):
		return "clickhouse", ""
	}
	return "", ""
}

// officialImage keeps an engine image only when it is the official one, so
// PortDock's backups and tools work with it.
func officialImage(engine, image string) string {
	repo := strings.Split(image, ":")[0]
	official := map[string][]string{
		"postgres": {"postgres", "docker.io/library/postgres"}, "mysql": {"mysql"}, "mariadb": {"mariadb"}, "mongodb": {"mongo"},
		"redis": {"redis"}, "valkey": {"valkey/valkey"}, "clickhouse": {"clickhouse/clickhouse-server"},
	}[engine]
	for _, o := range official {
		if repo == o {
			return image
		}
	}
	return ""
}
