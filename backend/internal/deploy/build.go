package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/netcfg"
	"github.com/portdock/portdock/internal/proxy"
	"github.com/portdock/portdock/internal/store"
	"go.yaml.in/yaml/v3"
)

func (j *job) buildArgs(flag string) string {
	keys := make([]string, 0, len(j.env.Build))
	for k := range j.env.Build {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(" " + flag + " " + q(k+"="+j.env.Build[k]))
	}
	return b.String()
}

func proxyArgs(flag string, env map[string]string) string {
	var b strings.Builder
	for _, k := range netcfg.Keys(env) {
		b.WriteString(" " + flag + " " + q(k+"="+env[k]))
	}
	return b.String()
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (j *job) imageLabels() string {
	return fmt.Sprintf(" --label %s=true --label %s=%s --label %s=%s --label org.opencontainers.image.revision=%s",
		docker.LabelManaged, docker.LabelResource, q(j.app.UUID), docker.LabelDeployment, q(j.d.UUID), q(j.d.CommitSHA))
}

func (j *job) build(ctx context.Context, image string) error {
	pack := j.app.BuildPack
	if pack == "auto" || pack == "" {
		detected, err := j.detect(ctx)
		if err != nil {
			return err
		}
		pack = detected
	}
	switch pack {
	case "dockerfile":
		return j.dockerBuild(ctx, image, path.Join(j.ctxDir, strings.TrimPrefix(j.app.DockerfilePath, "/")))
	case "nixpacks":
		return j.nixpacksBuild(ctx, image)
	case "static":
		j.app.Port = 80
		if err := executor.WriteFile(ctx, j.x, j.ctxDir+"/.portdock.nginx.conf", []byte(nginxConf), "0644"); err != nil {
			return err
		}
		df, err := j.writeDockerfile(ctx, j.staticDockerfile())
		if err != nil {
			return err
		}
		return j.dockerBuild(ctx, image, df)
	case "node", "bun", "python", "go", "shell":
		content, err := j.generatedDockerfile(ctx, pack)
		if err != nil {
			return err
		}
		df, err := j.writeDockerfile(ctx, content)
		if err != nil {
			return err
		}
		return j.dockerBuild(ctx, image, df)
	}
	return fmt.Errorf("unsupported build pack %q", pack)
}

func (j *job) dockerBuild(ctx context.Context, image, dockerfile string) error {
	// HTTP_PROXY… are predefined build arguments: RUN steps see them without
	// an ARG line, and they are left out of the image history.
	cmd := fmt.Sprintf("DOCKER_BUILDKIT=1 docker build --progress=plain -t %s -f %s%s%s%s %s",
		q(image), q(dockerfile), j.buildArgs("--build-arg"), proxyArgs("--build-arg", buildProxyEnv(j.net)), j.imageLabels(), q(j.ctxDir))
	if err := j.run(ctx, cmd); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}
	return nil
}

func (j *job) nixpacksBuild(ctx context.Context, image string) error {
	if _, err := j.output(ctx, "command -v nixpacks"); err != nil {
		return errors.New("nixpacks is not installed on the server; run Server setup with \"Also install Nixpacks\" enabled")
	}
	cmd := fmt.Sprintf("nixpacks build %s --name %s", q(j.ctxDir), q(image))
	if c := j.app.InstallCommand; c != "" {
		cmd += " --install-cmd " + q(c)
	}
	if c := j.app.BuildCommand; c != "" {
		cmd += " --build-cmd " + q(c)
	}
	if c := j.app.StartCommand; c != "" {
		cmd += " --start-cmd " + q(c)
	}
	cmd += j.buildArgs("--env") + proxyArgs("--env", buildProxyEnv(j.net))
	cmd += fmt.Sprintf(" --label %s=true --label %s=%s", docker.LabelManaged, docker.LabelResource, q(j.app.UUID))
	if err := j.run(ctx, cmd); err != nil {
		return fmt.Errorf("nixpacks build failed: %w", err)
	}
	return nil
}

func (j *job) writeDockerfile(ctx context.Context, content string) (string, error) {
	df := j.ctxDir + "/.portdock.Dockerfile"
	j.log.Printf("Generated Dockerfile:\n%s", content)
	return df, executor.WriteFile(ctx, j.x, df, []byte(content), "0644")
}

func (j *job) buildArgLines() string {
	var b strings.Builder
	keys := make([]string, 0, len(j.env.Build))
	for k := range j.env.Build {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "ARG %s\nENV %s=${%s}\n", k, k, k)
	}
	return b.String()
}

const nginxConf = `server {
    listen 80;
    root /usr/share/nginx/html;
    index index.html;
    gzip on;
    gzip_types text/plain text/css application/javascript application/json image/svg+xml;
    location / { try_files $uri $uri/ /index.html; }
    location ~* \.(?:js|css|woff2?|png|jpg|jpeg|gif|svg|ico|webp|avif)$ { expires 30d; add_header Cache-Control "public, immutable"; }
}`

func (j *job) staticDockerfile() string {
	pub := strings.Trim(j.app.PublishDirectory, "/")
	if j.app.BuildCommand == "" && j.app.InstallCommand == "" {
		src := "."
		if pub != "" && pub != "." {
			src = pub
		}
		return fmt.Sprintf("FROM nginx:alpine\nCOPY .portdock.nginx.conf /etc/nginx/conf.d/default.conf\nCOPY %s /usr/share/nginx/html\nEXPOSE 80\n", src)
	}
	install := j.app.InstallCommand
	if install == "" {
		install = "npm ci || npm install"
	}
	if pub == "" {
		pub = "dist"
	}
	return fmt.Sprintf(`FROM node:24-alpine AS build
WORKDIR /app
%sCOPY . .
RUN %s
RUN %s

FROM nginx:alpine
COPY .portdock.nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/%s /usr/share/nginx/html
EXPOSE 80
`, j.buildArgLines(), install, j.app.BuildCommand, pub)
}

// detect inspects the checkout and chooses a build strategy.
func (j *job) detect(ctx context.Context) (string, error) {
	out, err := j.output(ctx, "ls -1A "+q(j.ctxDir))
	if err != nil {
		return "", err
	}
	files := map[string]bool{}
	for _, f := range strings.Split(out, "\n") {
		files[strings.TrimSpace(f)] = true
	}
	pack := ""
	switch {
	case files["Dockerfile"]:
		pack = "dockerfile"
	case files["bun.lock"] || files["bun.lockb"]:
		pack = "bun"
	case files["package.json"]:
		pack = "node"
	case files["go.mod"]:
		pack = "go"
	case files["requirements.txt"] || files["pyproject.toml"]:
		pack = "python"
	case files["index.html"]:
		pack = "static"
	default:
		return "", errors.New("could not detect the project type; add a Dockerfile or choose a build pack")
	}
	j.log.Printf("Detected build pack: %s", pack)
	return pack, nil
}

type packageJSON struct {
	Scripts map[string]string `json:"scripts"`
	Engines map[string]string `json:"engines"`
}

func (j *job) generatedDockerfile(ctx context.Context, pack string) (string, error) {
	a := j.app
	port := strconv.Itoa(max(a.Port, 1))
	args := j.buildArgLines()
	switch pack {
	case "node", "bun":
		var pkg packageJSON
		if raw, err := j.output(ctx, "cat "+q(j.ctxDir+"/package.json")); err == nil {
			_ = json.Unmarshal([]byte(raw), &pkg)
		}
		files, _ := j.output(ctx, "ls -1A "+q(j.ctxDir))
		base, pm, install := "node:24-alpine", "npm", "npm ci || npm install"
		switch {
		case pack == "bun":
			base, pm, install = "oven/bun:1-alpine", "bun", "bun install --frozen-lockfile || bun install"
		case strings.Contains(files, "pnpm-lock.yaml"):
			pm, install = "pnpm", "corepack enable && pnpm install --frozen-lockfile"
		case strings.Contains(files, "yarn.lock"):
			pm, install = "yarn", "corepack enable && yarn install --frozen-lockfile"
		}
		if a.InstallCommand != "" {
			install = a.InstallCommand
		}
		build := a.BuildCommand
		if build == "" && pkg.Scripts["build"] != "" {
			build = pm + " run build"
		}
		start := a.StartCommand
		if start == "" {
			if pkg.Scripts["start"] != "" {
				start = pm + " run start"
			} else if pack == "bun" {
				start = "bun index.ts"
			} else {
				start = "node index.js"
			}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "FROM %s\nWORKDIR /app\n%sENV PORT=%s HOST=0.0.0.0\nCOPY . .\nRUN %s\n", base, args, port, install)
		if build != "" {
			fmt.Fprintf(&b, "RUN %s\n", build)
		}
		fmt.Fprintf(&b, "ENV NODE_ENV=production\nEXPOSE %s\nCMD %s\n", port, shellForm(start))
		return b.String(), nil
	case "python":
		install := a.InstallCommand
		if install == "" {
			install = "if [ -f requirements.txt ]; then pip install --no-cache-dir -r requirements.txt; else pip install --no-cache-dir .; fi"
		}
		start := a.StartCommand
		if start == "" {
			start = "python main.py"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "FROM python:3.13-slim\nWORKDIR /app\n%sENV PYTHONUNBUFFERED=1 PORT=%s\nCOPY . .\nRUN %s\n", args, port, install)
		if a.BuildCommand != "" {
			fmt.Fprintf(&b, "RUN %s\n", a.BuildCommand)
		}
		fmt.Fprintf(&b, "EXPOSE %s\nCMD %s\n", port, shellForm(start))
		return b.String(), nil
	case "go":
		build := a.BuildCommand
		if build == "" {
			build = "CGO_ENABLED=0 go build -ldflags='-s -w' -o /out/app ."
		}
		start := a.StartCommand
		if start == "" {
			start = "/app"
		}
		return fmt.Sprintf(`FROM golang:1.25-alpine AS build
WORKDIR /src
%sCOPY . .
RUN go mod download
RUN %s

FROM alpine:3
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/app /app
ENV PORT=%s
EXPOSE %s
CMD %s
`, args, build, port, port, shellForm(start)), nil
	case "shell":
		// Arbitrary shell commands on a base image of the user's choice.
		base := strings.TrimSpace(a.DockerImage)
		if base == "" {
			base = "debian:bookworm-slim"
		}
		if strings.TrimSpace(a.StartCommand) == "" {
			return "", errors.New("the shell build pack requires a start command")
		}
		var b strings.Builder
		fmt.Fprintf(&b, "FROM %s\nWORKDIR /app\n%sENV PORT=%s\nCOPY . .\n", base, args, port)
		if a.InstallCommand != "" {
			fmt.Fprintf(&b, "RUN %s\n", shellForm(a.InstallCommand))
		}
		if a.BuildCommand != "" {
			fmt.Fprintf(&b, "RUN %s\n", shellForm(a.BuildCommand))
		}
		fmt.Fprintf(&b, "EXPOSE %s\nCMD %s\n", port, shellForm(a.StartCommand))
		return b.String(), nil
	}
	return "", fmt.Errorf("no generator for %s", pack)
}

// shellForm renders a Dockerfile exec-form command running cmd through sh.
func shellForm(cmd string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false) // keep > < & readable in generated Dockerfiles
	_ = enc.Encode([]string{"sh", "-c", cmd})
	return strings.TrimSpace(b.String())
}

// ---- docker compose ----

func composeProject(uuid string) string { return "sy-" + strings.ToLower(uuid) }

type composeSpec struct {
	PortDock struct {
		Service string `yaml:"service"`
		Port    int    `yaml:"port"`
	} `yaml:"x-portdock"`
	Services map[string]yaml.Node `yaml:"services"`
}

// ComposeUp deploys a compose project with PortDock routing and labels injected via an override file.
type ComposeUp struct {
	X       executor.Executor
	Project string
	Dir     string // directory on the server holding the compose file
	File    string // compose file path
	Service string // service to expose (defaults to x-portdock.service)
	Port    int
	Domains []string
	HTTPS   bool
	// FileRoutes routes through the proxy's file provider (written by the caller)
	// instead of container labels; Routed services join the proxy network.
	FileRoutes bool
	Routed     []string
	// ServiceEnv sets the environment of each service on top of the compose file's
	// own `environment:` (PortDock values win). The main service also gets Env.
	// Used with FileRoutes; other callers pass Env through an env file.
	ServiceEnv map[string]map[string]string
	// Images pins services to existing images (a version's), so nothing is built or pulled for them.
	Images map[string]string
	Env    map[string]string
	// ProxyEnv (the corporate proxy) is added to every service, below its own variables.
	ProxyEnv map[string]string
	// BuildArgs are passed to the builds of the compose file (proxy for builds).
	BuildArgs map[string]string
	Kind      string // application | service
	Resource  string
	Network   string
	Build     bool
	Timeout   int
	Log       interface {
		Write([]byte) (int, error)
		Printf(string, ...any)
	}
}

// escapeCompose doubles "$" so compose doesn't interpolate variable values.
func escapeCompose(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = strings.ReplaceAll(v, "$", "$$")
	}
	return out
}

// ComposeResult reports the main service of a deployed compose project.
type ComposeResult struct {
	Main string
	Port int
}

func RunComposeUp(ctx context.Context, c ComposeUp) (ComposeResult, error) {
	raw, err := executor.Output(ctx, c.X, "cat "+q(c.File))
	if err != nil {
		return ComposeResult{}, fmt.Errorf("read compose file: %w", err)
	}
	var spec composeSpec
	if err := yaml.Unmarshal([]byte(raw), &spec); err != nil {
		return ComposeResult{}, fmt.Errorf("invalid compose file: %w", err)
	}
	if len(spec.Services) == 0 {
		return ComposeResult{}, errors.New("compose file defines no services")
	}
	names := make([]string, 0, len(spec.Services))
	for n := range spec.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	main := c.Service
	if main == "" {
		main = spec.PortDock.Service
	}
	if main == "" {
		main = names[0]
	}
	if _, ok := spec.Services[main]; !ok {
		return ComposeResult{}, fmt.Errorf("service %q not found in compose file (available: %s)", main, strings.Join(names, ", "))
	}
	for _, n := range c.Routed {
		if _, ok := spec.Services[n]; !ok {
			return ComposeResult{}, fmt.Errorf("a domain targets service %q, which is not in the compose file (available: %s)", n, strings.Join(names, ", "))
		}
	}
	port := c.Port
	if spec.PortDock.Port > 0 && (port == 0 || c.Kind == "service") {
		port = spec.PortDock.Port
	}
	res := ComposeResult{Main: main, Port: port}

	envPath := c.Dir + "/.portdock.env"
	if err := executor.WriteFile(ctx, c.X, envPath, []byte(envFile(c.Env)), "0600"); err != nil {
		return res, err
	}

	override := map[string]any{}
	services := map[string]any{}
	for _, n := range names {
		labels := map[string]string{
			docker.LabelManaged:  "true",
			docker.LabelKind:     c.Kind,
			docker.LabelResource: c.Resource,
		}
		svc := map[string]any{"labels": labels}
		if n == main && !c.FileRoutes {
			for k, v := range proxy.Labels(proxy.Route{Name: c.Resource, Domains: c.Domains, Port: port, HTTPS: c.HTTPS, Network: c.Network}) {
				labels[k] = v
			}
		}
		if n == main || slices.Contains(c.Routed, n) {
			svc["networks"] = []string{"default", c.Network}
		}
		if img := c.Images[n]; img != "" {
			svc["image"], svc["pull_policy"] = img, "never"
		}
		if c.FileRoutes {
			env := map[string]string{}
			for k, v := range c.ProxyEnv {
				env[k] = v
			}
			if n == main {
				for k, v := range c.Env {
					env[k] = v
				}
			}
			for k, v := range c.ServiceEnv[n] {
				env[k] = v
			}
			if len(env) > 0 {
				svc["environment"] = escapeCompose(env)
			}
		} else {
			if n == main {
				svc["env_file"] = []string{envPath}
			}
			if len(c.ProxyEnv) > 0 {
				svc["environment"] = escapeCompose(c.ProxyEnv)
			}
		}
		services[n] = svc
	}
	override["services"] = services
	override["networks"] = map[string]any{c.Network: map[string]any{"external": true}}
	ob, _ := yaml.Marshal(override)
	overridePath := c.Dir + "/.portdock.override.yml"
	if err := executor.WriteFile(ctx, c.X, overridePath, ob, "0600"); err != nil { // holds variable values
		return res, err
	}

	timeout := max(c.Timeout, 60)
	cmd := fmt.Sprintf("cd %s && docker compose -p %s -f %s -f %s --env-file %s up -d --remove-orphans --wait --wait-timeout %d",
		q(c.Dir), q(c.Project), q(c.File), q(overridePath), q(envPath), timeout)
	if c.Build && len(c.BuildArgs) > 0 {
		build := fmt.Sprintf("cd %s && docker compose -p %s -f %s -f %s --env-file %s build%s",
			q(c.Dir), q(c.Project), q(c.File), q(overridePath), q(envPath), proxyArgs("--build-arg", c.BuildArgs))
		c.Log.Printf("$ docker compose build (with the corporate proxy)")
		if err := c.X.Run(ctx, build, executor.RunOpts{Stdout: c.Log}); err != nil {
			return res, fmt.Errorf("docker compose build failed: %w", err)
		}
	} else if c.Build {
		cmd += " --build"
	}
	c.Log.Printf("$ %s", cmd)
	if err := c.X.Run(ctx, cmd, executor.RunOpts{Stdout: c.Log}); err != nil {
		return res, fmt.Errorf("docker compose up failed: %w", err)
	}
	if !c.FileRoutes {
		for _, d := range c.Domains {
			host, p := proxy.NormalizeDomain(d)
			c.Log.Printf("🌐 %s → %s:%d", host+p, main, port)
		}
	}
	return res, nil
}

// deployComposeAll runs the compose project on every server, one after another.
func (j *job) deployComposeAll(ctx context.Context, targets []target) error {
	for i, t := range targets {
		tj := *j
		tj.x, tj.srv = t.x, t.srv
		if len(targets) > 1 {
			j.log.Step(fmt.Sprintf("Server %d/%d: %s", i+1, len(targets), t.srv.Name))
		}
		if err := tj.deployCompose(ctx); err != nil {
			if i > 0 {
				return fmt.Errorf("%s: %w (updated on %d of %d servers)", t.srv.Name, err, i, len(targets))
			}
			return err
		}
		j.d.Image = tj.d.Image
	}
	j.workdir = ""
	if j.d.PRNumber == 0 && j.d.RollbackOf == 0 {
		j.recordComposeVersion(context.WithoutCancel(ctx), targets)
	}
	return nil
}

func (j *job) deployCompose(ctx context.Context) error {
	// Deploying a saved version, or restarting with changed variables, fetches
	// and builds nothing: the version's compose file runs with its images.
	reuse := j.d.RollbackOf != 0 && (j.d.Trigger == TriggerConfig || j.d.VersionID != 0)
	if j.d.RollbackOf != 0 && !reuse {
		return errors.New("deploy a saved version from the Versions tab to go back with docker compose applications")
	}
	var version *store.AppVersion
	if reuse && j.d.VersionID != 0 {
		v, err := store.ByID[store.AppVersion](ctx, j.e.db, "app_versions", j.d.VersionID)
		if err != nil {
			return fmt.Errorf("load version: %w", err)
		}
		version = &v
	}
	// Compose checkouts are kept so relative bind mounts and build contexts stay valid.
	j.workdir = path.Join(j.e.cfg.DataDir, "apps", j.app.UUID, "compose")
	j.srcDir = j.workdir + "/src"
	j.ctxDir = path.Join(j.srcDir, strings.Trim(j.app.BaseDirectory, "/"))
	if reuse {
		j.log.Step("Applying configuration (no rebuild)")
	} else if err := j.checkout(ctx); err != nil {
		return err
	}
	j.workdir = "" // keep the checkout after deploy

	j.log.Step("Docker Compose up")
	// Variables for ${VAR} interpolation in the compose file: the application's,
	// then each service's (for build args of that service).
	vars := map[string]string{}
	for k, v := range j.env.Build {
		vars[k] = v
	}
	for _, svc := range j.env.Services {
		for k, v := range svc {
			vars[k] = v
		}
	}
	for k, v := range j.env.Runtime {
		vars[k] = v
	}
	j.d.Image = "compose@" + short(j.d.CommitSHA)
	if j.reg != nil {
		if err := j.e.dockerLogin(ctx, j.x, *j.reg, j.log); err != nil {
			return err
		}
	}
	file := path.Join(j.ctxDir, strings.TrimPrefix(j.app.ComposePath, "/"))
	var images map[string]string
	if version != nil && version.ComposeFile != "" {
		// Next to the original so relative paths (bind mounts, build contexts) resolve the same.
		file = path.Join(path.Dir(file), ".portdock.version.yml")
		if err := executor.WriteFile(ctx, j.x, file, []byte(version.ComposeFile), "0644"); err != nil {
			return err
		}
		images = VersionServices(*version)
		j.log.Printf("Deploying version %s (%d service image(s)).", versionTag(version.Number), len(images))
	}
	// Resolve which service and port each domain targets before starting anything.
	raw, err := executor.Output(ctx, j.x, "cat "+q(file))
	if err != nil {
		if reuse {
			return errors.New("the application has not been deployed on this server yet; deploy it first")
		}
		return fmt.Errorf("read compose file: %w", err)
	}
	main, mainPort, services, err := composeMain(raw, j.app.ComposeService, j.app.Port)
	if err != nil {
		return err
	}
	routes, err := resolveComposeRoutes(j.routes, main, mainPort, services, j.log)
	if err != nil {
		return err
	}
	if _, err := RunComposeUp(ctx, ComposeUp{
		X: j.x, Project: composeProject(j.app.UUID), Dir: j.ctxDir, File: file,
		Service: main, Port: mainPort, FileRoutes: true, Routed: routedServices(routes),
		Env: vars, ServiceEnv: j.env.Services, Images: images, Kind: "application", Resource: j.app.UUID, Network: j.e.cfg.ProxyNetwork,
		ProxyEnv: containerProxyEnv(j.net, j.app.ProxyMode), BuildArgs: buildProxyEnv(j.net),
		Build: !reuse, Timeout: j.app.HealthCheckTimeout, Log: j.log,
	}); err != nil {
		return err
	}
	// Traffic moves once `up --wait` reported the containers healthy.
	return j.e.writeComposeRoutes(ctx, j.x, j.app, routes, j.d.PRNumber, j.log)
}

// ---- shared helpers ----

type Env struct {
	Runtime map[string]string
	Build   map[string]string
	Secrets []string
	// Services holds the variables of each Docker Compose service (compose applications).
	Services map[string]map[string]string
}

// ResolveEnv decrypts the environment variables of a resource. Preview
// variables override regular ones when preview is true.
func (e *Engine) ResolveEnv(ctx context.Context, ownerType string, ownerID int64, preview bool) (Env, error) {
	vars, err := store.Select[store.EnvVar](ctx, e.db, "SELECT * FROM env_vars WHERE owner_type = ? AND owner_id = ? ORDER BY is_preview, key", ownerType, ownerID)
	if err != nil {
		return Env{}, err
	}
	env := Env{Runtime: map[string]string{}, Build: map[string]string{}, Services: map[string]map[string]string{}}
	for _, v := range vars {
		if v.IsPreview && !preview {
			continue
		}
		val, err := e.box.Decrypt(v.Value)
		if err != nil {
			return env, fmt.Errorf("decrypt %s: %w", v.Key, err)
		}
		if v.IsSecret {
			env.Secrets = append(env.Secrets, val)
		}
		if v.Service != "" {
			if env.Services[v.Service] == nil {
				env.Services[v.Service] = map[string]string{}
			}
			env.Services[v.Service][v.Key] = val
			continue
		}
		env.Runtime[v.Key] = val
		if v.IsBuild {
			env.Build[v.Key] = val
		}
	}
	return env, nil
}

// PreviewDomains computes the domain(s) of a pull request preview.
func (e *Engine) PreviewDomains(app store.Application, srv store.Server, pr int) []string {
	tpl := app.PreviewDomain
	if tpl == "" && srv.WildcardDomain != "" {
		tpl = proxy.SafeName(app.Name) + "-pr-{{pr}}." + strings.TrimPrefix(srv.WildcardDomain, "*.")
	}
	if tpl == "" {
		return nil
	}
	return []string{strings.ReplaceAll(tpl, "{{pr}}", strconv.Itoa(pr))}
}

func (e *Engine) ProxyOptions(ctx context.Context) proxy.Options {
	return proxy.Options{
		Network:      e.cfg.ProxyNetwork,
		DataDir:      e.cfg.DataDir,
		ACMEEmail:    e.db.Setting(ctx, "acme_email"),
		Dashboard:    e.db.Setting(ctx, "proxy_dashboard") == "true",
		ACMEServer:   e.db.Setting(ctx, "acme_server"),
		DNSProvider:  e.db.Setting(ctx, "dns_provider"),
		DNSEnv:       e.dnsEnv(ctx),
		DNSResolvers: e.db.Setting(ctx, "dns_resolvers"),
	}
}

func (e *Engine) dnsEnv(ctx context.Context) map[string]string {
	env := map[string]string{}
	_ = json.Unmarshal([]byte(e.box.MustDecrypt(e.db.Setting(ctx, "dns_env"))), &env)
	return env
}
