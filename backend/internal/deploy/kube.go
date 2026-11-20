package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/kube"
	"github.com/portdock/portdock/internal/proxy"
	"github.com/portdock/portdock/internal/registry"
	"github.com/portdock/portdock/internal/store"
)

// Applications on Kubernetes. A cluster is a server whose executor is a
// *kube.Cluster; an application deployed to it becomes, in the server's
// namespace:
//
//   - a Secret with its variables, one per deployment, so a version that is
//     rolled back keeps the variables it started with;
//   - a Deployment, whose rolling update replaces the health check and traffic
//     switch PortDock does itself on Docker: new pods must be ready before the
//     old ones stop;
//   - a Service named after the resource (pd-<uuid>) and one named after the
//     application, the equivalent of the network aliases on Docker;
//   - an Ingress for its domains, with certificates from cert-manager;
//   - a PersistentVolumeClaim per volume.
//
// The image is built as on Docker, on a Docker server (the application's build
// server, or the one PortDock runs on), and pushed to the application's
// registry, where the cluster pulls it.

const (
	// labelEnv marks the Secrets holding variables, labelAlias the Service
	// named after the application.
	labelEnv   = "portdock.env"
	labelAlias = "portdock.alias"
)

func clusterOf(x executor.Executor) (*kube.Cluster, bool) {
	c, ok := x.(*kube.Cluster)
	return c, ok
}

// kubeTarget returns the cluster among the servers of an application (nil
// when they are all Docker servers).
func kubeTarget(targets []target) (*kube.Cluster, error) {
	var cluster *kube.Cluster
	for _, t := range targets {
		if c, ok := clusterOf(t.x); ok {
			cluster = c
		}
	}
	if cluster != nil && len(targets) > 1 {
		return nil, errors.New("an application on a Kubernetes cluster runs on that cluster only: remove its additional servers (the cluster spreads the replicas over its nodes)")
	}
	return cluster, nil
}

// noAnswerHint explains a pod that never answered its health check on port
// within seconds: most often the application listens on another port, which
// the image may declare (exposed, from EXPOSE).
func noAnswerHint(port, seconds int, exposed []int) string {
	if len(exposed) > 0 && !slices.Contains(exposed, port) {
		return fmt.Sprintf("Nothing answered on port %d within %d s, and the image declares port %s (EXPOSE): set the application's port to %d in Configuration → Network, then deploy again.",
			port, seconds, joinPorts(exposed), exposed[0])
	}
	return fmt.Sprintf("Nothing answered on port %d within %d s: check that the application listens on that port, on all interfaces (0.0.0.0, not 127.0.0.1), or change its port in Configuration → Network. An application that starts slowly needs a longer health check timeout.",
		port, seconds)
}

// ClusterRegistry is the registry an application built from Git, without a
// registry of its own, pushes to on the cluster srv: the cluster's registry,
// where the cluster then pulls it. 0 when there is none, or when the
// application does not build (a prebuilt image chooses its registry itself).
func ClusterRegistry(app store.Application, srv store.Server) int64 {
	if app.RegistryID != 0 || !srv.IsKubernetes() || srv.KubeRegistryID == 0 {
		return 0
	}
	if app.SourceType == "image" || app.BuildPack == "compose" {
		return 0
	}
	return srv.KubeRegistryID
}

// CheckKubeApp refuses the settings an application cannot have on a
// Kubernetes cluster yet.
func CheckKubeApp(app store.Application) error {
	switch {
	case app.BuildPack == "compose":
		return errors.New("Docker Compose applications cannot run on a Kubernetes cluster yet: choose a Docker server")
	case app.PreviewEnabled:
		return errors.New("pull request previews are not available on Kubernetes yet: turn them off or choose a Docker server")
	}
	return nil
}

// kubeBuildTarget is the Docker server that builds (and scans) the images of
// an application running on a cluster.
func (e *Engine) kubeBuildTarget(ctx context.Context, app store.Application) (target, error) {
	id := app.BuildServerID
	if id == 0 || id == app.ServerID {
		if err := e.db.QueryRowContext(ctx, "SELECT id FROM servers WHERE is_local = 1 AND kind != ? ORDER BY id LIMIT 1", store.KindKubernetes).Scan(&id); err != nil {
			return target{}, errors.New("no Docker server to build the image on: choose a build server for the application")
		}
	}
	x, srv, err := e.pool.ForID(ctx, id)
	if err != nil {
		return target{}, fmt.Errorf("connect to build server: %w", err)
	}
	if !docker.IsDocker(x) {
		return target{}, errors.New("the build server of the application must be a Docker server")
	}
	return target{x: x, srv: srv}, nil
}

func kubeLabels(uuid, kind string) map[string]string {
	return map[string]string{docker.LabelManaged: "true", docker.LabelKind: kind, docker.LabelResource: uuid}
}

// kubeSelector identifies the pods of an application's Deployment. Pods of its
// jobs carry the resource label too but not this one, so the Service never
// sends them traffic.
func kubeSelector(uuid string) map[string]string {
	return map[string]string{docker.LabelResource: uuid, docker.LabelPR: "0"}
}

func withLabels(base map[string]string, kv ...string) map[string]string {
	out := make(map[string]string, len(base)+len(kv)/2)
	for k, v := range base {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

func kubeAppLabels(uuid string) map[string]string {
	return withLabels(kubeLabels(uuid, "application"), docker.LabelPR, "0")
}

// secretKeyRe is what Kubernetes accepts as the key of a Secret.
var secretKeyRe = regexp.MustCompile(`^[-._a-zA-Z0-9]+$`)

// serviceNameRe is a name a Service can have (and other pods can resolve).
var serviceNameRe = regexp.MustCompile(`^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$`)

// kubePorts lists the ports of the application's container: its own first
// (the one probed), then those its domains target.
func kubePorts(app store.Application, routes []proxy.DomainRoute) []int {
	var ports []int
	add := func(p int) {
		if p <= 0 {
			return
		}
		for _, have := range ports {
			if have == p {
				return
			}
		}
		ports = append(ports, p)
	}
	add(app.Port)
	for _, r := range routes {
		add(r.Port)
	}
	return ports
}

// kubeProtected reports whether app restricts who reaches it. The proxy
// enforces that on Docker; on Kubernetes nothing does yet, so such an
// application gets no Ingress at all rather than an open one.
func (e *Engine) kubeProtected(ctx context.Context, app store.Application) (bool, error) {
	p, err := e.db.Protection(ctx, app.ID)
	if err != nil {
		return true, err
	}
	return p.Auth != "" || strings.TrimSpace(p.IPAllow) != "" || p.RateAverage > 0, nil
}

var errKubeProtection = errors.New("access protection (single sign-on, password, allowed addresses, rate limit) is not enforced on Kubernetes yet: " +
	"the application is not published; remove the protection or deploy it on a Docker server")

// kubeIngress renders the Ingress of an application (nil without domains).
func (e *Engine) kubeIngress(ctx context.Context, srv store.Server, app store.Application, routes []proxy.DomainRoute, notef func(string, ...any)) (*kube.Object, error) {
	if len(routes) == 0 {
		return nil, nil
	}
	if protected, err := e.kubeProtected(ctx, app); err != nil {
		return nil, fmt.Errorf("load the access protection: %w", err)
	} else if protected {
		return nil, errKubeProtection
	}
	name := kube.AppName(app.UUID)
	rs := make([]kube.Route, 0, len(routes))
	var redirects, custom bool
	for _, r := range routes {
		if r.Port <= 0 {
			return nil, fmt.Errorf("the domain %s has no port: set the port the application listens on", r.Host)
		}
		rs = append(rs, kube.Route{Host: r.Host, Path: r.Path, Service: name, Port: r.Port, TLS: r.HTTPS})
		redirects = redirects || (r.WWW != "" && r.WWW != "none")
		custom = custom || r.Cert == "custom"
	}
	if notef != nil {
		if redirects {
			notef("⚠ www redirects are not applied on Kubernetes yet: add both names as domains.")
		}
		if custom {
			notef("⚠ Uploaded certificates are not used on Kubernetes yet: the ingress controller's certificate is served.")
		}
		if srv.KubeClusterIssuer == "" {
			for _, r := range rs {
				if r.TLS {
					notef("ℹ No cert-manager issuer is set for this cluster: HTTPS domains use the ingress controller's default certificate.")
					break
				}
			}
		}
	}
	ing := kube.Ingress(name, kubeAppLabels(app.UUID), rs, kube.IngressOptions{Class: srv.KubeIngressClass, Issuer: srv.KubeClusterIssuer})
	return &ing, nil
}

// kubeApplyRoutes applies the domains of a deployed application without a
// deployment: its Services and its Ingress.
func (e *Engine) kubeApplyRoutes(ctx context.Context, c *kube.Cluster, srv store.Server, app store.Application, routes []proxy.DomainRoute) error {
	name := kube.AppName(app.UUID)
	if exists, err := c.DeploymentExists(ctx, name); err != nil || !exists {
		return err // routes are applied by the first deployment
	}
	ing, err := e.kubeIngress(ctx, srv, app, routes, nil)
	if err != nil || ing == nil {
		// No domain left, or one that must not be served: nothing is published.
		if derr := c.DeleteNamed(ctx, "ingress", name); derr != nil && err == nil {
			err = derr
		}
		if err != nil {
			return err
		}
	}
	objects := e.kubeServices(ctx, c, app, kubePorts(app, routes), nil)
	if ing != nil {
		objects = append(objects, *ing)
	}
	return c.Apply(ctx, objects...)
}

// kubeServices renders the Services of an application: pd-<uuid>, and one
// named after the application when no other resource of the namespace
// answers to that name.
func (e *Engine) kubeServices(ctx context.Context, c *kube.Cluster, app store.Application, ports []int, notef func(string, ...any)) []kube.Object {
	if len(ports) == 0 {
		return nil
	}
	name, labels, sel := kube.AppName(app.UUID), kubeAppLabels(app.UUID), kubeSelector(app.UUID)
	objects := []kube.Object{kube.Service(name, labels, sel, ports)}
	alias := proxy.SafeName(app.Name)
	if alias == name || !serviceNameRe.MatchString(alias) {
		return objects
	}
	owner, exists, err := c.Label(ctx, "service", alias, docker.LabelResource)
	switch {
	case err != nil:
	case exists && owner != app.UUID:
		if notef != nil {
			notef("ℹ Another resource of the namespace is already reachable as %q: other applications reach this one as %q.", alias, name)
		}
	default:
		objects = append(objects, kube.Service(alias, withLabels(labels, labelAlias, "true"), sel, ports))
	}
	return objects
}

// kubeDeploy rolls image out as the new version of the application on c.
func (j *job) kubeDeploy(ctx context.Context, c *kube.Cluster, srv store.Server, image string, pullAlways bool) error {
	app, d, e, log := j.app, j.d, j.e, j.log
	name := kube.AppName(app.UUID)
	labels := kubeAppLabels(app.UUID)

	if protected, err := e.kubeProtected(ctx, app); err != nil {
		return fmt.Errorf("load the access protection: %w", err)
	} else if protected && len(j.routes) > 0 {
		return errKubeProtection
	}
	if app.CanaryPercent > 0 {
		log.Printf("ℹ Canary deployments are not available on Kubernetes yet: pods are replaced one by one, each only once the new one is ready.")
	}
	if strings.TrimSpace(app.PortsMapping) != "" {
		log.Printf("⚠ Host port mappings do not apply on Kubernetes and are ignored.")
	}
	if strings.TrimSpace(app.CustomDockerOptions) != "" || strings.TrimSpace(app.CustomLabels) != "" {
		log.Printf("⚠ Custom Docker options and labels do not apply on Kubernetes and are ignored.")
	}

	w := kube.Workload{
		Name: name, Selector: kubeSelector(app.UUID), Labels: labels,
		PodLabels: map[string]string{docker.LabelDeployment: d.UUID},
		Image:     image, PullAlways: pullAlways, Replicas: max(app.Replicas, 1),
		Ports: kubePorts(app, j.routes), HealthPath: app.HealthCheckPath, StartupSeconds: max(app.HealthCheckTimeout, 10),
	}
	var err error
	if w.CPU, err = kube.CPUQuantity(app.CPULimit); err != nil {
		return err
	}
	if w.Memory, err = kube.MemoryQuantity(app.MemoryLimit); err != nil {
		return err
	}
	switch {
	case app.StartCommand == "" || app.BuildPack == "nixpacks":
	case app.SourceType == "image":
		// Prebuilt images: arguments of the image's entrypoint, as on Docker.
		if w.Args, err = executor.SplitArgs(app.StartCommand); err != nil {
			return fmt.Errorf("start command: %w", err)
		}
	default:
		w.Command = []string{"sh", "-c", app.StartCommand}
	}

	log.Step("Preparing the cluster")
	// Variables: a Secret of this deployment.
	env := map[string]string{}
	for k, v := range j.runtimeVars(ctx) {
		if !secretKeyRe.MatchString(k) {
			log.Printf("⚠ Variable %q is left out: on Kubernetes names may hold letters, digits, '_', '-' and '.'.", k)
			continue
		}
		env[k] = v
	}
	w.EnvSecret = fmt.Sprintf("%s-%d", kube.EnvSecretName(app.UUID), d.ID)
	first := []kube.Object{kube.Secret(w.EnvSecret, withLabels(labels, labelEnv, "true"), env)}

	// Registry credentials, for the cluster to pull the image.
	if j.reg != nil {
		user, pass, err := registry.Credentials(ctx, *j.reg, e.box.MustDecrypt(j.reg.Password))
		if err != nil {
			return err
		}
		if user != "" || pass != "" {
			log.Mask(pass)
			w.PullSecret = kube.PullSecretName(j.reg.ID)
			first = append(first, kube.PullSecret(w.PullSecret, map[string]string{docker.LabelManaged: "true"}, j.reg.Host, user, pass))
		}
	}

	// Persistent storage: claims are created once and then left alone.
	vols, err := e.db.Volumes(ctx, app.ID)
	if err != nil {
		return fmt.Errorf("load volumes: %w", err)
	}
	if len(vols) > 0 {
		have := map[string]bool{}
		names, err := c.Names(ctx, "persistentvolumeclaim", docker.LabelResource+"="+app.UUID)
		if err != nil {
			return fmt.Errorf("list the volumes: %w", err)
		}
		for _, n := range names {
			have[n] = true
		}
		size := srv.KubeVolumeSize
		if size == "" {
			size = DefaultKubeVolumeSize
		}
		for _, v := range vols {
			if v.HostPath != "" {
				return fmt.Errorf("volume %s mounts a directory of the server, which a Kubernetes cluster does not have: use a volume without a server path", v.Name)
			}
			claim := kube.ClaimName(app.UUID, v.Name)
			if !have[claim] {
				first = append(first, kube.Claim(claim, withLabels(kubeLabels(app.UUID, "application"), docker.LabelVolume, v.Name), size, srv.KubeStorageClass))
				log.Printf("Creating volume %s (%s).", v.Name, size)
			}
			w.Mounts = append(w.Mounts, kube.Mount{Claim: claim, Path: v.MountPath, ReadOnly: v.ReadOnly})
		}
		log.Printf("Persistent storage: %d volume(s). The previous version stops before the new one starts, because they cannot share a volume across nodes.", len(vols))
	}
	if err := c.Apply(ctx, first...); err != nil {
		return fmt.Errorf("prepare the cluster: %w", err)
	}
	// Whatever happens next, a Secret no version uses must not stay behind.
	rolledOut := false
	defer func() {
		if !rolledOut {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			_ = c.DeleteNamed(cleanup, "secret", w.EnvSecret)
		}
	}()

	if app.PreDeployCommand != "" {
		log.Step("Pre-deploy command")
		pre := w
		pre.Labels, pre.PodLabels = kubeLabels(app.UUID, "job"), nil
		pre.Command, pre.Args, pre.Mounts = []string{"sh", "-c", app.PreDeployCommand}, nil, nil
		if err := c.RunJob(ctx, kube.Job(fmt.Sprintf("%s-pre-%d", name, d.ID), pre, 30*time.Minute), 30*time.Minute, log); err != nil {
			return fmt.Errorf("pre-deploy command failed: %w", err)
		}
	}

	log.Step("Starting new version")
	existed, err := c.DeploymentExists(ctx, name)
	if err != nil {
		return err
	}
	objects := append([]kube.Object{kube.Deployment(w)}, e.kubeServices(ctx, c, app, w.Ports, log.Printf)...)
	if err := c.Apply(ctx, objects...); err != nil {
		return fmt.Errorf("apply the deployment: %w", err)
	}

	log.Step("Health check")
	if len(w.Ports) == 0 {
		log.Printf("The application has no port: pods count as ready once they run.")
	} else if w.HealthPath != "" {
		log.Printf("Probing %s on port %d of each pod (up to %ds).", w.HealthPath, w.Ports[0], w.StartupSeconds)
	} else {
		log.Printf("Probing port %d of each pod (up to %ds).", w.Ports[0], w.StartupSeconds)
	}
	newPods := kube.Selector(map[string]string{docker.LabelResource: app.UUID, docker.LabelDeployment: d.UUID})
	timeout := time.Duration(w.StartupSeconds+60)*time.Second*time.Duration(w.Replicas) + 5*time.Minute
	if err := c.Rollout(ctx, name, newPods, timeout, log); err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		c.PodProblems(cleanup, newPods, log)
		if existed {
			if uerr := c.RolloutUndo(cleanup, name); uerr != nil {
				log.Printf("⚠ Could not return to the previous version: %v", uerr)
			} else {
				log.Printf("Previous version keeps running.")
			}
		} else {
			_ = c.DeleteNamed(cleanup, "deployment", name)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, kube.ErrNoAnswer) && len(w.Ports) > 0 {
			hint := noAnswerHint(w.Ports[0], w.StartupSeconds, j.exposed)
			log.Printf("⚠ %s", hint)
			return fmt.Errorf("the new version did not become ready: %w. %s", err, hint)
		}
		return fmt.Errorf("the new version did not become ready: %w", err)
	}
	rolledOut = true
	log.Printf("Health check passed: %s running the new version.", plural(w.Replicas, "pod"))

	// The new version serves: what follows finishes even if the deployment is cancelled.
	ctx = context.WithoutCancel(ctx)
	ing, err := e.kubeIngress(ctx, srv, app, j.routes, log.Printf)
	if err != nil {
		return err
	}
	if ing != nil {
		if err := c.Apply(ctx, *ing); err != nil {
			return fmt.Errorf("update routing: %w", err)
		}
		log.Printf("Routing %d domain(s) to the new version.", len(j.routes))
	} else if err := c.DeleteNamed(ctx, "ingress", name); err != nil {
		log.Printf("⚠ Could not remove the previous routing: %v", err)
	}
	j.kubeTidy(ctx, c, w.EnvSecret)

	if app.PostDeployCommand != "" {
		log.Step("Post-deploy command")
		if err := c.Exec(ctx, name, log, "sh", "-c", app.PostDeployCommand); err != nil {
			log.Printf("⚠ post-deploy command failed: %v", err)
		}
	}
	for _, r := range j.routes {
		log.Printf("🌐 Live at %s", urlOf(r))
	}
	return nil
}

// DefaultKubeVolumeSize is the size of an application volume on a cluster
// that does not set one.
const DefaultKubeVolumeSize = "5Gi"

// kubeTidy removes what earlier deployments left: the Secrets of variables
// other than the current and the previous one, and a Service named after a
// name the application no longer has.
func (j *job) kubeTidy(ctx context.Context, c *kube.Cluster, current string) {
	uuid := j.app.UUID
	if names, err := c.Names(ctx, "secret", docker.LabelResource+"="+uuid+","+labelEnv+"=true"); err == nil {
		for _, n := range staleEnvSecrets(names, current) {
			_ = c.DeleteNamed(ctx, "secret", n)
		}
	}
	alias := proxy.SafeName(j.app.Name)
	if names, err := c.Names(ctx, "service", docker.LabelResource+"="+uuid+","+labelAlias+"=true"); err == nil {
		for _, n := range names {
			if n != alias {
				_ = c.DeleteNamed(ctx, "service", n)
			}
		}
	}
}

// staleEnvSecrets picks, among the Secrets of variables of an application
// (named …-env-<deployment id>), those to delete: all but current and the
// newest other one, which the previous version still reads.
func staleEnvSecrets(names []string, current string) []string {
	id := func(n string) int {
		v, _ := strconv.Atoi(n[strings.LastIndexByte(n, '-')+1:])
		return v
	}
	var others []string
	for _, n := range names {
		if n != current {
			others = append(others, n)
		}
	}
	sort.Slice(others, func(a, b int) bool { return id(others[a]) > id(others[b]) })
	if len(others) <= 1 {
		return nil
	}
	return others[1:]
}

// recordKubeVersion saves the deployment as a version. The registry keeps the
// image; a prebuilt image is pinned to the digest the cluster pulled, so the
// version stays what it was when its tag moves.
func (j *job) recordKubeVersion(ctx context.Context, c *kube.Cluster, image string) {
	e, app := j.e, j.app
	if app.SourceType == "image" {
		sel := kube.Selector(map[string]string{docker.LabelResource: app.UUID, docker.LabelDeployment: j.d.UUID})
		if pinned := c.ImageDigest(ctx, sel); pinned != "" {
			image = pinned
		}
	}
	n := e.nextVersionNumber(ctx, app.ID)
	v := store.AppVersion{ApplicationID: app.ID, Number: n, DeploymentID: j.d.ID, CommitSHA: j.d.CommitSHA, CommitMessage: j.d.CommitMessage, Branch: j.d.Branch, Image: image}
	if err := e.insertVersion(ctx, &v); err != nil {
		j.log.Printf("⚠ could not record the version: %v", err)
		return
	}
	j.d.VersionID = v.ID
	j.log.Printf("📦 Version %s saved (%s).", versionTag(n), image)
	e.pruneVersions(ctx, app, v.ID)
}

// kubeAppAction starts (scales up), stops (scales to zero) or restarts the
// pods of an application. It reports whether the application is deployed.
func (e *Engine) kubeAppAction(ctx context.Context, c *kube.Cluster, app store.Application, action string) (bool, error) {
	name := kube.AppName(app.UUID)
	exists, err := c.DeploymentExists(ctx, name)
	if err != nil || !exists {
		return false, err
	}
	switch action {
	case "stop":
		return true, c.Scale(ctx, name, 0)
	case "start":
		err = c.Scale(ctx, name, max(app.Replicas, 1))
	case "restart":
		err = c.Restart(ctx, name)
	default:
		return true, fmt.Errorf("unknown action %q", action)
	}
	if err != nil {
		return true, err
	}
	// So the status read right after reflects the pods that came up.
	wait, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	_ = c.RolloutStatus(wait, name, time.Minute, io.Discard)
	return true, nil
}

// kubeRemoveApp deletes everything of an application but its volumes.
func kubeRemoveApp(ctx context.Context, c *kube.Cluster, uuid string) error {
	return c.Delete(ctx, []string{"ingress", "service", "deployment", "job", "secret"}, docker.LabelResource+"="+uuid)
}

// kubeRemoveVolumes deletes the volumes of an application matching the extra
// label filters (all of them without any). A claim still mounted is removed by
// the cluster once its pod is gone.
func kubeRemoveVolumes(ctx context.Context, c *kube.Cluster, uuid string, filters ...string) error {
	return c.Delete(ctx, []string{"persistentvolumeclaim"}, strings.Join(append([]string{docker.LabelResource + "=" + uuid}, filters...), ","))
}

// kubeJob runs a scheduled job in a pod like those of the application: same
// image, variables and volumes as the version that is deployed.
func (e *Engine) kubeJob(ctx context.Context, c *kube.Cluster, j store.AppJob, app store.Application, run store.JobRun, timeout time.Duration, out io.Writer) error {
	name := kube.AppName(app.UUID)
	tmpl, err := c.Template(ctx, name)
	if err != nil {
		return fmt.Errorf("the application is not deployed on the cluster: %w", err)
	}
	w := kube.Workload{
		Labels: kubeLabels(app.UUID, "job"), Image: tmpl.Image, EnvSecret: tmpl.EnvSecret, PullSecret: tmpl.PullSecret,
		Mounts: tmpl.Mounts, Command: []string{"sh", "-c", j.Command},
	}
	if w.CPU, err = kube.CPUQuantity(app.CPULimit); err != nil {
		return err
	}
	if w.Memory, err = kube.MemoryQuantity(app.MemoryLimit); err != nil {
		return err
	}
	return c.RunJob(ctx, kube.Job(fmt.Sprintf("%s-job%d-%d", name, j.ID, run.ID), w, timeout), timeout, out)
}

// checkCluster is ValidateServer for a cluster: it answers, and when it was
// not known to work, its namespace exists and the credentials can deploy to it.
func (e *Engine) checkCluster(ctx context.Context, c *kube.Cluster, srv *store.Server, full bool) error {
	srv.ProxyStatus = "disabled"
	v, err := c.Version(ctx)
	if err != nil {
		srv.DockerVersion = ""
		return fmt.Errorf("the cluster does not answer: %w", err)
	}
	srv.DockerVersion = v
	if !full {
		return nil
	}
	if err := c.EnsureNamespace(ctx); err != nil {
		return err
	}
	return c.CanDeploy(ctx)
}

// setupCluster checks what applications need from a cluster and reports it.
// Nothing is installed: an ingress controller and cert-manager belong to
// whoever runs the cluster.
func (e *Engine) setupCluster(ctx context.Context, srv store.Server, c *kube.Cluster, log *topicLog) error {
	fail := func(err error) error {
		log.Printf("❌ %v", err)
		e.hub.Publish(ServerTopic(srv.ID), "status", "failed")
		return err
	}
	log.Printf("━━━ Connecting to the cluster")
	v, err := c.Version(ctx)
	if err != nil {
		return fail(fmt.Errorf("the cluster does not answer: %w", err))
	}
	log.Printf("Kubernetes %s", v)

	log.Printf("━━━ Namespace %s", c.Namespace)
	if err := c.EnsureNamespace(ctx); err != nil {
		return fail(err)
	}
	if err := c.CanDeploy(ctx); err != nil {
		return fail(err)
	}
	log.Printf("The credentials can deploy to the namespace.")

	log.Printf("━━━ Ingress controller")
	classes := c.IngressClasses(ctx)
	switch {
	case len(classes) == 0:
		log.Printf("⚠ No ingress class found (or the credentials cannot list them). Domains need an ingress controller, e.g. ingress-nginx or Traefik.")
	case srv.KubeIngressClass == "":
		log.Printf("Ingress classes: %s. Applications use the cluster's default class; set one on the server to choose.", strings.Join(classes, ", "))
	default:
		found := false
		for _, cl := range classes {
			found = found || cl == srv.KubeIngressClass
		}
		if found {
			log.Printf("Ingress class %s.", srv.KubeIngressClass)
		} else {
			log.Printf("⚠ The ingress class %q does not exist (found: %s).", srv.KubeIngressClass, strings.Join(classes, ", "))
		}
	}

	log.Printf("━━━ Certificates")
	switch {
	case !c.HasResource(ctx, "clusterissuers.cert-manager.io"):
		log.Printf("⚠ cert-manager is not installed (or cannot be seen): HTTPS domains use the ingress controller's default certificate.")
	case srv.KubeClusterIssuer == "":
		log.Printf("cert-manager is installed. Set a ClusterIssuer on the server to get a certificate for every HTTPS domain.")
	default:
		log.Printf("cert-manager is installed; certificates come from the ClusterIssuer %s.", srv.KubeClusterIssuer)
	}

	log.Printf("━━━ Metrics")
	if c.HasResource(ctx, "pods.metrics.k8s.io") {
		log.Printf("The metrics API is available: CPU and memory of pods are shown.")
	} else {
		log.Printf("ℹ metrics-server is not installed: CPU and memory of pods are not shown.")
	}

	_, _ = e.ValidateServer(ctx, srv)
	log.Printf("✅ The cluster is ready.")
	e.hub.Publish(ServerTopic(srv.ID), "status", "success")
	return nil
}
