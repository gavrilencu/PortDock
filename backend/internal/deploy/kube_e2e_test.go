package deploy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/portdock/portdock/internal/config"
	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/kube"
	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
	"github.com/portdock/portdock/internal/stream"
)

// TestKubeE2E deploys to a real cluster with the real kubectl. It needs:
//
//	PORTDOCK_KUBE_E2E            a kubeconfig file (embedded credentials) of a throwaway cluster
//	PORTDOCK_KUBE_E2E_INGRESS    optional: http://host:port of the cluster's ingress controller
//	PORTDOCK_KUBE_E2E_NAMESPACE  optional: an existing namespace, for credentials limited to it
//
// For example, with k3s in Docker:
//
//	docker run -d --name k3s --privileged -p 127.0.0.1:16443:6443 -p 127.0.0.1:18080:80 rancher/k3s server
//	docker exec k3s cat /etc/rancher/k3s/k3s.yaml | sed 's/:6443/:16443/' > k3s.yaml
//	PORTDOCK_KUBE_E2E=$PWD/k3s.yaml PORTDOCK_KUBE_E2E_INGRESS=http://127.0.0.1:18080 go test ./internal/deploy -run TestKubeE2E -v
//
// Everything is created in a namespace of its own, removed at the end.
func TestKubeE2E(t *testing.T) {
	file := os.Getenv("PORTDOCK_KUBE_E2E")
	if file == "" {
		t.Skip("set PORTDOCK_KUBE_E2E to a kubeconfig file to deploy to a real cluster")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kube.CheckKubeconfig(string(raw)); err != nil {
		t.Fatalf("kubeconfig: %v", err)
	}
	ingress := strings.TrimRight(os.Getenv("PORTDOCK_KUBE_E2E_INGRESS"), "/")
	ctx := context.Background()

	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	box, _ := secrets.LoadOrCreate("", dir)
	pool := executor.NewPool(db, box)
	pool.Cluster = kube.Factory(dir)
	e := New(config.Config{DataDir: filepath.ToSlash(dir), ProxyNetwork: "portdock", PublicURL: "http://localhost:8000"}, db, box, pool, stream.NewHub(), notify.New(db, box), nil)

	// PORTDOCK_KUBE_E2E_NAMESPACE runs in an existing namespace, for credentials
	// limited to it (those deploy/kubernetes/connect.sh makes); it is left in place.
	namespace := os.Getenv("PORTDOCK_KUBE_E2E_NAMESPACE")
	if namespace == "" {
		namespace = "portdock-e2e-" + secrets.RandomToken(3)
		t.Cleanup(func() {
			out, err := exec.Command("kubectl", "--kubeconfig", file, "delete", "namespace", namespace, "--ignore-not-found", "--wait=false").CombinedOutput()
			t.Logf("cleanup: %s %v", bytes.TrimSpace(out), err)
		})
	}
	srv := store.Server{Name: "e2e", Kind: store.KindKubernetes, Host: "e2e", Kubeconfig: box.Encrypt(string(raw)), KubeNamespace: namespace,
		KubeVolumeSize: "64Mi", Status: "unknown", CreatedAt: store.Now()}
	project := store.Project{Name: "e2e", CreatedAt: store.Now()}
	for _, row := range []struct {
		table string
		item  any
	}{{"servers", &srv}, {"projects", &project}} {
		if _, err := store.Insert(ctx, db, row.table, row.item); err != nil {
			t.Fatal(err)
		}
	}
	env := store.Environment{ProjectID: project.ID, Name: "production", CreatedAt: store.Now()}
	if _, err := store.Insert(ctx, db, "environments", &env); err != nil {
		t.Fatal(err)
	}
	f := &kubeFixture{t: t, e: e, srv: srv, env: env}

	// ---- the server ----
	srv, err = e.ValidateServer(ctx, srv)
	if err != nil || srv.Status != "reachable" || !strings.HasPrefix(srv.DockerVersion, "v") {
		t.Fatalf("ValidateServer: %+v, %v", srv, err)
	}
	t.Logf("cluster %s, namespace %s", srv.DockerVersion, namespace)
	x, _, err := pool.ForID(ctx, srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := clusterOf(x)
	if !ok {
		t.Fatal("the server is not a cluster")
	}
	if err := e.SetupServer(ctx, srv, false); err != nil {
		t.Fatalf("cluster check: %v", err)
	}
	if err := x.Run(ctx, "echo must-not-run", executor.RunOpts{}); err == nil {
		t.Fatal("shell commands must be refused for a cluster")
	}

	// ---- an application from an image, with a domain ----
	const host = "e2e.portdock.test"
	web := nginxApp()
	web.DockerImage, web.HealthCheckPath, web.CPULimit, web.MemoryLimit = "nginx:1.27-alpine", "/", "0.25", "128m"
	web.PostDeployCommand = "echo post-deploy-ran"
	a := f.app(web)
	d0 := store.AppDomain{ApplicationID: a.ID, Host: host, HTTPS: false, WWWRedirect: "none", CertMode: "auto", CreatedAt: store.Now()}
	if _, err := store.Insert(ctx, db, "app_domains", &d0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO env_vars(owner_type, owner_id, key, value, created_at) VALUES('application', ?, 'GREETING', ?, ?)",
		a.ID, box.Encrypt("hello from\na secret"), store.Now()); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	d := f.deploy(a, Options{})
	t.Logf("first deployment (%s):\n%s", time.Since(start).Round(time.Second), d.Logs)
	if d.Status != "success" {
		t.Fatalf("deployment %s", d.Status)
	}
	if !strings.Contains(d.Logs, "post-deploy-ran") {
		t.Error("the post-deploy command did not run in a pod")
	}
	name := kube.AppName(a.UUID)
	app, _ := store.ByID[store.Application](ctx, db, "applications", a.ID)
	if app.Status != "running" {
		t.Fatalf("application status %q", app.Status)
	}

	cs, err := e.AppContainers(ctx, app)
	if err != nil || len(cs) != 2 {
		t.Fatalf("expected 2 pods: %+v, %v", cs, err)
	}
	for _, ct := range cs {
		if ct.State != "running" || ct.Labels[docker.LabelResource] != a.UUID || ct.Image != "nginx:1.27-alpine" {
			t.Fatalf("pod: %+v", ct)
		}
	}
	pod := cs[0].Name
	if state, health, err := docker.State(ctx, x, pod); err != nil || state != "running" || health != "healthy" {
		t.Fatalf("State = %q %q %v", state, health, err)
	}
	if raw, err := docker.Inspect(ctx, x, pod); err != nil || !strings.Contains(string(raw), a.UUID) {
		t.Fatalf("Inspect: %v", err)
	}
	if all, err := docker.InspectAll(ctx, x, "label="+docker.LabelManaged+"=true"); err != nil || strings.Count(string(all), `"Id"`) < 2 {
		t.Fatalf("InspectAll: %v %s", err, all)
	}

	// Variables reach the container, multi-line values included.
	var out bytes.Buffer
	if err := c.Exec(ctx, name, &out, "sh", "-c", `printf '%s|%s|%s' "$GREETING" "$PORT" "$PORTDOCK_APP"`); err != nil {
		t.Fatalf("exec: %v %s", err, out.String())
	}
	if got := out.String(); got != "hello from\na secret|80|Shop Web" {
		t.Fatalf("variables in the pod: %q", got)
	}
	// Service links are off: no variable named after another Service.
	out.Reset()
	_ = c.Exec(ctx, name, &out, "sh", "-c", "env")
	if strings.Contains(out.String(), "_SERVICE_HOST=") && strings.Contains(out.String(), "SHOP_WEB_") {
		t.Fatalf("service link variables leaked: %s", out.String())
	}

	// Logs of a pod.
	out.Reset()
	if err := docker.Logs(ctx, x, pod, 20, false, true, &out); err != nil {
		t.Fatalf("logs: %v", err)
	}
	if !strings.Contains(out.String(), "worker process") || !strings.Contains(out.String(), "T") {
		t.Errorf("unexpected pod logs: %q", out.String())
	}
	// Usage figures: empty right after start, never an error.
	if _, err := docker.Stats(ctx, x); err != nil {
		t.Fatalf("stats: %v", err)
	}
	if m, err := docker.ServerMetrics(ctx, x); err != nil || m.RunningCont != 2 {
		t.Fatalf("metrics: %+v %v", m, err)
	}

	// The other application name resolves inside the namespace.
	out.Reset()
	if err := c.Exec(ctx, name, &out, "sh", "-c", "wget -qO- http://shop-web/ | head -c 200"); err != nil || !strings.Contains(out.String(), "nginx") {
		t.Fatalf("the Service named after the application does not answer: %v %q", err, out.String())
	}

	get := func() (int, string) {
		req, _ := http.NewRequest("GET", ingress+"/", nil)
		req.Host = host
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, string(body)
	}
	served := func(what string) {
		t.Helper()
		if ingress == "" {
			return
		}
		deadline := time.Now().Add(90 * time.Second)
		for {
			code, body := get()
			if code == 200 && strings.Contains(body, "nginx") {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: the domain does not answer through the ingress: %d %q", what, code, body)
			}
			time.Sleep(2 * time.Second)
		}
	}
	served("after the first deployment")

	// ---- a version that cannot start leaves the previous one serving ----
	bad := app
	bad.DockerImage = "nginx:this-tag-does-not-exist-portdock"
	_, _ = db.ExecContext(ctx, "UPDATE applications SET docker_image = ? WHERE id = ?", bad.DockerImage, a.ID)
	start = time.Now()
	d = f.deploy(bad, Options{})
	t.Logf("failing deployment (%s):\n%s", time.Since(start).Round(time.Second), d.Logs)
	if d.Status != "failed" {
		t.Fatalf("a missing image must fail the deployment: %s", d.Status)
	}
	if time.Since(start) > 4*time.Minute {
		t.Errorf("an image that cannot be pulled should fail fast, took %s", time.Since(start))
	}
	if !strings.Contains(d.Logs, "Previous version keeps running") {
		t.Error("the log should say the previous version keeps running")
	}
	app, _ = store.ByID[store.Application](ctx, db, "applications", a.ID)
	deadline := time.Now().Add(90 * time.Second)
	for e.liveStatus(ctx, app) != "running" && time.Now().Before(deadline) {
		time.Sleep(2 * time.Second) // the failed pod is being removed
	}
	if got := e.liveStatus(ctx, app); got != "running" {
		cs, _ := e.AppContainers(ctx, app)
		t.Fatalf("after a failed deployment the application must still run: %q %+v", got, cs)
	}
	served("after a failed deployment")
	if secrets, _ := c.Names(ctx, "secret", docker.LabelResource+"="+a.UUID+","+labelEnv+"=true"); len(secrets) != 1 {
		t.Fatalf("only the variables of the running version must remain: %v", secrets)
	}
	_, _ = db.ExecContext(ctx, "UPDATE applications SET docker_image = 'nginx:1.27-alpine' WHERE id = ?", a.ID)
	app, _ = store.ByID[store.Application](ctx, db, "applications", a.ID)

	// ---- a saved version is deployed again from its digest ----
	v, err := store.Get[store.AppVersion](ctx, db, "SELECT * FROM app_versions WHERE application_id = ? ORDER BY number DESC", a.ID)
	if err != nil || !strings.Contains(v.Image, "@sha256:") {
		t.Fatalf("the version must be pinned to a digest: %+v %v", v, err)
	}
	d = f.deploy(app, Options{VersionID: v.ID, Trigger: TriggerPromote})
	if d.Status != "success" {
		t.Fatalf("promoting %s failed:\n%s", v.Image, d.Logs)
	}
	served("after promoting a version")

	// ---- domains change without a deployment ----
	_, _ = db.ExecContext(ctx, "DELETE FROM app_domains WHERE application_id = ?", a.ID)
	if err := e.ApplyRoutes(ctx, app); err != nil {
		t.Fatal(err)
	}
	if names, _ := c.Names(ctx, "ingress", docker.LabelResource+"="+a.UUID); len(names) != 0 {
		t.Fatalf("the Ingress must go with its last domain: %v", names)
	}
	if _, err := store.Insert(ctx, db, "app_domains", &store.AppDomain{ApplicationID: a.ID, Host: host, WWWRedirect: "none", CertMode: "auto", CreatedAt: store.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := e.ApplyRoutes(ctx, app); err != nil {
		t.Fatal(err)
	}
	served("after adding the domain back")

	// ---- stop, start, restart ----
	if err := e.AppAction(ctx, app, "stop"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(90 * time.Second)
	for e.liveStatus(ctx, app) != "stopped" && time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
	}
	if got := e.liveStatus(ctx, app); got != "stopped" {
		t.Fatalf("stopped application: %q", got)
	}
	if err := e.AppAction(ctx, app, "start"); err != nil {
		t.Fatal(err)
	}
	if got := e.liveStatus(ctx, app); got != "running" {
		t.Fatalf("started application: %q", got)
	}
	if err := e.AppAction(ctx, app, "restart"); err != nil {
		t.Fatal(err)
	}
	served("after a restart")
	if err := docker.ContainerAction(ctx, x, "restart", mustPod(t, e, app)); err != nil {
		t.Fatalf("restarting a pod: %v", err)
	}

	// ---- storage, a pre-deploy command and scheduled jobs ----
	worker := store.Application{Name: "worker", SourceType: "image", BuildPack: "image", DockerImage: "busybox:1.36", Replicas: 1,
		StartCommand: "sh -c 'echo started > /data/started; exec sleep 3600'", PreDeployCommand: "echo pre-deploy-ran && test -n \"$PORTDOCK_APP\"", HealthCheckTimeout: 60}
	w := f.app(worker)
	vol := store.AppVolume{ApplicationID: w.ID, Name: "data", MountPath: "/data", CreatedAt: store.Now()}
	if _, err := store.Insert(ctx, db, "app_volumes", &vol); err != nil {
		t.Fatal(err)
	}
	d = f.deploy(w, Options{})
	t.Logf("worker deployment:\n%s", d.Logs)
	if d.Status != "success" {
		t.Fatalf("worker deployment %s", d.Status)
	}
	if !strings.Contains(d.Logs, "pre-deploy-ran") {
		t.Error("the output of the pre-deploy command is not in the log")
	}
	wapp, _ := store.ByID[store.Application](ctx, db, "applications", w.ID)
	if usage := e.VolumeUsage(ctx, wapp); len(usage) != 1 || len(usage[0].Volumes) != 1 || usage[0].Volumes[0] != "data" {
		t.Fatalf("volume usage: %+v", usage)
	}
	job := store.AppJob{ID: 1, ApplicationID: w.ID, Name: "report", Command: "echo job-says-$(cat /data/started) && echo from-job > /data/job"}
	jout := &tailBuffer{limit: 4096}
	if err := e.jobCommand(ctx, job, wapp, store.JobRun{ID: 1}, 2*time.Minute, jout); err != nil {
		t.Fatalf("job: %v\n%s", err, jout.String())
	}
	if !strings.Contains(jout.String(), "job-says-started") {
		t.Fatalf("the job must run with the application's volume: %q", jout.String())
	}
	failing := store.AppJob{ID: 2, ApplicationID: w.ID, Name: "broken", Command: "echo about-to-fail; exit 3"}
	jout = &tailBuffer{limit: 4096}
	if err := e.jobCommand(ctx, failing, wapp, store.JobRun{ID: 2}, 2*time.Minute, jout); err == nil || !strings.Contains(jout.String(), "about-to-fail") {
		t.Fatalf("a failing job must fail with its output: %v %q", err, jout.String())
	}
	// A second deployment keeps the data (the claim is reused, old pod first).
	d = f.deploy(wapp, Options{})
	if d.Status != "success" {
		t.Fatalf("second worker deployment %s:\n%s", d.Status, d.Logs)
	}
	out.Reset()
	if err := c.Exec(ctx, kube.AppName(w.UUID), &out, "cat", "/data/job"); err != nil || strings.TrimSpace(out.String()) != "from-job" {
		t.Fatalf("data must survive a deployment: %v %q", err, out.String())
	}

	// ---- deletion ----
	for _, target := range []store.Application{app, wapp} {
		if err := e.DeleteApp(ctx, target, true); err != nil {
			t.Fatalf("delete %s: %v", target.Name, err)
		}
	}
	deadline = time.Now().Add(2 * time.Minute)
	for {
		left := []string{}
		for _, kind := range []string{"deployment", "service", "ingress", "secret", "persistentvolumeclaim", "pod"} {
			names, _ := c.Names(ctx, kind, docker.LabelManaged+"=true,"+docker.LabelKind+"=application")
			for _, n := range names {
				left = append(left, kind+"/"+n)
			}
		}
		if len(left) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("left behind after deleting the applications: %v", left)
		}
		time.Sleep(3 * time.Second)
	}
}

func mustPod(t *testing.T, e *Engine, app store.Application) string {
	t.Helper()
	cs, err := e.AppContainers(context.Background(), app)
	if err != nil || len(cs) == 0 {
		t.Fatalf("no pod: %v", err)
	}
	return fmt.Sprint(cs[0].Name)
}
