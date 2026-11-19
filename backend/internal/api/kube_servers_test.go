package api

import (
	"context"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/kube"
	"github.com/portdock/portdock/internal/store"
)

const testKubeconfig = `apiVersion: v1
kind: Config
current-context: portdock
clusters:
- name: prod
  cluster:
    server: https://10.0.0.1:6443
    certificate-authority-data: LS0tLS1CRUdJTg==
contexts:
- name: portdock
  context: {cluster: prod, user: portdock}
users:
- name: portdock
  user:
    token: super-secret-service-account-token
`

// A cluster is added, shown and changed through the API without its
// credentials ever coming back, and what needs Docker is refused on it.
func TestKubernetesServerAPI(t *testing.T) {
	e := newTestEnv(t)
	// The cluster answers: kubectl is scripted, and gets the kubeconfig file the pool wrote.
	var kubeconfigs []string
	e.api.pool.Cluster = func(srv store.Server, kubeconfig string) (executor.Executor, error) {
		kubeconfigs = append(kubeconfigs, kubeconfig)
		return kube.NewWithRunner(srv.KubeNamespace, func(_ context.Context, _ io.Reader, out, _ io.Writer, args ...string) error {
			switch {
			case strings.Contains(strings.Join(args, " "), "get --raw /version"):
				_, _ = io.WriteString(out, `{"gitVersion":"v1.31.2"}`)
			case strings.Contains(strings.Join(args, " "), "auth can-i"):
				_, _ = io.WriteString(out, "yes\n")
			}
			return nil
		}), nil
	}
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("dev@corp.com", "correct-horse", "member")
	owner, member := e.client(), e.client()
	for c, email := range map[*http.Client]string{owner: "owner@corp.com", member: "dev@corp.com"} {
		if code, _ := e.do(c, "POST", "/api/auth/login", map[string]string{"email": email, "password": "correct-horse"}); code != 200 {
			t.Fatalf("login %s: %d", email, code)
		}
	}
	cluster := func(kubeconfig string, more map[string]any) map[string]any {
		body := map[string]any{"kind": "kubernetes", "name": "prod-cluster", "kubeconfig": kubeconfig, "kube_namespace": "apps"}
		for k, v := range more {
			body[k] = v
		}
		return body
	}

	if code, _ := e.do(member, "POST", "/api/servers", cluster(testKubeconfig, nil)); code != 403 {
		t.Fatalf("a member must not add servers: %d", code)
	}
	// A kubeconfig that runs a program would run it on the PortDock machine.
	exec := strings.Replace(testKubeconfig, "    token: super-secret-service-account-token\n", "    exec:\n      command: /bin/sh\n      args: [\"-c\", \"id > /tmp/pwned\"]\n", 1)
	if code, res := e.do(owner, "POST", "/api/servers", cluster(exec, nil)); code != 400 || !strings.Contains(res["error"].(string), "program") {
		t.Fatalf("an exec kubeconfig must be refused: %d %v", code, res)
	}
	for what, body := range map[string]map[string]any{
		"no kubeconfig":            cluster("", nil),
		"a system namespace":       cluster(testKubeconfig, map[string]any{"kube_namespace": "kube-system"}),
		"an option as namespace":   cluster(testKubeconfig, map[string]any{"kube_namespace": "apps --all-namespaces"}),
		"an invalid ingress class": cluster(testKubeconfig, map[string]any{"kube_ingress_class": "nginx; rm -rf /"}),
		"an invalid volume size":   cluster(testKubeconfig, map[string]any{"kube_volume_size": "lots"}),
	} {
		if code, res := e.do(owner, "POST", "/api/servers", body); code != 400 {
			t.Fatalf("%s must be refused: %d %v", what, code, res)
		}
	}

	code, srv := e.do(owner, "POST", "/api/servers", cluster(testKubeconfig, map[string]any{"kube_ingress_class": "nginx", "kube_cluster_issuer": "letsencrypt", "kube_volume_size": "10Gi", "proxy_enabled": true, "is_local": true}))
	if code != 200 {
		t.Fatalf("create: %d %v", code, srv)
	}
	id := int64(srv["id"].(float64))
	if srv["kind"] != "kubernetes" || srv["host"] != "https://10.0.0.1:6443" || srv["has_kubeconfig"] != true || srv["proxy_enabled"] != false ||
		srv["is_local"] != false || srv["status"] != "reachable" || srv["docker_version"] != "v1.31.2" || srv["kube_namespace"] != "apps" {
		t.Fatalf("server: %v", srv)
	}
	if _, leaked := srv["kubeconfig"]; leaked {
		t.Fatal("the kubeconfig must never be returned")
	}
	// Stored encrypted; kubectl got the plain file.
	row, _ := store.ByID[store.Server](t.Context(), e.db, "servers", id)
	if strings.Contains(row.Kubeconfig, "super-secret") || row.Kubeconfig == "" {
		t.Fatalf("the kubeconfig must be stored encrypted: %q", row.Kubeconfig)
	}
	if len(kubeconfigs) == 0 || !strings.Contains(kubeconfigs[0], "super-secret-service-account-token") {
		t.Fatalf("the pool must hand the decrypted kubeconfig to the cluster: %v", kubeconfigs)
	}
	for _, path := range []string{"/api/servers", "/api/servers/" + strconv.FormatInt(id, 10)} {
		req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
		res, err := owner.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if strings.Contains(string(raw), "super-secret") || strings.Contains(string(raw), row.Kubeconfig) {
			t.Fatalf("%s leaks the kubeconfig: %s", path, raw)
		}
	}
	if code, res := e.do(owner, "POST", "/api/servers", cluster(testKubeconfig, map[string]any{"name": "again"})); code != 400 {
		t.Fatalf("the same namespace of the same cluster twice: %d %v", code, res)
	}

	// Changing it: the kind stays, a bad kubeconfig is refused, the stored one survives an edit.
	code, upd := e.do(owner, "PATCH", "/api/servers/"+strconv.FormatInt(id, 10), map[string]any{"name": "prod", "kind": "docker", "host": "evil.example.com", "kube_ingress_class": "traefik"})
	if code != 200 || upd["kind"] != "kubernetes" || upd["host"] != "https://10.0.0.1:6443" || upd["kube_ingress_class"] != "traefik" || upd["has_kubeconfig"] != true {
		t.Fatalf("update: %d %v", code, upd)
	}
	if code, _ := e.do(owner, "PATCH", "/api/servers/"+strconv.FormatInt(id, 10), map[string]any{"kubeconfig": exec}); code != 400 {
		t.Fatalf("an exec kubeconfig must be refused on update: %d", code)
	}
	if after, _ := store.ByID[store.Server](t.Context(), e.db, "servers", id); after.Kubeconfig != row.Kubeconfig {
		t.Fatal("the kubeconfig changed without a new one being sent")
	}

	// What needs Docker is refused on a cluster, with a reason.
	_, project := e.do(owner, "POST", "/api/projects", map[string]any{"name": "shop"})
	envID := project["environments"].([]any)[0].(map[string]any)["id"]
	for what, req := range map[string]struct {
		path string
		body map[string]any
	}{
		"a database":            {"/api/databases", map[string]any{"name": "pg", "engine": "postgres", "server_id": id, "environment_id": envID}},
		"a compose application": {"/api/applications", map[string]any{"name": "stack", "server_id": id, "environment_id": envID, "source_type": "git", "build_pack": "compose", "git_repository": "https://git.corp/a/b.git"}},
		"previews":              {"/api/applications", map[string]any{"name": "web", "server_id": id, "environment_id": envID, "source_type": "image", "build_pack": "image", "docker_image": "nginx", "preview_enabled": true}},
		"a cluster as build server": {"/api/applications", map[string]any{"name": "api", "server_id": id, "environment_id": envID, "source_type": "image", "build_pack": "image", "docker_image": "nginx",
			"build_server_id": mustInsertCluster(t, e)}},
	} {
		if code, res := e.do(owner, "POST", req.path, req.body); code != 400 {
			t.Fatalf("%s on a cluster must be refused: %d %v", what, code, res)
		}
	}
	code, app := e.do(owner, "POST", "/api/applications", map[string]any{"name": "web", "server_id": id, "environment_id": envID, "source_type": "image", "build_pack": "image", "docker_image": "nginx:1.27", "port": 80})
	if code != 200 {
		t.Fatalf("an image application on a cluster: %d %v", code, app)
	}
	// With an application on it, the server cannot be pointed at another namespace.
	if code, res := e.do(owner, "PATCH", "/api/servers/"+strconv.FormatInt(id, 10), map[string]any{"kube_namespace": "elsewhere"}); code != 400 {
		t.Fatalf("moving a namespace with applications: %d %v", code, res)
	}

	// The kubeconfig file goes with the server.
	file := kube.ConfigPath(e.dir, 999)
	if _, err := kube.WriteConfig(e.dir, 999, testKubeconfig); err != nil {
		t.Fatal(err)
	}
	other := store.Server{ID: 999, Name: "temp", Kind: store.KindKubernetes, Host: "https://10.9.9.9:6443", KubeNamespace: "temp", CreatedAt: store.Now()}
	if _, err := e.db.ExecContext(t.Context(), "INSERT INTO servers(id, name, kind, host, kube_namespace, created_at) VALUES(?, ?, ?, ?, ?, ?)",
		other.ID, other.Name, other.Kind, other.Host, other.KubeNamespace, other.CreatedAt); err != nil {
		t.Fatal(err)
	}
	if code, res := e.do(owner, "DELETE", "/api/servers/999", nil); code != 200 {
		t.Fatalf("delete: %d %v", code, res)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the kubeconfig file of a deleted server must be removed: %v", err)
	}
}

// mustInsertCluster adds a second cluster directly and returns its id.
func mustInsertCluster(t *testing.T, e *testEnv) int64 {
	t.Helper()
	srv := store.Server{Name: "builder", Kind: store.KindKubernetes, Host: "https://10.0.0.2:6443", KubeNamespace: "build", CreatedAt: store.Now()}
	if _, err := store.Insert(t.Context(), e.db, "servers", &srv); err != nil {
		t.Fatal(err)
	}
	return srv.ID
}
