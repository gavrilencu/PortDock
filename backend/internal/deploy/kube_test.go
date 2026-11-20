package deploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/portdock/portdock/internal/config"
	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/kube"
	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
	"github.com/portdock/portdock/internal/stream"
)

// fakeKube is a scripted cluster: it keeps what is applied and answers
// kubectl the way a cluster would, so deployments run through the engine
// from the queue to the stored result.
type fakeKube struct {
	mu          sync.Mutex
	objects     map[string]map[string]any // "Kind/name"
	calls       []string
	failRollout bool // new pods crash; the rollout never completes
}

var kubeKinds = map[string]string{
	"deployment": "Deployment", "service": "Service", "secret": "Secret", "ingress": "Ingress",
	"persistentvolumeclaim": "PersistentVolumeClaim", "job": "Job",
}

func labelsOf(o map[string]any) map[string]string {
	out := map[string]string{}
	meta, _ := o["metadata"].(map[string]any)
	ls, _ := meta["labels"].(map[string]any)
	for k, v := range ls {
		out[k], _ = v.(string)
	}
	return out
}

func matches(labels map[string]string, sel string) bool {
	for _, part := range strings.Split(sel, ",") {
		k, v, _ := strings.Cut(part, "=")
		if part != "" && labels[k] != v {
			return false
		}
	}
	return true
}

func arg(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func (f *fakeKube) names(kind, sel string) []string {
	var names []string
	for key, o := range f.objects {
		if k, n, _ := strings.Cut(key, "/"); k == kind && matches(labelsOf(o), sel) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// pods are derived from the Deployments: as many as their replicas, with the
// labels of the pod template.
func (f *fakeKube) pods(sel string) []map[string]any {
	var pods []map[string]any
	for _, name := range f.names("Deployment", "") {
		d := f.objects["Deployment/"+name]
		spec, _ := d["spec"].(map[string]any)
		tmpl, _ := spec["template"].(map[string]any)
		replicas, _ := spec["replicas"].(float64)
		if !matches(labelsOf(tmpl), sel) {
			continue
		}
		tspec, _ := tmpl["spec"].(map[string]any)
		containers, _ := tspec["containers"].([]any)
		image, _ := containers[0].(map[string]any)["image"].(string)
		for i := 0; i < int(replicas); i++ {
			status := map[string]any{"phase": "Running", "podIP": "10.42.0.9",
				"containerStatuses": []any{map[string]any{"name": "app", "ready": true, "imageID": "docker.io/library/" + strings.Split(image, ":")[0] + "@sha256:feed", "state": map[string]any{"running": map[string]any{}}}}}
			if f.failRollout {
				status["containerStatuses"] = []any{map[string]any{"name": "app", "ready": false, "restartCount": 4,
					"state": map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff", "message": "back-off restarting failed container"}}}}
			}
			pods = append(pods, map[string]any{
				"metadata": map[string]any{"name": fmt.Sprintf("%s-%d", name, i), "labels": tmpl["metadata"].(map[string]any)["labels"]},
				"spec":     tspec, "status": status,
			})
		}
	}
	return pods
}

func (f *fakeKube) run(_ context.Context, in io.Reader, out, errw io.Writer, args ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(args) >= 2 && args[0] == "--namespace" {
		args = args[2:]
	}
	f.calls = append(f.calls, strings.Join(args, " "))
	write := func(v any) error {
		if s, ok := v.(string); ok {
			_, _ = io.WriteString(out, s)
			return nil
		}
		b, _ := json.Marshal(v)
		_, _ = out.Write(b)
		return nil
	}
	notFound := func(what string) error {
		_, _ = fmt.Fprintf(errw, "Error from server (NotFound): %s not found", what)
		return errors.New("exit status 1")
	}
	sel := arg(args, "-l")
	switch {
	case args[0] == "get" && args[1] == "--raw":
		return write(`{"gitVersion":"v1.31.2"}`)
	case args[0] == "auth":
		return write("yes\n")
	case args[0] == "apply":
		var list struct {
			Items []map[string]any `json:"items"`
		}
		body, _ := io.ReadAll(in)
		if err := json.Unmarshal(body, &list); err != nil {
			return err
		}
		for _, o := range list.Items {
			kind, _ := o["kind"].(string)
			name, _ := o["metadata"].(map[string]any)["name"].(string)
			f.objects[kind+"/"+name] = o
		}
		return nil
	case args[0] == "rollout" && args[1] == "status":
		if f.failRollout {
			_, _ = io.WriteString(errw, "error: timed out waiting for the condition")
			return errors.New("exit status 1")
		}
		return write("deployment successfully rolled out\n")
	case args[0] == "rollout" && args[1] == "undo":
		f.failRollout = false // the previous pods are back
		return nil
	case args[0] == "scale":
		name := strings.TrimPrefix(args[1], "deployment/")
		var n float64
		_, _ = fmt.Sscanf(args[2], "--replicas=%g", &n)
		f.objects["Deployment/"+name]["spec"].(map[string]any)["replicas"] = n
		return nil
	case args[0] == "delete" && sel != "":
		for _, kind := range strings.Split(args[1], ",") {
			for _, n := range f.names(kubeKinds[kind], sel) {
				delete(f.objects, kubeKinds[kind]+"/"+n)
			}
		}
		return nil
	case args[0] == "delete":
		delete(f.objects, kubeKinds[args[1]]+"/"+args[2])
		return nil
	case args[0] == "get" && args[1] == "namespace":
		return write("namespace/apps\n")
	case args[0] == "get" && args[1] == "pods" && strings.Contains(strings.Join(args, " "), "imageID"):
		var ids []string
		for _, p := range f.pods(sel) {
			ids = append(ids, p["status"].(map[string]any)["containerStatuses"].([]any)[0].(map[string]any)["imageID"].(string))
		}
		return write(strings.Join(ids, "\n"))
	case args[0] == "get" && args[1] == "pods":
		return write(map[string]any{"items": f.pods(sel)})
	case args[0] == "get" && args[1] == "job":
		return write(`{"status":{"succeeded":1}}`)
	case args[0] == "get" && args[1] == "events":
		return nil
	case args[0] == "get" && sel != "" && strings.HasPrefix(arg(args, "-o"), "jsonpath"):
		return write(strings.Join(f.names(kubeKinds[args[1]], sel), "\n"))
	case args[0] == "get" && sel != "":
		items := []any{}
		for _, n := range f.names(kubeKinds[args[1]], sel) {
			items = append(items, f.objects[kubeKinds[args[1]]+"/"+n])
		}
		return write(map[string]any{"items": items})
	case args[0] == "get":
		o, ok := f.objects[kubeKinds[args[1]]+"/"+args[2]]
		if !ok {
			return notFound(args[1] + " " + args[2])
		}
		if arg(args, "-o") == "name" {
			return write(args[1] + "/" + args[2] + "\n")
		}
		return write(o)
	}
	return nil // logs, exec, rollout restart
}

func (f *fakeKube) called(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// index of the first call starting with prefix (-1 when there is none).
func (f *fakeKube) index(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

func (f *fakeKube) object(key string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.objects[key]
}

type kubeFixture struct {
	t   *testing.T
	e   *Engine
	k   *fakeKube
	srv store.Server
	env store.Environment
}

func newKubeFixture(t *testing.T) *kubeFixture {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	box, _ := secrets.LoadOrCreate("", dir)
	k := &fakeKube{objects: map[string]map[string]any{}}
	pool := executor.NewPool(db, box)
	pool.Cluster = func(srv store.Server, _ string) (executor.Executor, error) {
		return kube.NewWithRunner(srv.KubeNamespace, k.run), nil
	}
	e := New(config.Config{DataDir: filepath.ToSlash(dir), ProxyNetwork: "portdock", PublicURL: "http://localhost:8000"}, db, box, pool, stream.NewHub(), notify.New(db, box), nil)
	ctx := context.Background()
	f := &kubeFixture{t: t, e: e, k: k}
	f.srv = store.Server{Name: "prod-cluster", Kind: store.KindKubernetes, Host: "https://10.0.0.1:6443", KubeNamespace: "apps",
		KubeIngressClass: "nginx", KubeClusterIssuer: "letsencrypt", Status: "reachable", CreatedAt: store.Now()}
	project := store.Project{Name: "shop", CreatedAt: store.Now()}
	for _, row := range []struct {
		table string
		item  any
	}{{"servers", &f.srv}, {"projects", &project}} {
		if _, err := store.Insert(ctx, db, row.table, row.item); err != nil {
			t.Fatal(err)
		}
	}
	f.env = store.Environment{ProjectID: project.ID, Name: "production", CreatedAt: store.Now()}
	if _, err := store.Insert(ctx, db, "environments", &f.env); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *kubeFixture) app(a store.Application, domains ...string) store.Application {
	f.t.Helper()
	ctx := context.Background()
	a.UUID, a.EnvironmentID, a.ServerID = secrets.RandomToken(8), f.env.ID, f.srv.ID
	a.CreatedAt, a.UpdatedAt = store.Now(), store.Now()
	if a.Replicas == 0 {
		a.Replicas = 1
	}
	if _, err := store.Insert(ctx, f.e.db, "applications", &a); err != nil {
		f.t.Fatal(err)
	}
	for _, host := range domains {
		d := store.AppDomain{ApplicationID: a.ID, Host: host, HTTPS: true, WWWRedirect: "none", CertMode: "auto", CreatedAt: store.Now()}
		if _, err := store.Insert(ctx, f.e.db, "app_domains", &d); err != nil {
			f.t.Fatal(err)
		}
	}
	return a
}

// deploy runs one deployment to its end and returns it, with its log.
func (f *kubeFixture) deploy(a store.Application, o Options) store.Deployment {
	f.t.Helper()
	ctx := context.Background()
	d, err := f.e.Enqueue(ctx, a, o)
	if err != nil {
		f.t.Fatal(err)
	}
	f.e.process(ctx, d.ID)
	d, err = store.ByID[store.Deployment](ctx, f.e.db, "deployments", d.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return d
}

func nginxApp() store.Application {
	return store.Application{Name: "Shop Web", SourceType: "image", BuildPack: "image", DockerImage: "nginx:1.27", Port: 80,
		Replicas: 2, HealthCheckPath: "/healthz", HealthCheckTimeout: 60, CPULimit: "0.5", MemoryLimit: "256m"}
}

func at(t *testing.T, o any, path ...any) any {
	t.Helper()
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := o.(map[string]any)
			if !ok {
				t.Fatalf("no object at %v of %v", p, path)
			}
			o = m[k]
		case int:
			l, ok := o.([]any)
			if !ok || k >= len(l) {
				t.Fatalf("no element %d at %v", k, path)
			}
			o = l[k]
		}
	}
	return o
}

func TestKubeDeployImageApplication(t *testing.T) {
	f := newKubeFixture(t)
	ctx := context.Background()
	a := f.app(nginxApp(), "shop.example.com")
	if _, err := f.e.db.ExecContext(ctx, "INSERT INTO env_vars(owner_type, owner_id, key, value, created_at) VALUES('application', ?, 'LOG_LEVEL', ?, ?)",
		a.ID, f.e.box.Encrypt("debug"), store.Now()); err != nil {
		t.Fatal(err)
	}

	d := f.deploy(a, Options{})
	if d.Status != "success" {
		t.Fatalf("deployment %s:\n%s", d.Status, d.Logs)
	}
	name := kube.AppName(a.UUID)

	dep := f.k.object("Deployment/" + name)
	if dep == nil {
		t.Fatalf("no Deployment; calls: %v", f.k.calls)
	}
	c := at(t, dep, "spec", "template", "spec", "containers", 0)
	if at(t, c, "image") != "nginx:1.27" || at(t, c, "imagePullPolicy") != "Always" || at(t, dep, "spec", "replicas") != float64(2) {
		t.Fatalf("container: %v", c)
	}
	if at(t, c, "resources", "limits", "cpu") != "500m" || at(t, c, "resources", "limits", "memory") != "268435456" ||
		at(t, c, "readinessProbe", "httpGet", "path") != "/healthz" || at(t, c, "readinessProbe", "httpGet", "port") != float64(80) {
		t.Fatalf("limits and probe: %v", c)
	}
	if at(t, dep, "spec", "template", "metadata", "labels", docker.LabelDeployment) != d.UUID {
		t.Fatal("pods must carry their deployment")
	}

	// Variables: a Secret of this deployment, read by the pods.
	secretName := fmt.Sprintf("%s-env-%d", name, d.ID)
	if at(t, c, "envFrom", 0, "secretRef", "name") != secretName {
		t.Fatalf("envFrom: %v", at(t, c, "envFrom"))
	}
	sec := f.k.object("Secret/" + secretName)
	if sec == nil {
		t.Fatal("no Secret with the variables")
	}
	value := func(key string) string {
		enc, _ := at(t, sec, "data").(map[string]any)[key].(string)
		raw, _ := base64.StdEncoding.DecodeString(enc)
		return string(raw)
	}
	if value("LOG_LEVEL") != "debug" || value("PORT") != "80" || value("PORTDOCK_APP") != "Shop Web" || value("PORTDOCK_URL") != "https://shop.example.com" {
		t.Fatalf("variables: %v", at(t, sec, "data"))
	}

	// Reachable by other applications as pd-<uuid> and by its name.
	for _, svc := range []string{name, "shop-web"} {
		s := f.k.object("Service/" + svc)
		if s == nil {
			t.Fatalf("no Service %s", svc)
		}
		if at(t, s, "spec", "selector", docker.LabelResource) != a.UUID || at(t, s, "spec", "selector", docker.LabelPR) != "0" || at(t, s, "spec", "ports", 0, "port") != float64(80) {
			t.Fatalf("service %s: %v", svc, s)
		}
	}

	ing := f.k.object("Ingress/" + name)
	if ing == nil {
		t.Fatal("no Ingress")
	}
	if at(t, ing, "spec", "rules", 0, "host") != "shop.example.com" || at(t, ing, "spec", "ingressClassName") != "nginx" ||
		at(t, ing, "metadata", "annotations", "cert-manager.io/cluster-issuer") != "letsencrypt" ||
		at(t, ing, "spec", "rules", 0, "http", "paths", 0, "backend", "service", "name") != name {
		t.Fatalf("ingress: %v", ing)
	}
	// Traffic is routed only after the pods are ready.
	rollout, apply2 := f.k.index("rollout status deployment/"+name), -1
	f.k.mu.Lock()
	for i, call := range f.k.calls {
		if strings.HasPrefix(call, "apply") && i > rollout {
			apply2 = i
		}
	}
	f.k.mu.Unlock()
	if rollout < 0 || apply2 < 0 {
		t.Fatalf("the Ingress must be applied after the rollout: %v", f.k.calls)
	}

	// The result is stored like any deployment, and saved as a version pinned to a digest.
	app, _ := store.ByID[store.Application](ctx, f.e.db, "applications", a.ID)
	if app.Status != "running" || app.CurrentImage != "nginx:1.27" {
		t.Fatalf("application: %s, %s", app.Status, app.CurrentImage)
	}
	v, err := store.Get[store.AppVersion](ctx, f.e.db, "SELECT * FROM app_versions WHERE application_id = ?", a.ID)
	if err != nil || v.Number != 1 || v.Image != "docker.io/library/nginx@sha256:feed" {
		t.Fatalf("version: %+v, %v", v, err)
	}
	if got := f.e.liveStatus(ctx, app); got != "running" {
		t.Fatalf("live status = %q", got)
	}
	cs, err := f.e.AppContainers(ctx, app)
	if err != nil || len(cs) != 2 || cs[0].State != "running" {
		t.Fatalf("containers: %+v, %v", cs, err)
	}

	// Deploying that version again uses the pinned image, and builds nothing.
	d2 := f.deploy(app, Options{VersionID: v.ID, Trigger: TriggerPromote})
	if d2.Status != "success" {
		t.Fatalf("promote %s:\n%s", d2.Status, d2.Logs)
	}
	c = at(t, f.k.object("Deployment/"+name), "spec", "template", "spec", "containers", 0)
	if at(t, c, "image") != "docker.io/library/nginx@sha256:feed" || at(t, c, "imagePullPolicy") != "IfNotPresent" {
		t.Fatalf("promoted container: %v", c)
	}
}

// Each deployment has its own variables; the previous one's stay for a rollback.
func TestKubeKeepsPreviousVariables(t *testing.T) {
	f := newKubeFixture(t)
	a := f.app(nginxApp())
	var ids []int64
	for i := 0; i < 3; i++ {
		d := f.deploy(a, Options{})
		if d.Status != "success" {
			t.Fatalf("deployment %d %s:\n%s", i, d.Status, d.Logs)
		}
		ids = append(ids, d.ID)
	}
	name := kube.AppName(a.UUID)
	secret := func(id int64) bool { return f.k.object(fmt.Sprintf("Secret/%s-env-%d", name, id)) != nil }
	if secret(ids[0]) || !secret(ids[1]) || !secret(ids[2]) {
		t.Fatalf("expected the secrets of the last two deployments: %v %v %v", secret(ids[0]), secret(ids[1]), secret(ids[2]))
	}
	if f.k.object("Ingress/"+name) != nil {
		t.Fatal("no domain, no Ingress")
	}
}

func TestStaleEnvSecrets(t *testing.T) {
	got := staleEnvSecrets([]string{"pd-a-env-9", "pd-a-env-10", "pd-a-env-2", "pd-a-env-11"}, "pd-a-env-11")
	sort.Strings(got)
	if strings.Join(got, ",") != "pd-a-env-2,pd-a-env-9" {
		t.Fatalf("stale = %v", got)
	}
	if got := staleEnvSecrets([]string{"pd-a-env-1", "pd-a-env-2"}, "pd-a-env-2"); len(got) != 0 {
		t.Fatalf("the previous one stays: %v", got)
	}
}

// A version that does not start leaves the previous one serving, with its variables.
func TestKubeFailedRolloutKeepsPreviousVersion(t *testing.T) {
	f := newKubeFixture(t)
	ctx := context.Background()
	a := f.app(nginxApp(), "shop.example.com")
	first := f.deploy(a, Options{})
	if first.Status != "success" {
		t.Fatalf("first deployment %s:\n%s", first.Status, first.Logs)
	}
	name := kube.AppName(a.UUID)
	ingressBefore := fmt.Sprint(f.k.object("Ingress/" + name))

	f.k.mu.Lock()
	f.k.failRollout = true
	f.k.mu.Unlock()
	app, _ := store.ByID[store.Application](ctx, f.e.db, "applications", a.ID)
	second := f.deploy(app, Options{})
	if second.Status != "failed" {
		t.Fatalf("second deployment %s:\n%s", second.Status, second.Logs)
	}
	for _, want := range []string{"CrashLoopBackOff", "Previous version keeps running", "did not become ready"} {
		if !strings.Contains(second.Logs, want) {
			t.Errorf("the log should mention %q:\n%s", want, second.Logs)
		}
	}
	if f.k.called("rollout undo deployment/"+name) != 1 {
		t.Fatalf("the deployment must be returned to its previous version: %v", f.k.calls)
	}
	if f.k.object(fmt.Sprintf("Secret/%s-env-%d", name, second.ID)) != nil {
		t.Fatal("the variables of the failed deployment must not stay")
	}
	if f.k.object(fmt.Sprintf("Secret/%s-env-%d", name, first.ID)) == nil {
		t.Fatal("the variables of the running version must stay")
	}
	if fmt.Sprint(f.k.object("Ingress/"+name)) != ingressBefore {
		t.Fatal("routing must not change for a version that did not start")
	}
	app, _ = store.ByID[store.Application](ctx, f.e.db, "applications", a.ID)
	if app.Status != "running" {
		t.Fatalf("the previous version still serves: status %q", app.Status)
	}
	var versions int
	_ = f.e.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM app_versions WHERE application_id = ?", a.ID).Scan(&versions)
	if versions != 1 {
		t.Fatalf("a failed deployment is not a version: %d", versions)
	}
}

// A first deployment that fails leaves nothing running.
func TestKubeFailedFirstDeploymentIsRemoved(t *testing.T) {
	f := newKubeFixture(t)
	f.k.failRollout = true
	a := f.app(nginxApp(), "shop.example.com")
	d := f.deploy(a, Options{})
	if d.Status != "failed" {
		t.Fatalf("deployment %s:\n%s", d.Status, d.Logs)
	}
	name := kube.AppName(a.UUID)
	if f.k.object("Deployment/"+name) != nil || f.k.object("Ingress/"+name) != nil || f.k.called("rollout undo") != 0 {
		t.Fatalf("nothing may stay of a first deployment that failed: %v", f.k.calls)
	}
}

// Access protection is not enforced on Kubernetes: such an application must
// not be published at all.
func TestKubeProtectedApplicationIsNotPublished(t *testing.T) {
	f := newKubeFixture(t)
	ctx := context.Background()
	a := f.app(nginxApp(), "admin.example.com")
	if d := f.deploy(a, Options{}); d.Status != "success" {
		t.Fatalf("deployment %s:\n%s", d.Status, d.Logs)
	}
	name := kube.AppName(a.UUID)
	if f.k.object("Ingress/"+name) == nil {
		t.Fatal("no Ingress")
	}
	// Protection added afterwards: the Ingress goes away, with an error saying why.
	if err := f.e.db.SaveProtection(ctx, store.AppProtection{ApplicationID: a.ID, Auth: "sso", SSOAccess: "project"}); err != nil {
		t.Fatal(err)
	}
	app, _ := store.ByID[store.Application](ctx, f.e.db, "applications", a.ID)
	if err := f.e.ApplyRoutes(ctx, app); err == nil || !strings.Contains(err.Error(), "protection") {
		t.Fatalf("ApplyRoutes = %v", err)
	}
	if f.k.object("Ingress/"+name) != nil {
		t.Fatal("a protected application must not stay published without its protection")
	}
	// And a deployment refuses before changing anything.
	applies := f.k.called("apply")
	d := f.deploy(app, Options{})
	if d.Status != "failed" || !strings.Contains(d.Logs, "protection") || f.k.called("apply") != applies {
		t.Fatalf("deployment %s (%d applies, was %d):\n%s", d.Status, f.k.called("apply"), applies, d.Logs)
	}
	// An allow list alone counts too.
	_ = f.e.db.SaveProtection(ctx, store.AppProtection{ApplicationID: a.ID, IPAllow: "10.0.0.0/8"})
	if protected, _ := f.e.kubeProtected(ctx, app); !protected {
		t.Fatal("an allow list is protection")
	}
}

func TestKubeDomainsApplyWithoutDeployment(t *testing.T) {
	f := newKubeFixture(t)
	ctx := context.Background()
	a := f.app(nginxApp())
	if d := f.deploy(a, Options{}); d.Status != "success" {
		t.Fatalf("deployment %s:\n%s", d.Status, d.Logs)
	}
	name := kube.AppName(a.UUID)
	d := store.AppDomain{ApplicationID: a.ID, Host: "api.example.com", Port: 8080, HTTPS: false, WWWRedirect: "none", CertMode: "auto", CreatedAt: store.Now()}
	if _, err := store.Insert(ctx, f.e.db, "app_domains", &d); err != nil {
		t.Fatal(err)
	}
	app, _ := store.ByID[store.Application](ctx, f.e.db, "applications", a.ID)
	if err := f.e.ApplyRoutes(ctx, app); err != nil {
		t.Fatal(err)
	}
	ing := f.k.object("Ingress/" + name)
	if ing == nil || at(t, ing, "spec", "rules", 0, "host") != "api.example.com" || at(t, ing, "spec", "tls") != nil ||
		at(t, ing, "spec", "rules", 0, "http", "paths", 0, "backend", "service", "port", "number") != float64(8080) {
		t.Fatalf("ingress: %v", ing)
	}
	// The Service gains the port the new domain targets.
	ports := at(t, f.k.object("Service/"+name), "spec", "ports").([]any)
	if len(ports) != 2 || at(t, ports[1], "port") != float64(8080) {
		t.Fatalf("service ports: %v", ports)
	}
	// Removing the domain removes the Ingress.
	_, _ = f.e.db.ExecContext(ctx, "DELETE FROM app_domains WHERE application_id = ?", a.ID)
	if err := f.e.ApplyRoutes(ctx, app); err != nil || f.k.object("Ingress/"+name) != nil {
		t.Fatalf("ApplyRoutes = %v, ingress %v", err, f.k.object("Ingress/"+name))
	}
}

func TestKubeVolumesAndPreDeploy(t *testing.T) {
	f := newKubeFixture(t)
	ctx := context.Background()
	app := nginxApp()
	app.PreDeployCommand = "./migrate"
	a := f.app(app)
	v := store.AppVolume{ApplicationID: a.ID, Name: "uploads", MountPath: "/app/uploads", CreatedAt: store.Now()}
	if _, err := store.Insert(ctx, f.e.db, "app_volumes", &v); err != nil {
		t.Fatal(err)
	}
	d := f.deploy(a, Options{})
	if d.Status != "success" {
		t.Fatalf("deployment %s:\n%s", d.Status, d.Logs)
	}
	name := kube.AppName(a.UUID)
	claim := f.k.object("PersistentVolumeClaim/" + name + "-uploads")
	if claim == nil || at(t, claim, "spec", "resources", "requests", "storage") != DefaultKubeVolumeSize || at(t, claim, "metadata", "labels", docker.LabelVolume) != "uploads" {
		t.Fatalf("claim: %v", claim)
	}
	dep := f.k.object("Deployment/" + name)
	if at(t, dep, "spec", "strategy", "type") != "Recreate" || at(t, dep, "spec", "template", "spec", "containers", 0, "volumeMounts", 0, "mountPath") != "/app/uploads" {
		t.Fatalf("deployment with a volume: %v", at(t, dep, "spec"))
	}
	// The pre-deploy command ran as a Job before the new version started, and was removed.
	job, start := f.k.index(fmt.Sprintf("get job %s-pre-%d", name, d.ID)), f.k.index("rollout status")
	if job < 0 || job > start || f.k.object(fmt.Sprintf("Job/%s-pre-%d", name, d.ID)) != nil {
		t.Fatalf("pre-deploy job: at %d, rollout at %d: %v", job, start, f.k.calls)
	}
	if got := f.e.VolumeUsage(ctx, a); len(got) != 1 || len(got[0].Volumes) != 1 || got[0].Volumes[0] != "uploads" {
		t.Fatalf("volume usage: %+v", got)
	}

	// A second deployment leaves the claim alone.
	creates := strings.Count(d.Logs, "Creating volume")
	d2 := f.deploy(a, Options{})
	if d2.Status != "success" || creates != 1 || strings.Contains(d2.Logs, "Creating volume") {
		t.Fatalf("second deployment %s:\n%s", d2.Status, d2.Logs)
	}

	// Deleting the application keeps its data unless asked.
	if err := f.e.DeleteApp(ctx, a, false); err != nil {
		t.Fatal(err)
	}
	if f.k.object("Deployment/"+name) != nil || f.k.object("Service/"+name) != nil || f.k.object("PersistentVolumeClaim/"+name+"-uploads") == nil {
		t.Fatalf("after delete: %v", f.k.objects)
	}
	if err := f.e.DeleteApp(ctx, a, true); err != nil || f.k.object("PersistentVolumeClaim/"+name+"-uploads") != nil {
		t.Fatalf("volumes must go when asked: %v", err)
	}
}

func TestKubeAppActions(t *testing.T) {
	f := newKubeFixture(t)
	ctx := context.Background()
	a := f.app(nginxApp())
	if err := f.e.AppAction(ctx, a, "stop"); err == nil {
		t.Fatal("an application that was never deployed has nothing to stop")
	}
	if d := f.deploy(a, Options{}); d.Status != "success" {
		t.Fatalf("deployment %s:\n%s", d.Status, d.Logs)
	}
	name := kube.AppName(a.UUID)
	if err := f.e.AppAction(ctx, a, "stop"); err != nil {
		t.Fatal(err)
	}
	app, _ := store.ByID[store.Application](ctx, f.e.db, "applications", a.ID)
	if at(t, f.k.object("Deployment/"+name), "spec", "replicas") != float64(0) || app.Status != "stopped" {
		t.Fatalf("stopped: replicas %v, status %q", at(t, f.k.object("Deployment/"+name), "spec", "replicas"), app.Status)
	}
	if err := f.e.AppAction(ctx, a, "start"); err != nil {
		t.Fatal(err)
	}
	app, _ = store.ByID[store.Application](ctx, f.e.db, "applications", a.ID)
	if at(t, f.k.object("Deployment/"+name), "spec", "replicas") != float64(2) || app.Status != "running" {
		t.Fatalf("started: status %q", app.Status)
	}
	if err := f.e.AppAction(ctx, a, "restart"); err != nil || f.k.called("rollout restart deployment/"+name) != 1 {
		t.Fatalf("restart: %v", err)
	}
}

// What is not ported must fail before anything runs, and say why.
func TestKubeRefusals(t *testing.T) {
	f := newKubeFixture(t)
	ctx := context.Background()

	git := f.app(store.Application{Name: "api", SourceType: "git", BuildPack: "dockerfile", GitRepository: "https://git.corp/team/api.git", GitBranch: "main", Port: 3000})
	if d := f.deploy(git, Options{}); d.Status != "failed" || !strings.Contains(d.Logs, "container registry") {
		t.Fatalf("a Git application without a registry: %s\n%s", d.Status, d.Logs)
	}
	compose := f.app(store.Application{Name: "stack", SourceType: "git", BuildPack: "compose", GitRepository: "https://git.corp/team/stack.git", GitBranch: "main"})
	if d := f.deploy(compose, Options{}); d.Status != "failed" || !strings.Contains(d.Logs, "Docker Compose") {
		t.Fatalf("a compose application: %s\n%s", d.Status, d.Logs)
	}
	if d := f.deploy(f.app(nginxApp()), Options{PRNumber: 7, Branch: "feature"}); d.Status != "failed" || !strings.Contains(d.Logs, "previews") {
		t.Fatalf("a preview: %s\n%s", d.Status, d.Logs)
	}
	hostPath := f.app(nginxApp())
	v := store.AppVolume{ApplicationID: hostPath.ID, Name: "etc", MountPath: "/host", HostPath: "/etc", CreatedAt: store.Now()}
	_, _ = store.Insert(ctx, f.e.db, "app_volumes", &v)
	if d := f.deploy(hostPath, Options{}); d.Status != "failed" || !strings.Contains(d.Logs, "directory of the server") {
		t.Fatalf("a server directory: %s\n%s", d.Status, d.Logs)
	}
	if n := f.k.called("apply"); n != 0 {
		t.Fatalf("nothing may be applied for a refused deployment: %d applies", n)
	}

	// A database on a cluster: refused by the executor, nothing runs anywhere.
	db := store.Database{UUID: secrets.RandomToken(8), Name: "pg", Engine: "postgres", Image: "postgres:17", ServerID: f.srv.ID,
		EnvironmentID: f.env.ID, Username: "app", DatabaseName: "app", Password: f.e.box.Encrypt("x"), CreatedAt: store.Now()}
	if _, err := store.Insert(ctx, f.e.db, "databases", &db); err != nil {
		t.Fatal(err)
	}
	if err := f.e.StartDatabase(ctx, db, true); err == nil || !strings.Contains(err.Error(), "Kubernetes") {
		t.Fatalf("StartDatabase = %v", err)
	}
}

// An application built from Git without a registry of its own uses the
// registry of its cluster; one with its own registry, or a prebuilt image,
// keeps its own choice.
func TestClusterRegistryFallback(t *testing.T) {
	srv := store.Server{Kind: store.KindKubernetes, KubeRegistryID: 7}
	for _, c := range []struct {
		name string
		app  store.Application
		srv  store.Server
		want int64
	}{
		{"git, no registry", store.Application{SourceType: "git", BuildPack: "dockerfile"}, srv, 7},
		{"git, own registry", store.Application{SourceType: "git", BuildPack: "dockerfile", RegistryID: 3}, srv, 0},
		{"prebuilt image", store.Application{SourceType: "image", BuildPack: "image"}, srv, 0},
		{"compose", store.Application{SourceType: "git", BuildPack: "compose"}, srv, 0},
		{"cluster without registry", store.Application{SourceType: "git", BuildPack: "dockerfile"}, store.Server{Kind: store.KindKubernetes}, 0},
		{"Docker server", store.Application{SourceType: "git", BuildPack: "dockerfile"}, store.Server{Kind: "docker", KubeRegistryID: 7}, 0},
	} {
		if got := ClusterRegistry(c.app, c.srv); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}

	// In a deployment, the registry check passes with the cluster's registry:
	// what stops this one is the missing Docker server to build on.
	f := newKubeFixture(t)
	reg := store.Registry{Name: "cluster registry", Kind: "generic", Host: "registry.corp", CreatedAt: store.Now()}
	if _, err := store.Insert(context.Background(), f.e.db, "registries", &reg); err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.db.ExecContext(context.Background(), "UPDATE servers SET kube_registry_id = ? WHERE id = ?", reg.ID, f.srv.ID); err != nil {
		t.Fatal(err)
	}
	git := f.app(store.Application{Name: "api", SourceType: "git", BuildPack: "dockerfile", GitRepository: "https://git.corp/team/api.git", GitBranch: "main", Port: 3000})
	d := f.deploy(git, Options{})
	if d.Status != "failed" || strings.Contains(d.Logs, "container registry") || !strings.Contains(d.Logs, "no Docker server to build the image on") {
		t.Fatalf("a Git application on a cluster with a registry: %s\n%s", d.Status, d.Logs)
	}
}

func TestKubeTargetIsExclusive(t *testing.T) {
	c := kube.NewWithRunner("apps", nil)
	if got, err := kubeTarget([]target{{x: executor.Local{}}}); got != nil || err != nil {
		t.Fatalf("Docker servers: %v %v", got, err)
	}
	if got, err := kubeTarget([]target{{x: c}}); got != c || err != nil {
		t.Fatalf("a cluster: %v %v", got, err)
	}
	if _, err := kubeTarget([]target{{x: executor.Local{}}, {x: c}}); err == nil {
		t.Fatal("a cluster next to another server must be refused")
	}
}

func TestValidateCluster(t *testing.T) {
	f := newKubeFixture(t)
	f.srv.Status = "unknown"
	srv, err := f.e.ValidateServer(context.Background(), f.srv)
	if err != nil || srv.Status != "reachable" || srv.DockerVersion != "v1.31.2" || srv.ProxyStatus != "disabled" {
		t.Fatalf("server: %+v, %v", srv, err)
	}
	if f.k.called("auth can-i") != 1 {
		t.Fatal("a cluster not known to work is checked in full")
	}
	// Once it works, the periodic check only asks for the version.
	if _, err := f.e.ValidateServer(context.Background(), srv); err != nil || f.k.called("auth can-i") != 1 {
		t.Fatalf("periodic check: %v, %d", err, f.k.called("auth can-i"))
	}
}
