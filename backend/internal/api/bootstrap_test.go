package api

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/portdock/portdock/internal/deploy"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/kube"
	"github.com/portdock/portdock/internal/store"
)

// With PORTDOCK_SETUP_TOKEN (set by the Kubernetes installer), the owner
// account is created, or a backup restored, only with the token: a fresh
// instance on a public domain is not claimed by its first visitor.
func TestSetupToken(t *testing.T) {
	t.Setenv("PORTDOCK_SETUP_TOKEN", "s3cret-setup-token")
	e := newTestEnv(t)
	c := e.client()

	if _, st := e.do(c, "GET", "/api/auth/status", nil); st["setup_required"] != true || st["setup_token_required"] != true {
		t.Fatalf("status of a fresh protected instance: %v", st)
	}
	restore := func(token string) int {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		if token != "" {
			_ = mw.WriteField("setup_token", token)
		}
		_ = mw.WriteField("passphrase", "a long backup passphrase")
		fw, _ := mw.CreateFormFile("file", "portdock.age")
		_, _ = fw.Write([]byte("not a backup"))
		mw.Close()
		res, err := http.Post(e.srv.URL+"/api/setup/restore", mw.FormDataContentType(), &body)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		_, _ = io.Copy(io.Discard, res.Body)
		return res.StatusCode
	}
	if code := restore(""); code != 403 {
		t.Fatalf("a restore without the setup token: %d", code)
	}
	if code := restore("s3cret-setup-token"); code == 403 {
		t.Fatal("a restore with the setup token must reach the backup")
	}

	body := map[string]string{"email": "owner@corp.com", "password": "correct-horse"}
	if code, res := e.do(c, "POST", "/api/auth/register", body); code != 403 || !strings.Contains(res["error"].(string), "setup token") {
		t.Fatalf("an owner without the setup token: %d %v", code, res)
	}
	body["setup_token"] = "s3cret-setup-tokeN"
	if code, _ := e.do(c, "POST", "/api/auth/register", body); code != 403 {
		t.Fatalf("an owner with a wrong setup token: %d", code)
	}
	body["setup_token"] = " s3cret-setup-token "
	if code, res := e.do(c, "POST", "/api/auth/register", body); code != 200 {
		t.Fatalf("an owner with the setup token: %d %v", code, res)
	}
	if _, st := e.do(e.client(), "GET", "/api/auth/status", nil); st["setup_required"] != false || st["setup_token_required"] != nil {
		t.Fatalf("status once set up: %v", st)
	}
}

// Without PORTDOCK_SETUP_TOKEN nothing changes: the first visitor creates the owner.
func TestNoSetupToken(t *testing.T) {
	e := newTestEnv(t)
	if _, st := e.do(e.client(), "GET", "/api/auth/status", nil); st["setup_token_required"] != nil {
		t.Fatalf("status: %v", st)
	}
	if code, res := e.do(e.client(), "POST", "/api/auth/register", map[string]string{"email": "owner@corp.com", "password": "correct-horse"}); code != 200 {
		t.Fatalf("register: %d %v", code, res)
	}
}

// In a pod, the owner account finds what the installer prepared: a server
// per namespace with the registry and certificate issuer of the cluster, and
// the registry itself, with its password encrypted.
func TestBootstrapCluster(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.43.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	t.Setenv("PORTDOCK_KUBE_SERVERS", "test=portdock-test, prod=portdock-prod, broken")
	t.Setenv("PORTDOCK_KUBE_CLUSTER_ISSUER", "letsencrypt")
	t.Setenv("PORTDOCK_REGISTRY_HOST", "https://panel.example.com/")
	t.Setenv("PORTDOCK_REGISTRY_USERNAME", "portdock")
	t.Setenv("PORTDOCK_REGISTRY_PASSWORD", "registry-password")
	notSocket := filepath.Join(t.TempDir(), "docker.sock")
	if err := os.WriteFile(notSocket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	defer func(old string) { dockerSocket = old }(dockerSocket)
	dockerSocket = notSocket

	e := newTestEnv(t)
	e.api.pool.Cluster = func(srv store.Server, _ string) (executor.Executor, error) {
		return kube.NewWithRunner(srv.KubeNamespace, func(context.Context, io.Reader, io.Writer, io.Writer, ...string) error { return nil }), nil
	}
	if code, res := e.do(e.client(), "POST", "/api/auth/register", map[string]string{"email": "owner@corp.com", "password": "correct-horse"}); code != 200 {
		t.Fatalf("register: %d %v", code, res)
	}

	regs, err := store.Select[store.Registry](t.Context(), e.db, "SELECT * FROM registries")
	if err != nil || len(regs) != 1 {
		t.Fatalf("registries: %v %v", regs, err)
	}
	reg := regs[0]
	if reg.Host != "panel.example.com" || reg.Kind != "generic" || reg.Username != "portdock" || e.api.box.MustDecrypt(reg.Password) != "registry-password" || reg.Password == "registry-password" {
		t.Fatalf("registry: %+v", reg)
	}
	servers, err := store.Select[store.Server](t.Context(), e.db, "SELECT * FROM servers ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 {
		t.Fatalf("expected the servers test and prod only (no Docker socket, a broken entry): %+v", servers)
	}
	for i, want := range []struct{ name, namespace string }{{"test", "portdock-test"}, {"prod", "portdock-prod"}} {
		srv := servers[i]
		if srv.Name != want.name || srv.KubeNamespace != want.namespace || !srv.IsKubernetes() || srv.Host != "in-cluster" ||
			srv.KubeClusterIssuer != "letsencrypt" || srv.KubeRegistryID != reg.ID || srv.IsLocal || srv.ProxyEnabled {
			t.Fatalf("server %d: %+v", i, srv)
		}
	}

	// Once is enough: running again adds nothing.
	if err := e.api.bootstrapCluster(t.Context()); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = e.db.QueryRowContext(t.Context(), "SELECT (SELECT COUNT(*) FROM servers) + (SELECT COUNT(*) FROM registries)").Scan(&n)
	if n != 3 {
		t.Fatalf("a second run added rows: %d", n)
	}
}

// Without PORTDOCK_KUBE_SERVERS, the cluster is one server, as before.
func TestBootstrapClusterDefault(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.43.0.1")
	t.Setenv("PORTDOCK_KUBE_NAMESPACE", "apps")
	defer func(old string) { dockerSocket = old }(dockerSocket)
	dockerSocket = filepath.Join(t.TempDir(), "missing.sock")
	e := newTestEnv(t)
	e.api.pool.Cluster = func(srv store.Server, _ string) (executor.Executor, error) {
		return kube.NewWithRunner(srv.KubeNamespace, func(context.Context, io.Reader, io.Writer, io.Writer, ...string) error { return nil }), nil
	}
	if err := e.api.bootstrapCluster(t.Context()); err != nil {
		t.Fatal(err)
	}
	servers, _ := store.Select[store.Server](t.Context(), e.db, "SELECT * FROM servers")
	if len(servers) != 1 || servers[0].Name != "cluster" || servers[0].KubeNamespace != "apps" || servers[0].KubeRegistryID != 0 {
		t.Fatalf("servers: %+v", servers)
	}
}

// The node's Docker socket, mounted in the pod, becomes the server "build":
// local, and without a proxy (ports 80 and 443 belong to the cluster).
func TestBootstrapBuildServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix sockets do not report as sockets on Windows")
	}
	sock := filepath.Join(t.TempDir(), "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("no Unix sockets here:", err)
	}
	defer l.Close()
	set := make(chan store.Server, 1)
	defer func(old string, oldSetup func(*deploy.Engine, store.Server)) {
		dockerSocket, setupBuildServer = old, oldSetup
	}(dockerSocket, setupBuildServer)
	dockerSocket = sock
	setupBuildServer = func(_ *deploy.Engine, srv store.Server) { set <- srv }

	e := newTestEnv(t)
	for range 2 {
		if err := e.api.bootstrapBuildServer(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	servers, _ := store.Select[store.Server](t.Context(), e.db, "SELECT * FROM servers")
	if len(servers) != 1 {
		t.Fatalf("servers: %+v", servers)
	}
	srv := servers[0]
	if srv.Name != "build" || !srv.IsLocal || srv.IsKubernetes() || srv.ProxyEnabled {
		t.Fatalf("build server: %+v", srv)
	}
	if got := <-set; got.ID != srv.ID {
		t.Fatalf("set up %+v, not the build server", got)
	}
}

// A cluster names the registry its applications push to; the registry must
// exist, a Docker server has none, and deleting the registry unsets it.
func TestClusterRegistry(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	c := e.client()
	if code, _ := e.do(c, "POST", "/api/auth/login", map[string]string{"email": "owner@corp.com", "password": "correct-horse"}); code != 200 {
		t.Fatalf("login: %d", code)
	}
	id := mustInsertCluster(t, e)
	path := "/api/servers/" + strconv.FormatInt(id, 10)
	if code, res := e.do(c, "PATCH", path, map[string]any{"kube_registry_id": 4242}); code != 400 || !strings.Contains(res["error"].(string), "registry") {
		t.Fatalf("an unknown registry: %d %v", code, res)
	}
	code, reg := e.do(c, "POST", "/api/registries", map[string]any{"kind": "generic", "host": "registry.corp", "username": "u", "password": "p"})
	if code != 200 {
		t.Fatalf("registry: %d %v", code, reg)
	}
	regID := int64(reg["id"].(float64))
	if code, res := e.do(c, "PATCH", path, map[string]any{"kube_registry_id": regID}); code != 200 || res["kube_registry_id"] != float64(regID) {
		t.Fatalf("set the registry: %d %v", code, res)
	}

	docker := store.Server{Name: "edge", Kind: "docker", Host: "10.0.0.9", Port: 22, User: "root", SSHKeyID: 1, CreatedAt: store.Now()}
	if _, err := store.Insert(t.Context(), e.db, "servers", &docker); err != nil {
		t.Fatal(err)
	}
	if code, res := e.do(c, "PATCH", "/api/servers/"+strconv.FormatInt(docker.ID, 10), map[string]any{"name": "edge", "host": "10.0.0.9", "ssh_key_id": 1, "kube_registry_id": regID}); code != 200 || res["kube_registry_id"] != float64(0) {
		t.Fatalf("a Docker server has no cluster registry: %d %v", code, res)
	}

	if code, res := e.do(c, "DELETE", "/api/registries/"+strconv.FormatInt(regID, 10), nil); code != 200 {
		t.Fatalf("delete the registry: %d %v", code, res)
	}
	if srv, _ := store.ByID[store.Server](t.Context(), e.db, "servers", id); srv.KubeRegistryID != 0 {
		t.Fatalf("the cluster still names a deleted registry: %d", srv.KubeRegistryID)
	}
}

// The domains of the cluster PortDock runs in must point to this machine's
// public address; of another cluster, the address is not known.
func TestClusterServerIPs(t *testing.T) {
	e := newTestEnv(t)
	_ = e.db.SetSetting(t.Context(), "public_ipv4", "138.199.174.93")
	inCluster := store.Server{Kind: store.KindKubernetes, Host: "in-cluster", KubeNamespace: "portdock-test"}
	if ips := e.api.serverIPs(t.Context(), inCluster); len(ips) != 1 || ips[0].String() != "138.199.174.93" {
		t.Fatalf("in-cluster: %v", ips)
	}
	remote := store.Server{Kind: store.KindKubernetes, Host: "https://10.0.0.1:6443", KubeNamespace: "apps"}
	if ips := e.api.serverIPs(t.Context(), remote); ips != nil {
		t.Fatalf("another cluster: %v", ips)
	}
	if status, _ := checkDNS(t.Context(), "localhost", e.api.serverIPs(t.Context(), inCluster)); status != "local" {
		t.Fatalf("checkDNS still answers: %s", status)
	}
}
