package github

import (
	"context"
	"encoding/json"
	"path"
	"strings"

	"github.com/portdock/portdock/internal/store"
)

// Suggestion is the recommended deployment configuration for a repository.
type Suggestion struct {
	BuildPack        string   `json:"build_pack"`
	Port             int      `json:"port"`
	Reason           string   `json:"reason"`
	InstallCommand   string   `json:"install_command,omitempty"`
	BuildCommand     string   `json:"build_command,omitempty"`
	StartCommand     string   `json:"start_command,omitempty"`
	PublishDirectory string   `json:"publish_directory,omitempty"`
	ComposePath      string   `json:"compose_path,omitempty"`
	Framework        string   `json:"framework,omitempty"`
	Files            []string `json:"files"`
}

type pkgJSON struct {
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	PackageManager  string            `json:"packageManager"`
}

func (p pkgJSON) has(dep string) bool {
	_, a := p.Dependencies[dep]
	_, b := p.DevDependencies[dep]
	return a || b
}

// Detect inspects the repository (at ref, within baseDir) and suggests a build pack.
func (c *Client) Detect(ctx context.Context, app store.GitHubApp, installationID int64, fullName, ref, baseDir string) (Suggestion, error) {
	entries, err := c.Contents(ctx, app, installationID, fullName, baseDir, ref)
	if err != nil {
		return Suggestion{}, err
	}
	files := map[string]bool{}
	s := Suggestion{Files: []string{}}
	for _, e := range entries {
		name := e.Name
		if e.Type == "dir" {
			name += "/"
		}
		files[e.Name] = true
		s.Files = append(s.Files, name)
	}
	file := func(names ...string) string {
		for _, n := range names {
			if files[n] {
				return n
			}
		}
		return ""
	}
	switch {
	case file("Dockerfile") != "":
		s.BuildPack, s.Port, s.Reason = "dockerfile", 3000, "Dockerfile found in the repository"
	case file("docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml") != "":
		s.BuildPack, s.Reason = "compose", "Docker Compose file found"
		s.ComposePath = file("docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml")
	case file("package.json") != "":
		var p pkgJSON
		if raw, err := c.File(ctx, app, installationID, fullName, path.Join(baseDir, "package.json"), ref); err == nil {
			_ = json.Unmarshal([]byte(raw), &p)
		}
		pm := "npm"
		switch {
		case file("bun.lock", "bun.lockb") != "" || strings.HasPrefix(p.PackageManager, "bun"):
			pm = "bun"
		case file("pnpm-lock.yaml") != "" || strings.HasPrefix(p.PackageManager, "pnpm"):
			pm = "pnpm"
		case file("yarn.lock") != "" || strings.HasPrefix(p.PackageManager, "yarn"):
			pm = "yarn"
		}
		s.BuildPack, s.Port = "node", 3000
		if pm == "bun" {
			s.BuildPack = "bun"
		}
		switch {
		case p.has("next"):
			s.Framework = "Next.js"
		case p.has("nuxt"):
			s.Framework = "Nuxt"
		case p.has("@remix-run/node") || p.has("@react-router/node"):
			s.Framework = "Remix / React Router"
		case p.has("@sveltejs/kit"):
			s.Framework = "SvelteKit"
		case p.has("@nestjs/core"):
			s.Framework = "NestJS"
		case p.has("express") || p.has("fastify") || p.has("hono"):
			s.Framework = "Node.js server"
		case p.has("vite") && p.Scripts["start"] == "":
			// Client-only Vite app: build once and serve static files.
			s.Framework, s.BuildPack, s.Port = "Vite", "static", 80
			s.InstallCommand = map[string]string{"npm": "npm ci || npm install", "pnpm": "corepack enable && pnpm install", "yarn": "corepack enable && yarn install", "bun": "npm install -g bun && bun install"}[pm]
			s.BuildCommand = pm + " run build"
			s.PublishDirectory = "dist"
		}
		s.Reason = "package.json found (" + pm + ")"
		if s.Framework != "" {
			s.Reason = s.Framework + " project detected (" + pm + ")"
		}
	case file("go.mod") != "":
		s.BuildPack, s.Port, s.Framework, s.Reason = "go", 8080, "Go", "go.mod found"
	case file("requirements.txt", "pyproject.toml", "Pipfile") != "":
		s.BuildPack, s.Port, s.Framework, s.Reason = "python", 8000, "Python", "Python project detected"
		if files["manage.py"] {
			s.Framework, s.Reason = "Django", "Django project detected; set the start command (e.g. gunicorn <project>.wsgi)"
		}
	case file("index.html") != "":
		s.BuildPack, s.Port, s.Reason, s.PublishDirectory = "static", 80, "Static site (index.html)", "."
	default:
		s.BuildPack, s.Port, s.Reason = "shell", 3000, "No known project type; use a shell script build"
	}
	return s, nil
}
