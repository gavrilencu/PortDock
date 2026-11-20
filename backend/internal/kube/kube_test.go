package kube

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
)

// call is one invocation of the fake kubectl.
type call struct {
	args  []string
	stdin string
}

// fake returns a cluster whose kubectl is answer, and the calls it received.
func fake(answer func(c call) (stdout, stderr string, err error)) (*Cluster, *[]call) {
	calls := &[]call{}
	cl := &Cluster{Namespace: "apps", kubeconfig: "/data/kube/server-1.kubeconfig"}
	cl.run = func(ctx context.Context, in io.Reader, out, errw io.Writer, args ...string) error {
		c := call{args: args}
		if in != nil {
			b, _ := io.ReadAll(in)
			c.stdin = string(b)
		}
		*calls = append(*calls, c)
		stdout, stderr, err := answer(c)
		if out != nil {
			_, _ = io.WriteString(out, stdout)
		}
		if errw != nil {
			_, _ = io.WriteString(errw, stderr)
		}
		return err
	}
	return cl, calls
}

func has(args []string, want ...string) bool {
	return strings.Contains(" "+strings.Join(args, " ")+" ", " "+strings.Join(want, " ")+" ")
}

const podsJSON = `{"items":[
 {"metadata":{"name":"pd-abc-7d9f-x1","labels":{"portdock.managed":"true","portdock.kind":"application","portdock.resource":"abc","portdock.pr":"0"},"creationTimestamp":"2026-11-19T10:00:00Z"},
  "spec":{"nodeName":"node-1","containers":[{"name":"app","image":"registry.corp/team/web:1a2b3c","ports":[{"containerPort":3000,"protocol":"TCP"}],"env":[{"name":"LOG_LEVEL","value":"info"}],"readinessProbe":{"tcpSocket":{"port":3000}}}]},
  "status":{"phase":"Running","podIP":"10.42.0.7","startTime":"2026-11-19T10:00:02Z","containerStatuses":[{"name":"app","ready":true,"restartCount":2,"state":{"running":{}}}]}},
 {"metadata":{"name":"pd-abc-7d9f-x2","labels":{"portdock.managed":"true","portdock.resource":"abc"}},
  "spec":{"containers":[{"name":"app","image":"registry.corp/team/web:1a2b3c"}]},
  "status":{"phase":"Running","containerStatuses":[{"name":"app","ready":false,"restartCount":6,"state":{"waiting":{"reason":"CrashLoopBackOff","message":"back-off 5m0s restarting failed container"}}}]}},
 {"metadata":{"name":"pd-abc-7d9f-x3","labels":{"portdock.resource":"abc"}},
  "spec":{"containers":[{"name":"app","image":"registry.corp/team/web:1a2b3c"}]},
  "status":{"phase":"Pending","containerStatuses":[{"name":"app","ready":false,"state":{"waiting":{"reason":"ImagePullBackOff"}}}]}},
 {"metadata":{"name":"pd-abc-old","labels":{"portdock.resource":"abc"},"deletionTimestamp":"2026-11-19T10:05:00Z"},
  "spec":{"containers":[{"name":"app","image":"x"}]},"status":{"phase":"Running"}},
 {"metadata":{"name":"pd-abc-job1-9","labels":{"portdock.resource":"abc","portdock.kind":"job"}},
  "spec":{"containers":[{"name":"app","image":"x"}]},"status":{"phase":"Succeeded"}}
]}`

func TestContainersArePods(t *testing.T) {
	cl, calls := fake(func(c call) (string, string, error) { return podsJSON, "", nil })
	cs, err := cl.Containers(context.Background(), true, "label=portdock.resource=abc", "label=portdock.pr=0")
	if err != nil {
		t.Fatal(err)
	}
	args := (*calls)[0].args
	if !has(args, "--kubeconfig", "/data/kube/server-1.kubeconfig", "--namespace", "apps", "get", "pods") || !has(args, "-l", "portdock.resource=abc,portdock.pr=0") {
		t.Fatalf("unexpected kubectl call: %v", args)
	}
	if len(cs) != 3 {
		t.Fatalf("a pod being deleted and a completed one must be left out; got %d: %+v", len(cs), cs)
	}
	first := cs[0]
	if first.ID != "pd-abc-7d9f-x1" || first.Name != first.ID || first.State != "running" || first.Image != "registry.corp/team/web:1a2b3c" ||
		first.Ports != "3000/tcp" || first.Labels[docker.LabelResource] != "abc" || first.Networks != "10.42.0.7" {
		t.Fatalf("unexpected container: %+v", first)
	}
	if !strings.Contains(first.Status, "1/1 ready") || !strings.Contains(first.Status, "2 restart") {
		t.Fatalf("status = %q", first.Status)
	}
	if cs[1].State != "restarting" || cs[2].State != "created" || !strings.Contains(cs[2].Status, "ImagePullBackOff") {
		t.Fatalf("states: %q (%s), %q (%s)", cs[1].State, cs[1].Status, cs[2].State, cs[2].Status)
	}
	running, err := cl.Containers(context.Background(), false)
	if err != nil || len(running) != 1 {
		t.Fatalf("without all, only running pods: %v, %+v", err, running)
	}
}

// A filter that is not a label would otherwise match every pod of the namespace.
func TestContainersRefuseOtherFilters(t *testing.T) {
	cl, calls := fake(func(c call) (string, string, error) { return podsJSON, "", nil })
	for _, f := range []string{"name=web", "status=running", "label="} {
		if _, err := cl.Containers(context.Background(), true, f); err == nil {
			t.Fatalf("filter %q must be refused", f)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("kubectl must not run for a refused filter: %v", *calls)
	}
}

// Shell commands must never run for a cluster: they would run where PortDock is.
func TestClusterRefusesShell(t *testing.T) {
	cl, calls := fake(func(c call) (string, string, error) { return "", "", nil })
	var x executor.Executor = cl
	if err := x.Run(context.Background(), "docker rm -f portdock", executor.RunOpts{}); !errors.Is(err, ErrNotDocker) {
		t.Fatalf("Run = %v", err)
	}
	if _, err := x.PTY(context.Background(), "sh", 24, 80); !errors.Is(err, ErrNotDocker) {
		t.Fatalf("PTY = %v", err)
	}
	if _, err := executor.Output(context.Background(), x, "docker ps"); err == nil {
		t.Fatal("Output must fail")
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing may be executed: %v", *calls)
	}
	if docker.IsDocker(x) {
		t.Fatal("a cluster is not a Docker host")
	}
}

// The docker package asks the cluster instead of running the docker CLI.
func TestDockerPackageDispatch(t *testing.T) {
	cl, calls := fake(func(c call) (string, string, error) {
		switch {
		case has(c.args, "get", "pods"):
			return podsJSON, "", nil
		case has(c.args, "get", "pod", "pd-abc-7d9f-x1"):
			var list struct{ Items []json.RawMessage }
			_ = json.Unmarshal([]byte(podsJSON), &list)
			return string(list.Items[0]), "", nil
		case has(c.args, "top", "pods"):
			return "pd-abc-7d9f-x1   250m   64Mi\npd-other   1   1Gi\n", "", nil
		}
		return "", "", nil
	})
	ctx := context.Background()
	cs, err := docker.PS(ctx, cl, true, "label=portdock.resource=abc")
	if err != nil || len(cs) != 3 {
		t.Fatalf("PS: %v, %d", err, len(cs))
	}
	raw, err := docker.Inspect(ctx, cl, "pd-abc-7d9f-x1")
	if err != nil {
		t.Fatal(err)
	}
	// The shape the API reads to authorize a container and to hide secrets.
	var doc []struct {
		Config struct {
			Labels map[string]string
			Env    []string
		}
		State struct {
			Status string
			Health *struct{ Status string }
		}
		RestartCount int
	}
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc) != 1 {
		t.Fatalf("inspect: %v %s", err, raw)
	}
	if doc[0].Config.Labels[docker.LabelResource] != "abc" || doc[0].State.Status != "running" || doc[0].State.Health == nil ||
		doc[0].State.Health.Status != "healthy" || doc[0].RestartCount != 2 || len(doc[0].Config.Env) != 1 || doc[0].Config.Env[0] != "LOG_LEVEL=info" {
		t.Fatalf("inspect document: %s", raw)
	}
	// Every pod at once (the topology map): one kubectl call, the same documents,
	// without the pod that is being deleted or the job that completed.
	before := len(*calls)
	all, err := docker.InspectAll(ctx, cl, "label=portdock.resource=abc")
	if err != nil {
		t.Fatal(err)
	}
	var docs []struct {
		ID     string `json:"Id"`
		Config struct{ Labels map[string]string }
	}
	if err := json.Unmarshal(all, &docs); err != nil || len(docs) != 3 || docs[0].ID != "pd-abc-7d9f-x1" || docs[0].Config.Labels[docker.LabelResource] != "abc" {
		t.Fatalf("InspectAll: %v %s", err, all)
	}
	if got := len(*calls) - before; got != 1 {
		t.Fatalf("InspectAll made %d kubectl calls, want 1", got)
	}
	state, health, err := docker.State(ctx, cl, "pd-abc-7d9f-x1")
	if err != nil || state != "running" || health != "healthy" {
		t.Fatalf("State = %q %q %v", state, health, err)
	}
	stats, err := docker.Stats(ctx, cl, "pd-abc-7d9f-x1")
	if err != nil || len(stats) != 1 || stats[0].CPUPerc != "25.00%" || !strings.HasPrefix(stats[0].MemUsage, "64MiB") {
		t.Fatalf("Stats = %+v %v", stats, err)
	}
	if err := docker.ContainerAction(ctx, cl, "restart", "pd-abc-7d9f-x1"); err != nil {
		t.Fatal(err)
	}
	if last := (*calls)[len(*calls)-1].args; !has(last, "delete", "pod", "pd-abc-7d9f-x1") {
		t.Fatalf("restart must delete the pod: %v", last)
	}
	if err := docker.ContainerAction(ctx, cl, "stop", "pd-abc-7d9f-x1"); err == nil {
		t.Fatal("a pod cannot be stopped on its own")
	}
	if imgs, err := docker.Images(ctx, cl); err != nil || len(imgs) != 0 {
		t.Fatalf("Images = %v %v", imgs, err)
	}
	if _, err := docker.Prune(ctx, cl, "all"); !errors.Is(err, docker.ErrDockerOnly) {
		t.Fatalf("Prune = %v", err)
	}
	if err := docker.EnsureNetwork(ctx, cl, "portdock"); err != nil {
		t.Fatal(err)
	}
}

func TestPodNamesCannotBeOptions(t *testing.T) {
	cl, calls := fake(func(c call) (string, string, error) { return "", "", nil })
	for _, name := range []string{"", "--all", "-o=yaml", "deployment/web", "a b"} {
		if err := cl.ContainerAction(context.Background(), "restart", name); err == nil {
			t.Fatalf("name %q must be refused", name)
		}
		if err := cl.ContainerLogs(context.Background(), name, 10, false, false, io.Discard); err == nil {
			t.Fatalf("logs of %q must be refused", name)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("kubectl ran: %v", *calls)
	}
}

func TestStatsWithoutMetricsServer(t *testing.T) {
	// No metrics-server, and pods it has not sampled yet (seen on k3s right after a deployment).
	for _, msg := range []string{"error: Metrics API not available", "error: metrics not available yet"} {
		cl, _ := fake(func(c call) (string, string, error) { return "", msg, errors.New("exit status 1") })
		stats, err := cl.ContainerStats(context.Background())
		if err != nil || len(stats) != 0 {
			t.Fatalf("%q is not an error: %v %v", msg, stats, err)
		}
	}
	cl, _ := fake(func(c call) (string, string, error) {
		return "", "Unable to connect to the server", errors.New("exit status 1")
	})
	if _, err := cl.ContainerStats(context.Background()); err == nil {
		t.Fatal("an unreachable cluster is an error")
	}
}

func TestApplySendsAListServerSide(t *testing.T) {
	cl, calls := fake(func(c call) (string, string, error) { return "", "", nil })
	err := cl.Apply(context.Background(), Secret("pd-abc-env-7", nil, map[string]string{"A": "1"}), Service("pd-abc", nil, map[string]string{"x": "y"}, []int{80}))
	if err != nil {
		t.Fatal(err)
	}
	c := (*calls)[0]
	if !has(c.args, "apply", "--server-side", "--force-conflicts", "--field-manager=portdock", "-f", "-") || !has(c.args, "--namespace", "apps") {
		t.Fatalf("args: %v", c.args)
	}
	var list struct {
		Kind  string
		Items []struct{ Kind string }
	}
	if err := json.Unmarshal([]byte(c.stdin), &list); err != nil || list.Kind != "List" || len(list.Items) != 2 || list.Items[0].Kind != "Secret" || list.Items[1].Kind != "Service" {
		t.Fatalf("stdin: %s (%v)", c.stdin, err)
	}
	if err := cl.Delete(context.Background(), []string{"deployment"}, ""); err == nil {
		t.Fatal("a delete without a selector must be refused")
	}
}

func TestErrorsCarryKubectlMessage(t *testing.T) {
	cl, _ := fake(func(c call) (string, string, error) {
		return "", `Error from server (Forbidden): deployments.apps is forbidden: User "sa" cannot create resource`, errors.New("exit status 1")
	})
	err := cl.Apply(context.Background(), Object{"kind": "Deployment"})
	if err == nil || !strings.Contains(err.Error(), "Forbidden") || strings.Contains(err.Error(), "exit status") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureNamespace(t *testing.T) {
	// Missing: created.
	cl, calls := fake(func(c call) (string, string, error) {
		if has(c.args, "get", "namespace") {
			return "", `Error from server (NotFound): namespaces "apps" not found`, errors.New("exit status 1")
		}
		return "", "", nil
	})
	if err := cl.EnsureNamespace(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(*calls); n != 2 || !has((*calls)[1].args, "create", "namespace", "apps") {
		t.Fatalf("calls: %v", *calls)
	}
	// Credentials of one namespace cannot read it: that is not a failure.
	cl, calls = fake(func(c call) (string, string, error) {
		return "", `Error from server (Forbidden): namespaces "apps" is forbidden`, errors.New("exit status 1")
	})
	if err := cl.EnsureNamespace(context.Background()); err != nil || len(*calls) != 1 {
		t.Fatalf("forbidden: %v, %d calls", err, len(*calls))
	}
}

func TestVersion(t *testing.T) {
	cl, calls := fake(func(c call) (string, string, error) {
		return `{"major":"1","minor":"31","gitVersion":"v1.31.2+k3s1","platform":"linux/amd64"}`, "", nil
	})
	if v, err := cl.Version(context.Background()); err != nil || v != "v1.31.2+k3s1" {
		t.Fatalf("Version = %q %v", v, err)
	}
	if !has((*calls)[0].args, "get", "--raw", "/version") {
		t.Fatalf("args: %v", (*calls)[0].args)
	}
	cl, _ = fake(func(c call) (string, string, error) {
		return "", "Unable to connect to the server: dial tcp 10.0.0.1:6443: i/o timeout", errors.New("exit status 1")
	})
	if _, err := cl.Version(context.Background()); err == nil || !strings.Contains(err.Error(), "i/o timeout") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunJob(t *testing.T) {
	gets := 0
	cl, calls := fake(func(c call) (string, string, error) {
		switch {
		case has(c.args, "get", "job"):
			gets++
			if gets < 2 {
				return `{"status":{"active":1}}`, "", nil
			}
			return `{"status":{"succeeded":1}}`, "", nil
		case has(c.args, "logs"):
			return "migrated 3 tables\n", "", nil
		}
		return "", "", nil
	})
	var out bytes.Buffer
	job := Job("pd-abc-pre-7", Workload{Image: "web:1", Command: []string{"sh", "-c", "migrate"}}, time.Minute)
	if err := cl.RunJob(context.Background(), job, time.Minute, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "migrated 3 tables") {
		t.Fatalf("output = %q", out.String())
	}
	last := (*calls)[len(*calls)-1].args
	if !has(last, "delete", "job", "pd-abc-pre-7") {
		t.Fatalf("the job must be removed afterwards: %v", last)
	}

	cl, _ = fake(func(c call) (string, string, error) {
		if has(c.args, "get", "job") {
			return `{"status":{"failed":1}}`, "", nil
		}
		return "", "", nil
	})
	if err := cl.RunJob(context.Background(), job, time.Minute, io.Discard); err == nil {
		t.Fatal("a failed job must be an error")
	}
}

func TestImageDigest(t *testing.T) {
	cl, _ := fake(func(c call) (string, string, error) {
		return "docker-pullable://nginx@sha256:aaaa\n", "", nil
	})
	if got := cl.ImageDigest(context.Background(), "x=y"); got != "nginx@sha256:aaaa" {
		t.Fatalf("digest = %q", got)
	}
	cl, _ = fake(func(c call) (string, string, error) { return "sha256:bbbb\n", "", nil })
	if got := cl.ImageDigest(context.Background(), "x=y"); got != "" {
		t.Fatalf("an id without a name cannot be pulled: %q", got)
	}
}

// ---- manifests ----

func dig(t *testing.T, o any, path ...any) any {
	t.Helper()
	b, _ := json.Marshal(o)
	var v any
	_ = json.Unmarshal(b, &v)
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("%v: not an object at %v in %s", path, p, b)
			}
			v = m[k]
		case int:
			l, ok := v.([]any)
			if !ok || k >= len(l) {
				t.Fatalf("%v: no element %d in %s", path, k, b)
			}
			v = l[k]
		}
	}
	return v
}

func TestDeploymentManifest(t *testing.T) {
	w := Workload{
		Name: "pd-abc", Selector: map[string]string{"portdock.resource": "abc", "portdock.pr": "0"},
		Labels:    map[string]string{"portdock.resource": "abc", "portdock.pr": "0", "portdock.managed": "true"},
		PodLabels: map[string]string{"portdock.deployment": "d1"},
		Image:     "registry.corp/team/web:1a2b3c", PullSecret: "pd-registry-2", Replicas: 3,
		Command: []string{"sh", "-c", "echo $(date) && exec node server.js"}, Ports: []int{3000, 9090},
		EnvSecret: "pd-abc-env-7", CPU: "500m", Memory: "536870912", HealthPath: "health", StartupSeconds: 120,
	}
	d := Deployment(w)
	spec := []any{"spec", "template", "spec"}
	at := func(p ...any) any { return dig(t, d, append(append([]any{}, spec...), p...)...) }

	if dig(t, d, "apiVersion") != "apps/v1" || dig(t, d, "spec", "replicas") != float64(3) {
		t.Fatalf("deployment: %v", d)
	}
	// Zero downtime: a new pod first, and the old one only once it is ready.
	if dig(t, d, "spec", "strategy", "type") != "RollingUpdate" || dig(t, d, "spec", "strategy", "rollingUpdate", "maxUnavailable") != float64(0) {
		t.Fatalf("strategy: %v", dig(t, d, "spec", "strategy"))
	}
	if dig(t, d, "spec", "selector", "matchLabels", "portdock.resource") != "abc" || dig(t, d, "spec", "selector", "matchLabels", "portdock.deployment") != nil {
		t.Fatal("the selector must not change between deployments")
	}
	if dig(t, d, "spec", "template", "metadata", "labels", "portdock.deployment") != "d1" || dig(t, d, "metadata", "labels", "portdock.deployment") != nil {
		t.Fatal("the deployment label belongs to the pods only")
	}
	if at("enableServiceLinks") != false || at("automountServiceAccountToken") != false {
		t.Fatal("service links and the API token must be off")
	}
	if at("imagePullSecrets", 0, "name") != "pd-registry-2" || at("containers", 0, "envFrom", 0, "secretRef", "name") != "pd-abc-env-7" {
		t.Fatalf("pod spec: %v", at())
	}
	// Kubernetes must not expand $(date): the shell does.
	if got := at("containers", 0, "command", 2); got != "echo $$(date) && exec node server.js" {
		t.Fatalf("command = %q", got)
	}
	if at("containers", 0, "resources", "limits", "cpu") != "500m" || at("containers", 0, "resources", "limits", "memory") != "536870912" {
		t.Fatalf("limits: %v", at("containers", 0, "resources"))
	}
	if at("containers", 0, "readinessProbe", "httpGet", "path") != "/health" || at("containers", 0, "readinessProbe", "httpGet", "port") != float64(3000) {
		t.Fatalf("readiness: %v", at("containers", 0, "readinessProbe"))
	}
	// 120 s to start, probed every 2 s.
	if at("containers", 0, "startupProbe", "failureThreshold") != float64(60) || at("containers", 0, "startupProbe", "periodSeconds") != float64(2) {
		t.Fatalf("startup: %v", at("containers", 0, "startupProbe"))
	}

	// Without a health path the port is probed; without a port, nothing.
	w.HealthPath = ""
	if dig(t, Deployment(w), "spec", "template", "spec", "containers", 0, "readinessProbe", "tcpSocket", "port") != float64(3000) {
		t.Fatal("TCP probe expected")
	}
	w.Ports = nil
	if dig(t, Deployment(w), "spec", "template", "spec", "containers", 0, "readinessProbe") != nil {
		t.Fatal("no probe without a port")
	}

	// A volume cannot follow a new pod to another node: old pods stop first.
	w.Mounts = []Mount{{Claim: "pd-abc-data", Path: "/data"}, {Claim: "pd-abc-conf", Path: "/etc/app", ReadOnly: true}}
	d = Deployment(w)
	if dig(t, d, "spec", "strategy", "type") != "Recreate" || dig(t, d, "spec", "strategy", "rollingUpdate") != nil {
		t.Fatalf("strategy with volumes: %v", dig(t, d, "spec", "strategy"))
	}
	if dig(t, d, "spec", "template", "spec", "volumes", 1, "persistentVolumeClaim", "claimName") != "pd-abc-conf" ||
		dig(t, d, "spec", "template", "spec", "containers", 0, "volumeMounts", 1, "mountPath") != "/etc/app" ||
		dig(t, d, "spec", "template", "spec", "containers", 0, "volumeMounts", 1, "readOnly") != true ||
		dig(t, d, "spec", "template", "spec", "containers", 0, "volumeMounts", 0, "name") != dig(t, d, "spec", "template", "spec", "volumes", 0, "name") {
		t.Fatalf("volumes: %v", dig(t, d, "spec", "template", "spec"))
	}
}

func TestJobManifest(t *testing.T) {
	j := Job("pd-abc-job3-9", Workload{Labels: map[string]string{"portdock.kind": "job"}, Image: "web:1", Ports: []int{3000}, Command: []string{"sh", "-c", "report"}}, 90*time.Second)
	if dig(t, j, "kind") != "Job" || dig(t, j, "spec", "backoffLimit") != float64(0) || dig(t, j, "spec", "activeDeadlineSeconds") != float64(90) {
		t.Fatalf("job: %v", j)
	}
	if dig(t, j, "spec", "template", "spec", "restartPolicy") != "Never" || dig(t, j, "spec", "template", "spec", "containers", 0, "readinessProbe") != nil {
		t.Fatalf("job pod: %v", dig(t, j, "spec", "template", "spec"))
	}
}

func TestIngressManifest(t *testing.T) {
	routes := []Route{
		{Host: "shop.example.com", Service: "pd-abc", Port: 3000, TLS: true},
		{Host: "shop.example.com", Path: "/api", Service: "pd-abc", Port: 8080, TLS: true},
		{Host: "*.apps.example.com", Service: "pd-abc", Port: 3000, TLS: true},
		{Host: "plain.example.com", Service: "pd-abc", Port: 3000},
	}
	ing := Ingress("pd-abc", map[string]string{"portdock.resource": "abc"}, routes, IngressOptions{Class: "nginx", Issuer: "letsencrypt"})
	if dig(t, ing, "apiVersion") != "networking.k8s.io/v1" || dig(t, ing, "spec", "ingressClassName") != "nginx" ||
		dig(t, ing, "metadata", "annotations", "cert-manager.io/cluster-issuer") != "letsencrypt" {
		t.Fatalf("ingress: %v", ing)
	}
	rules := dig(t, ing, "spec", "rules").([]any)
	if len(rules) != 3 {
		t.Fatalf("one rule per host, got %d", len(rules))
	}
	if dig(t, ing, "spec", "rules", 0, "http", "paths", 0, "path") != "/" || dig(t, ing, "spec", "rules", 0, "http", "paths", 1, "path") != "/api" ||
		dig(t, ing, "spec", "rules", 0, "http", "paths", 1, "backend", "service", "port", "number") != float64(8080) ||
		dig(t, ing, "spec", "rules", 0, "http", "paths", 0, "pathType") != "Prefix" {
		t.Fatalf("paths: %v", dig(t, ing, "spec", "rules", 0))
	}
	tls := dig(t, ing, "spec", "tls").([]any)
	if len(tls) != 2 {
		t.Fatalf("only HTTPS hosts get TLS, once each: %v", tls)
	}
	if dig(t, ing, "spec", "tls", 0, "secretName") != "pd-tls-shop-example-com" || dig(t, ing, "spec", "tls", 1, "secretName") != "pd-tls-wildcard-apps-example-com" {
		t.Fatalf("tls: %v", tls)
	}

	// Without an issuer: the controller's certificate, no annotation, no secret.
	ing = Ingress("pd-abc", nil, routes[:1], IngressOptions{})
	if dig(t, ing, "spec", "ingressClassName") != nil || dig(t, ing, "metadata", "annotations") != nil || dig(t, ing, "spec", "tls", 0, "secretName") != nil ||
		dig(t, ing, "spec", "tls", 0, "hosts", 0) != "shop.example.com" {
		t.Fatalf("ingress without issuer: %v", ing)
	}
	// No HTTPS host, no TLS section.
	if dig(t, Ingress("pd-abc", nil, routes[3:], IngressOptions{Issuer: "letsencrypt"}), "spec", "tls") != nil {
		t.Fatal("no TLS expected")
	}
}

func TestTLSSecretNameIsALabel(t *testing.T) {
	long := strings.Repeat("very-long-subdomain.", 6) + "example.com"
	a, b := TLSSecretName(long), TLSSecretName(long+"x")
	if len(a) > 63 || a == b {
		t.Fatalf("names must fit and differ: %q %q", a, b)
	}
}

func TestSecretsAreEncoded(t *testing.T) {
	s := Secret("pd-abc-env-7", nil, map[string]string{"KEY": "line1\nline2"})
	raw, _ := base64.StdEncoding.DecodeString(dig(t, s, "data", "KEY").(string))
	if string(raw) != "line1\nline2" {
		t.Fatalf("value = %q", raw)
	}
	p := PullSecret("pd-registry-2", nil, "registry.corp", "robot", "s3cret")
	if dig(t, p, "type") != "kubernetes.io/dockerconfigjson" {
		t.Fatal("wrong type")
	}
	cfg, _ := base64.StdEncoding.DecodeString(dig(t, p, "data", ".dockerconfigjson").(string))
	var doc struct {
		Auths map[string]struct{ Username, Password, Auth string }
	}
	if err := json.Unmarshal(cfg, &doc); err != nil || doc.Auths["registry.corp"].Password != "s3cret" ||
		doc.Auths["registry.corp"].Auth != base64.StdEncoding.EncodeToString([]byte("robot:s3cret")) {
		t.Fatalf("docker config: %s", cfg)
	}
}

func TestQuantities(t *testing.T) {
	for in, want := range map[string]string{"": "", "512m": "536870912", "1g": "1073741824", "1.5g": "1610612736", "256MB": "268435456", "1048576": "1048576", "64mi": "67108864"} {
		if got, err := MemoryQuantity(in); err != nil || got != want {
			t.Errorf("MemoryQuantity(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"lots", "-1g", "0", "1x"} {
		if _, err := MemoryQuantity(bad); err == nil {
			t.Errorf("MemoryQuantity(%q) must fail", bad)
		}
	}
	for in, want := range map[string]string{"": "", "0.5": "500m", "2": "2000m", "0.25": "250m", "750m": "750m"} {
		if got, err := CPUQuantity(in); err != nil || got != want {
			t.Errorf("CPUQuantity(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"half", "0", "-1"} {
		if _, err := CPUQuantity(bad); err == nil {
			t.Errorf("CPUQuantity(%q) must fail", bad)
		}
	}
}

// ---- kubeconfig ----

const tokenConfig = `apiVersion: v1
kind: Config
current-context: portdock
clusters:
- name: prod
  cluster:
    server: https://10.0.0.1:6443
    certificate-authority-data: LS0tLS1CRUdJTg==
contexts:
- name: portdock
  context: {cluster: prod, user: portdock, namespace: apps}
users:
- name: portdock
  user:
    token: eyJhbGciOi
`

func TestCheckKubeconfig(t *testing.T) {
	server, err := CheckKubeconfig(tokenConfig)
	if err != nil || server != "https://10.0.0.1:6443" {
		t.Fatalf("server = %q, %v", server, err)
	}
	refused := map[string]string{
		"a program run for credentials": strings.Replace(tokenConfig, "    token: eyJhbGciOi\n", "    exec:\n      command: aws\n      args: [eks, get-token]\n", 1),
		"an auth provider":              strings.Replace(tokenConfig, "    token: eyJhbGciOi\n", "    auth-provider:\n      name: gcp\n", 1),
		"a token file":                  strings.Replace(tokenConfig, "    token: eyJhbGciOi\n", "    tokenFile: /var/run/secrets/token\n", 1),
		"a key file":                    strings.Replace(tokenConfig, "    token: eyJhbGciOi\n", "    client-certificate: /etc/ssl/c.crt\n    client-key: /etc/ssl/c.key\n", 1),
		"a CA file":                     strings.Replace(tokenConfig, "    certificate-authority-data: LS0tLS1CRUdJTg==\n", "    certificate-authority: /etc/shadow\n", 1),
		"a missing context":             strings.Replace(tokenConfig, "current-context: portdock", "current-context: other", 1),
		// However kubectl would read the file, these must not get through.
		"exec spelled in another case":               strings.Replace(tokenConfig, "    token: eyJhbGciOi\n", "    Exec:\n      command: sh\n", 1),
		"exec on a user that is not the current one": tokenConfig + "- name: other\n  user:\n    exec:\n      command: sh\n",
		"a token file in another case":               strings.Replace(tokenConfig, "    token: eyJhbGciOi\n", "    TOKENFILE: /etc/passwd\n", 1),
		"exec behind a YAML merge":                   "base: &b\n  exec:\n    command: sh\n" + strings.Replace(tokenConfig, "    token: eyJhbGciOi\n", "    <<: *b\n", 1),
		"no server":                                  strings.Replace(tokenConfig, "    server: https://10.0.0.1:6443\n", "", 1),
		"not YAML":                                   "{{{",
		"empty":                                      "",
	}
	for what, cfg := range refused {
		if _, err := CheckKubeconfig(cfg); err == nil {
			t.Errorf("a kubeconfig with %s must be refused", what)
		}
	}
}

// In a pod, kubectl is given a kubeconfig of the service account: not every
// kubectl command finds those credentials by itself.
func TestInClusterConfig(t *testing.T) {
	dir := t.TempDir()
	old := serviceAccountDir
	serviceAccountDir = filepath.ToSlash(dir)
	t.Cleanup(func() { serviceAccountDir = old })
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	if _, err := inClusterConfig(); err == nil {
		t.Fatal("outside a pod there is no service account")
	}
	t.Setenv("KUBERNETES_SERVICE_HOST", "fd00::1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	if _, err := inClusterConfig(); err == nil {
		t.Fatal("a pod without a token must be reported")
	}
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte("t"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := inClusterConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"server: https://[fd00::1]:443", "tokenFile: " + serviceAccountDir + "/token", "certificate-authority: " + serviceAccountDir + "/ca.crt", "current-context: in-cluster"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("the kubeconfig should contain %q:\n%s", want, cfg)
		}
	}
	file, err := writeInClusterConfig(t.TempDir())
	if err != nil || !strings.HasSuffix(filepath.ToSlash(file), "kube/in-cluster.kubeconfig") {
		t.Fatalf("file %q, %v", file, err)
	}
}

func TestCheckNamespace(t *testing.T) {
	for _, ok := range []string{"portdock", "team-a-prod", "a1"} {
		if err := CheckNamespace(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Prod", "-x", "a_b", "kube-system", "x --all-namespaces", strings.Repeat("a", 64)} {
		if err := CheckNamespace(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

// A pod of a new version that will not become ready is recognized at once,
// with what went wrong; one that may still is left alone.
func TestStartFailure(t *testing.T) {
	parse := func(status string) pod {
		var p pod
		if err := json.Unmarshal([]byte(`{"metadata":{"name":"web-1"},"status":`+status+`}`), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for name, c := range map[string]struct {
		status   string
		want     string // "" when the pod may still become ready
		noAnswer bool
	}{
		"starting":      {`{"containerStatuses":[{"ready":false,"restartCount":0,"state":{"running":{}}}]}`, "", false},
		"ready":         {`{"containerStatuses":[{"ready":true,"restartCount":2,"state":{"running":{}}}]}`, "", false},
		"probe kill":    {`{"containerStatuses":[{"ready":false,"restartCount":1,"state":{"running":{}},"lastState":{"terminated":{"reason":"Completed","exitCode":0}}}]}`, "never answered its health check", true},
		"sigkill":       {`{"containerStatuses":[{"ready":false,"restartCount":2,"state":{"waiting":{"reason":"CrashLoopBackOff"}},"lastState":{"terminated":{"reason":"Error","exitCode":137}}}]}`, "exit code 137", true},
		"crash":         {`{"containerStatuses":[{"ready":false,"restartCount":1,"state":{"running":{}},"lastState":{"terminated":{"reason":"Error","exitCode":1}}}]}`, "exits with code 1 (Error)", false},
		"crash loop":    {`{"containerStatuses":[{"ready":false,"restartCount":4,"state":{"waiting":{"reason":"CrashLoopBackOff","message":"back-off 40s"}}}]}`, "CrashLoopBackOff (back-off 40s)", false},
		"image pull":    {`{"containerStatuses":[{"ready":false,"restartCount":0,"state":{"waiting":{"reason":"ImagePullBackOff"}}}]}`, "ImagePullBackOff", false},
		"pulling image": {`{"containerStatuses":[{"ready":false,"restartCount":0,"state":{"waiting":{"reason":"ContainerCreating"}}}]}`, "", false},
	} {
		err := parse(c.status).startFailure()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v, want none", name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: %v, want %q", name, err, c.want)
		case c.want != "" && errors.Is(err, ErrNoAnswer) != c.noAnswer:
			t.Errorf("%s: ErrNoAnswer is %v", name, errors.Is(err, ErrNoAnswer))
		}
	}
}
