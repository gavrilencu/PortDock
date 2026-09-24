package deploy

import (
	"context"
	"errors"
	"fmt"
	"github.com/portdock/portdock/internal/azuredevops"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/netcfg"
	"github.com/portdock/portdock/internal/proxy"
	"github.com/portdock/portdock/internal/registry"
	"github.com/portdock/portdock/internal/store"
)

var q = executor.Quote

const gitImage = "alpine/git:latest"

// job carries the state of a single deployment run.
type job struct {
	e       *Engine
	app     store.Application
	srv     store.Server
	x       executor.Executor
	d       *store.Deployment
	log     *logger
	workdir string // build directory on the target server
	srcDir  string // checkout root
	ctxDir  string // build context (checkout + base directory)
	env     Env
	routes  []proxy.DomainRoute // production domains, or preview domains for PRs
	reg     *store.Registry     // push/pull registry (nil: images stay on the servers)
	net     netcfg.Config       // corporate proxy and mirrors
}

func (j *job) run(ctx context.Context, cmd string) error {
	j.log.Printf("$ %s", cmd)
	return j.x.Run(ctx, cmd, executor.RunOpts{Stdout: j.log})
}

func (j *job) output(ctx context.Context, cmd string) (string, error) {
	return executor.Output(ctx, j.x, cmd)
}

func (e *Engine) runApplication(ctx context.Context, d *store.Deployment, log *logger) error {
	app, err := store.ByID[store.Application](ctx, e.db, "applications", d.ApplicationID)
	if err != nil {
		return fmt.Errorf("load application: %w", err)
	}
	targets, err := e.appTargets(ctx, app)
	if err != nil {
		return err
	}
	if d.PRNumber > 0 {
		targets = targets[:1] // previews run on the primary server only
	}
	build := targets[0]
	if app.BuildServerID != 0 && app.BuildServerID != app.ServerID && app.BuildPack != "compose" && app.SourceType != "image" && d.RollbackOf == 0 {
		x, srv, err := e.pool.ForID(ctx, app.BuildServerID)
		if err != nil {
			return fmt.Errorf("connect to build server: %w", err)
		}
		build = target{x: x, srv: srv}
	}
	if d.PRNumber == 0 {
		_, _ = e.db.ExecContext(ctx, "UPDATE applications SET status = 'deploying' WHERE id = ?", app.ID)
		e.hub.Event("application", app.ID, "deploying")
	}
	j := &job{e: e, app: app, srv: build.srv, x: build.x, d: d, log: log}
	if j.reg, err = e.Registry(ctx, app.RegistryID); err != nil {
		return err
	}
	if d.PRNumber > 0 {
		j.routes = e.previewRoutes(app, targets[0].srv, d.PRNumber)
	} else if j.routes, err = e.DomainRoutes(ctx, app); err != nil {
		return fmt.Errorf("load domains: %w", err)
	}
	j.workdir = path.Join(e.cfg.DataDir, "builds", app.UUID, d.UUID)
	j.srcDir = j.workdir + "/src"
	j.ctxDir = path.Join(j.srcDir, strings.Trim(app.BaseDirectory, "/"))

	if j.env, err = e.ResolveEnv(ctx, "application", app.ID, d.PRNumber > 0); err != nil {
		return err
	}
	log.Mask(j.env.Secrets...)
	j.net = e.Network(ctx)
	if j.net.Password != "" {
		log.Mask(j.net.Password)
	}
	// Package mirrors (npm, PyPI, Go…) are build variables of every build.
	for k, v := range j.net.PackageEnv() {
		if _, set := j.env.Build[k]; !set {
			j.env.Build[k] = v
		}
	}
	if j.net.Enabled() && (j.net.Builds || j.net.Containers || app.ProxyMode == "on") {
		log.Printf("Corporate proxy: builds %s, containers %s", onOff(j.net.Builds), onOff(containerProxyEnv(j.net, app.ProxyMode) != nil))
	}
	if tok := e.box.MustDecrypt(app.GitToken); tok != "" {
		log.Mask(tok)
	}

	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.srv.Name
	}
	log.Printf("🚀 Deploying %s (deployment %s, trigger: %s) to %s", app.Name, d.UUID, d.Trigger, plural(len(targets), "server")+" ("+strings.Join(names, ", ")+")")
	if build.srv.ID != targets[0].srv.ID {
		log.Printf("Building on dedicated build server %q", build.srv.Name)
	}
	if j.reg != nil {
		log.Printf("Registry: %s (%s)", j.reg.Name, j.reg.Host)
	}
	if d.PRNumber > 0 {
		log.Printf("Preview deployment for pull request #%d (branch %s)", d.PRNumber, d.Branch)
	}

	log.Step("Preparing " + plural(len(targets), "server"))
	for _, t := range targets {
		if err := docker.EnsureNetwork(ctx, t.x, e.cfg.ProxyNetwork); err != nil {
			return fmt.Errorf("%s: create network: %w", t.srv.Name, err)
		}
		if t.srv.ProxyEnabled && len(j.domains()) > 0 {
			if err := proxy.Ensure(ctx, t.x, e.ProxyOptions(ctx), false); err != nil {
				log.Printf("⚠ %s: could not start proxy: %v", t.srv.Name, err)
			}
		}
	}

	defer func() {
		if j.workdir != "" {
			// Files cloned by the git container are root-owned; fall back to a container for cleanup.
			parent, name := path.Split(j.workdir)
			_ = build.x.Run(context.WithoutCancel(ctx), fmt.Sprintf("rm -rf %s 2>/dev/null || docker run --rm -v %s:/w alpine rm -rf /w/%s",
				q(j.workdir), q(parent), q(name)), executor.RunOpts{})
		}
	}()

	if app.BuildPack == "compose" {
		if err := j.deployComposeAll(ctx, targets); err != nil {
			return err
		}
		// Compose builds and starts in one step: the images are scanned once running (report only).
		if app.ScanOnDeploy && d.RollbackOf == 0 && d.PRNumber == 0 {
			log.Step("Vulnerability scan (Trivy)")
			a := app
			a.ServerID = targets[0].srv.ID
			if err := e.ScanApp(context.WithoutCancel(ctx), a, d.ID, "deploy"); err != nil {
				log.Printf("⚠ Could not start the scan: %v", err)
			} else {
				log.Printf("The service images are being scanned in the background; see the Security tab.")
			}
		}
		j.startDAST(ctx, targets[0].srv.ID)
		return nil
	}

	image, pull, err := j.resolveImage(ctx)
	if err != nil {
		return err
	}
	d.Image = image
	if len(targets) > 1 || build.srv.ID != targets[0].srv.ID || pull {
		log.Step("Distributing image")
		if err := j.distribute(ctx, image, build, targets, pull); err != nil {
			return err
		}
	}

	if app.ScanOnDeploy && d.RollbackOf == 0 {
		if err := j.scanBeforeRollout(ctx, targets[0].x, image); err != nil {
			return err
		}
	}

	for i, t := range targets {
		tj := *j // per-server copy; build-time changes to j.app (e.g. static port) carry over
		tj.x, tj.srv = t.x, t.srv
		if len(targets) > 1 {
			log.Step(fmt.Sprintf("Server %d/%d: %s", i+1, len(targets), t.srv.Name))
		}
		if i == 0 && app.PreDeployCommand != "" {
			log.Step("Pre-deploy command")
			envFile, err := tj.writeEnvFile(ctx)
			if err != nil {
				return err
			}
			cmd := fmt.Sprintf("docker run --rm --network %s --env-file %s %s sh -c %s",
				q(e.cfg.ProxyNetwork), q(envFile), q(image), q(app.PreDeployCommand))
			if err := tj.run(ctx, cmd); err != nil {
				return fmt.Errorf("pre-deploy command failed: %w", err)
			}
		}
		if err := tj.rollout(ctx, image); err != nil {
			if i > 0 {
				return fmt.Errorf("%s: %w (the new version is live on %d of %d servers)", t.srv.Name, err, i, len(targets))
			}
			return err
		}
		if app.PostDeployCommand != "" {
			log.Step("Post-deploy command")
			if err := tj.run(ctx, fmt.Sprintf("docker exec %s sh -c %s", q(tj.containerName(1)), q(app.PostDeployCommand))); err != nil {
				log.Printf("⚠ post-deploy command failed: %v", err)
			}
		}
		tj.cleanupImages(context.WithoutCancel(ctx))
	}
	if build.srv.ID != targets[0].srv.ID {
		j.cleanupImages(context.WithoutCancel(ctx))
	}
	if d.PRNumber == 0 && d.RollbackOf == 0 {
		j.recordImageVersion(context.WithoutCancel(ctx), targets, image)
	}
	j.startDAST(ctx, targets[0].srv.ID)
	return nil
}

// startDAST scans the new version in the background once it is live.
func (j *job) startDAST(ctx context.Context, serverID int64) {
	if !j.app.DastOnDeploy || j.d.PRNumber != 0 {
		return
	}
	j.log.Step("Dynamic scan (DAST, OWASP ZAP)")
	a := j.app
	a.ServerID = serverID
	if err := j.e.ScanRunning(context.WithoutCancel(ctx), a, j.d.ID, "deploy"); err != nil {
		j.log.Printf("⚠ Could not start the dynamic scan: %v", err)
		return
	}
	j.log.Printf("OWASP ZAP is scanning the application in the background; see the Security tab.")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// resolveImage checks out and builds the source (pushing it to the registry),
// or selects a prebuilt image. pull reports that every server must pull it.
func (j *job) resolveImage(ctx context.Context) (image string, pull bool, err error) {
	app, d := j.app, j.d

	if d.RollbackOf != 0 {
		j.log.Step("Rollback")
		j.log.Printf("Re-using image %s from deployment #%d", d.Image, d.RollbackOf)
		if !hasImage(ctx, j.x, d.Image) && (j.reg == nil || !strings.HasPrefix(d.Image, j.reg.Host+"/")) {
			if app.SourceType == "image" || app.BuildPack == "image" {
				return d.Image, true, nil
			}
			return "", false, fmt.Errorf("image %s is no longer available on the server", d.Image)
		}
		return d.Image, false, nil
	}

	if app.SourceType == "image" || app.BuildPack == "image" {
		if app.DockerImage == "" {
			return "", false, errors.New("no docker image configured")
		}
		return app.DockerImage, true, nil
	}

	if err := j.checkout(ctx); err != nil {
		return "", false, err
	}
	if app.SastOnDeploy {
		if err := j.sastAfterCheckout(ctx); err != nil {
			return "", false, err
		}
	}

	image = j.imageRepo() + ":" + short12(d.CommitSHA)
	if !d.ForceRebuild && hasImage(ctx, j.x, image) {
		j.log.Printf("Image %s already exists for this commit, skipping build (use force rebuild to override).", image)
		return image, false, nil
	}
	j.log.Step("Building image (" + app.BuildPack + ")")
	if err := j.build(ctx, image); err != nil {
		return "", false, err
	}
	if d.PRNumber == 0 {
		if err := j.push(ctx, image); err != nil {
			return "", false, err
		}
	}
	return image, false, nil
}

func short12(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	if sha == "" {
		return "latest"
	}
	return sha
}

func (j *job) imageRepo() string {
	if j.reg != nil && j.d.PRNumber == 0 {
		return registry.Repository(*j.reg, j.app.RegistryImage, j.app.Name)
	}
	repo := "portdock/" + j.app.UUID
	if j.d.PRNumber > 0 {
		repo += "-pr" + strconv.Itoa(j.d.PRNumber)
	}
	return repo
}

// gitURL injects the access token into HTTPS repository URLs.
func (j *job) gitURL(ctx context.Context) (string, error) {
	if j.app.GitHubAppID != 0 && j.app.GitHubInstallation != 0 && j.app.GitFullName != "" {
		u, err := j.e.github.CloneURL(ctx, j.app.GitHubAppID, j.app.GitHubInstallation, j.app.GitFullName)
		if err != nil {
			return "", fmt.Errorf("GitHub App authentication failed: %w", err)
		}
		if pu, err := url.Parse(u); err == nil {
			if tok, ok := pu.User.Password(); ok {
				j.log.Mask(tok)
			}
		}
		return u, nil
	}
	if j.app.AzureConnectionID != 0 {
		conn, err := store.ByID[store.AzureConnection](ctx, j.e.db, "azure_connections", j.app.AzureConnectionID)
		if err != nil {
			return "", fmt.Errorf("the Azure DevOps connection of this application no longer exists: %w", err)
		}
		tok := j.e.box.MustDecrypt(conn.Token)
		j.log.Mask(tok)
		return azuredevops.CloneURL(j.app.GitRepository, tok), nil
	}
	return j.gitURLStatic(), nil
}

func (j *job) gitURLStatic() string {
	raw := strings.TrimSpace(j.app.GitRepository)
	tok := j.e.box.MustDecrypt(j.app.GitToken)
	if tok == "" || !strings.HasPrefix(raw, "https://") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	user := "x-access-token"
	switch {
	case strings.Contains(u.Host, "gitlab"):
		user = "oauth2"
	case strings.Contains(u.Host, "bitbucket"):
		user = "x-token-auth"
	}
	u.User = url.UserPassword(user, tok)
	return u.String()
}

func (j *job) checkout(ctx context.Context) error {
	app, d := j.app, j.d
	j.log.Step("Cloning repository")
	if app.GitRepository == "" {
		return errors.New("no git repository configured")
	}
	if err := j.x.Run(ctx, fmt.Sprintf("rm -rf %s && mkdir -p %s", q(j.workdir), q(j.srcDir)), executor.RunOpts{}); err != nil {
		return err
	}
	repo, err := j.gitURL(ctx)
	if err != nil {
		return err
	}
	if repo != app.GitRepository {
		j.log.Mask(repo) // contains credentials
	}
	j.log.Printf("Repository: %s", app.GitRepository)

	var steps []string
	switch {
	case d.PRNumber > 0:
		steps = []string{
			"init -q",
			"remote add origin " + q(repo),
			fmt.Sprintf("fetch -q --depth 1 origin %s", q(fmt.Sprintf("pull/%d/head", d.PRNumber))),
			"checkout -q FETCH_HEAD",
		}
		if d.CommitSHA != "" {
			// GitLab uses merge-requests refs; fall back to the branch when pull/N/head is unknown.
			steps[2] = fmt.Sprintf("fetch -q --depth 1 origin %s || git fetch -q --depth 1 origin %s || git fetch -q --depth 1 origin %s",
				q(d.CommitSHA), q(fmt.Sprintf("pull/%d/head", d.PRNumber)), q(d.Branch))
		}
	case d.CommitSHA != "" || app.GitCommit != "":
		sha := d.CommitSHA
		if sha == "" {
			sha = app.GitCommit
		}
		steps = []string{
			"init -q",
			"remote add origin " + q(repo),
			fmt.Sprintf("fetch -q --depth 1 origin %s || git fetch -q origin %s", q(sha), q(d.Branch)),
			"checkout -q " + q(sha) + " 2>/dev/null || git checkout -q FETCH_HEAD",
		}
	default:
		steps = []string{fmt.Sprintf("clone -q --depth 1 --branch %s --single-branch --recurse-submodules --shallow-submodules %s .", q(d.Branch), q(repo))}
	}
	script := "set -e; git config --global --add safe.directory '*'; git " + strings.Join(steps, "; git ") + "; git log -1 --format='%H%n%s'"
	cmd := fmt.Sprintf("docker run --rm%s -v %s:/work -w /work/src --entrypoint sh %s -c %s", envFlags(buildProxyEnv(j.net)), q(j.workdir), gitImage, q(script))
	j.log.Printf("$ git clone %s (branch %s)", app.GitRepository, d.Branch)
	out, err := j.output(ctx, cmd)
	if err != nil {
		return fmt.Errorf("git checkout failed: %v", maskErr(err, repo))
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if n := len(lines); n >= 2 {
		d.CommitSHA = strings.TrimSpace(lines[n-2])
		d.CommitMessage = strings.TrimSpace(lines[n-1])
	}
	j.log.Printf("Checked out %s: %s", short(d.CommitSHA), d.CommitMessage)
	j.e.reportCommitStatus(app, *d, "pending", "Deployment in progress")
	_, _ = j.e.db.ExecContext(ctx, "UPDATE deployments SET commit_sha = ?, commit_message = ? WHERE id = ?", d.CommitSHA, d.CommitMessage, d.ID)
	j.e.hub.Event("deployment", d.ID, "running")
	return nil
}

func maskErr(err error, secret string) string {
	return strings.ReplaceAll(err.Error(), secret, "<repository>")
}

func (j *job) domains() []string { return hostsOf(j.routes) }

func (j *job) routeName() string {
	if j.d.PRNumber > 0 {
		return fmt.Sprintf("%s-pr%d", j.app.UUID, j.d.PRNumber)
	}
	return j.app.UUID
}

func (j *job) containerName(replica int) string {
	n := fmt.Sprintf("%s-%d", j.routeName(), j.d.ID)
	if replica > 1 {
		n += "-" + strconv.Itoa(replica)
	}
	return n
}

// writeEnvFile stores runtime variables in a 0600 env-file on the server.
func (j *job) writeEnvFile(ctx context.Context) (string, error) {
	dir := path.Join(j.e.cfg.DataDir, "apps", j.app.UUID)
	file := dir + "/.env"
	if j.d.PRNumber > 0 {
		file = fmt.Sprintf("%s/.env.pr%d", dir, j.d.PRNumber)
	}
	vars := j.env.Runtime
	vars["PORTDOCK_APP"] = j.app.Name
	vars["PORTDOCK_BRANCH"] = j.d.Branch
	vars["PORTDOCK_COMMIT"] = j.d.CommitSHA
	if len(j.routes) > 0 {
		vars["PORTDOCK_FQDN"] = j.routes[0].Host
		vars["PORTDOCK_URL"] = urlOf(j.routes[0])
	}
	// Applications written before the rename may read SHIPYARD_* variables.
	for _, k := range []string{"APP", "BRANCH", "COMMIT", "FQDN", "URL"} {
		if v, ok := vars["PORTDOCK_"+k]; ok {
			vars["SHIPYARD_"+k] = v
		}
	}
	if _, ok := vars["PORT"]; !ok && j.app.Port > 0 {
		vars["PORT"] = strconv.Itoa(j.app.Port)
	}
	// The corporate proxy, unless the application sets its own.
	for k, v := range containerProxyEnv(j.net, j.app.ProxyMode) {
		if _, set := vars[k]; !set {
			vars[k] = v
		}
	}
	if err := executor.WriteFile(ctx, j.x, file, []byte(envFile(vars)), "0600"); err != nil {
		return "", err
	}
	return file, nil
}

// envFile renders KEY=value lines (docker --env-file does not support quoting or newlines).
func envFile(vars map[string]string) string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + strings.ReplaceAll(vars[k], "\n", `\n`) + "\n")
	}
	return b.String()
}

// rollout starts the new version, waits until it is healthy, then removes the old one.
func (j *job) rollout(ctx context.Context, image string) error {
	app, d, e := j.app, j.d, j.e
	j.log.Step("Starting new version")
	envFile, err := j.writeEnvFile(ctx)
	if err != nil {
		return err
	}

	old, _ := docker.PS(ctx, j.x, true, "label="+docker.LabelResource+"="+app.UUID, fmt.Sprintf("label=%s=%d", docker.LabelPR, d.PRNumber))

	replicas := max(app.Replicas, 1)
	hostPorts := strings.TrimSpace(app.PortsMapping) != "" && d.PRNumber == 0
	if hostPorts {
		// Host ports can't be bound twice: stop the old version first (short downtime).
		replicas = 1
		if len(old) > 0 {
			j.log.Printf("Host port mappings configured; stopping previous version first (no zero-downtime).")
			for _, c := range old {
				_ = j.x.Run(ctx, "docker rm -f "+q(c.Name), executor.RunOpts{})
			}
			old = nil
		}
	}

	labels := map[string]string{
		docker.LabelManaged:    "true",
		docker.LabelKind:       "application",
		docker.LabelResource:   app.UUID,
		docker.LabelDeployment: d.UUID,
		docker.LabelPR:         strconv.Itoa(d.PRNumber),
	}
	for _, l := range strings.Split(app.CustomLabels, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), "="); ok && k != "" {
			labels[k] = v
		}
	}

	var started []string
	for r := 1; r <= replicas; r++ {
		name := j.containerName(r)
		var b strings.Builder
		fmt.Fprintf(&b, "docker run -d --name %s --restart unless-stopped --network %s --network-alias %s", q(name), q(e.cfg.ProxyNetwork), q(j.routeName()))
		if d.PRNumber == 0 {
			fmt.Fprintf(&b, " --network-alias %s", q(proxy.SafeName(app.Name)))
		}
		fmt.Fprintf(&b, " --env-file %s --log-opt max-size=10m --log-opt max-file=3", q(envFile))
		if app.CPULimit != "" {
			b.WriteString(" --cpus " + q(app.CPULimit))
		}
		if app.MemoryLimit != "" {
			b.WriteString(" --memory " + q(app.MemoryLimit))
		}
		if hostPorts {
			mappings, err := store.NormalizePortMappings(app.PortsMapping)
			if err != nil {
				return fmt.Errorf("port mappings: %w", err)
			}
			for _, p := range store.SplitList(mappings) {
				b.WriteString(" -p " + q(p))
			}
		}
		b.WriteString(proxy.LabelArgs(labels))
		// Validated again here: rows written before the allow-list existed
		// must not reach the shell unchecked.
		opts, err := docker.RunOptions(app.CustomDockerOptions, true)
		if err != nil {
			return err
		}
		b.WriteString(opts)
		b.WriteString(" " + q(image))
		switch {
		case app.StartCommand == "" || app.BuildPack == "nixpacks":
		case app.SourceType == "image":
			// Prebuilt images: pass arguments to the image's entrypoint (like
			// compose `command:`); many images have no shell.
			args, err := executor.SplitArgs(app.StartCommand)
			if err != nil {
				return fmt.Errorf("start command: %w", err)
			}
			for _, a := range args {
				b.WriteString(" " + q(a))
			}
		default:
			b.WriteString(" sh -c " + q(app.StartCommand))
		}
		if err := j.run(ctx, b.String()); err != nil {
			j.removeContainers(started...)
			return fmt.Errorf("start container: %w", err)
		}
		started = append(started, name)
	}

	j.log.Step("Health check")
	for _, name := range started {
		if err := j.waitHealthy(ctx, name); err != nil {
			j.log.Printf("Container logs (%s):", name)
			_ = docker.Logs(context.WithoutCancel(ctx), j.x, name, 50, false, false, j.log)
			j.removeContainers(started...)
			if len(old) > 0 {
				j.log.Printf("Previous version keeps running.")
			}
			return err
		}
	}

	// Switch traffic to the new containers only now that they are healthy.
	routesFile := e.routesPath(app.UUID, d.PRNumber)
	routes := proxy.RenderDynamic(proxy.AppRoutes{Name: j.routeName(), Upstreams: started, Domains: j.routes})
	if err := writeRoutes(ctx, j.x, routesFile, routes); err != nil {
		j.removeContainers(started...)
		return fmt.Errorf("update routing: %w", err)
	}
	if routes != nil {
		j.log.Printf("Routing %d domain(s) to the new version.", len(j.routes))
		if len(old) > 0 {
			// Give Traefik time to load the file before old upstreams disappear
			// (inotify is instant on Linux; bind mounts on Docker Desktop lag).
			select {
			case <-ctx.Done():
			case <-time.After(4 * time.Second):
			}
		}
	}

	if len(old) > 0 {
		j.log.Step("Removing previous version")
		for _, c := range old {
			if c.Labels[docker.LabelDeployment] == d.UUID {
				continue
			}
			j.log.Printf("Stopping %s", c.Name)
			_ = j.x.Run(ctx, fmt.Sprintf("docker stop -t 30 %s >/dev/null 2>&1; docker rm -f %s >/dev/null 2>&1", q(c.Name), q(c.Name)), executor.RunOpts{})
		}
	}
	for _, r := range j.routes {
		j.log.Printf("🌐 Live at %s", urlOf(r))
	}
	return nil
}

func (j *job) removeContainers(names ...string) {
	for _, n := range names {
		_ = j.x.Run(context.Background(), "docker rm -f "+q(n), executor.RunOpts{})
	}
}

// waitHealthy waits for the container to run and pass its health check.
func (j *job) waitHealthy(ctx context.Context, name string) error {
	timeout := time.Duration(max(j.app.HealthCheckTimeout, 10)) * time.Second
	deadline := time.Now().Add(timeout)
	probe := ""
	if p := j.app.HealthCheckPath; p != "" && j.app.Port > 0 {
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		probe = fmt.Sprintf("docker run --rm --network %s curlimages/curl:latest -fsS -o /dev/null --max-time 5 %s",
			q(j.e.cfg.ProxyNetwork), q(fmt.Sprintf("http://%s:%d%s", name, j.app.Port, p)))
		j.log.Printf("Probing http://%s:%d%s (timeout %s)", name, j.app.Port, p, timeout)
	}
	stableSince := time.Time{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		state, health, err := docker.State(ctx, j.x, name)
		if err != nil {
			return fmt.Errorf("inspect container: %w", err)
		}
		switch {
		case state == "exited" || state == "dead":
			return fmt.Errorf("container %s exited during startup", name)
		case state == "restarting":
			return fmt.Errorf("container %s is crash-looping", name)
		case health == "unhealthy":
			return fmt.Errorf("container %s reported unhealthy", name)
		case state == "running" && (health == "" || health == "healthy"):
			if probe != "" {
				if _, err := executor.Output(ctx, j.x, probe); err == nil {
					j.log.Printf("Health check passed.")
					return nil
				}
			} else {
				if stableSince.IsZero() {
					stableSince = time.Now()
				}
				if time.Since(stableSince) >= 5*time.Second {
					j.log.Printf("Container is running.")
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("health check timed out after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// cleanupImages keeps the most recent images of this application for rollbacks.
func (j *job) cleanupImages(ctx context.Context) {
	const keep = 5
	out, err := j.output(ctx, fmt.Sprintf("docker images %s --format '{{.Repository}}:{{.Tag}}'", q(j.imageRepo())))
	if err != nil {
		return
	}
	var imgs []string
	for _, img := range store.SplitList(out) {
		if !versionTagRe.MatchString(img) { // version tags are handled by pruneVersions
			imgs = append(imgs, img)
		}
	}
	if len(imgs) <= keep {
		return
	}
	for _, img := range imgs[keep:] { // docker lists newest first
		if img == j.d.Image {
			continue
		}
		_ = j.x.Run(ctx, "docker rmi "+q(img)+" >/dev/null 2>&1", executor.RunOpts{})
	}
}

// liveStatus derives an application's status from its containers on all servers.
func (e *Engine) liveStatus(ctx context.Context, app store.Application) string {
	filter := "label=" + docker.LabelResource + "=" + app.UUID
	if app.BuildPack == "compose" {
		filter = "label=com.docker.compose.project=" + composeProject(app.UUID)
	}
	cs, err := e.listAppContainers(ctx, app, true, filter)
	if err != nil && len(cs) == 0 {
		return "unknown"
	}
	return aggregateStatus(cs, true)
}

// aggregateStatus reduces container states to one resource status.
func aggregateStatus(cs []docker.Container, mainOnly bool) string {
	running, total := 0, 0
	for _, c := range cs {
		if mainOnly && c.Labels[docker.LabelPR] != "" && c.Labels[docker.LabelPR] != "0" {
			continue
		}
		total++
		if c.State == "running" {
			running++
		}
	}
	switch {
	case total == 0:
		return "stopped"
	case running == total:
		return "running"
	case running == 0:
		return "exited"
	default:
		return "degraded"
	}
}
