package api

import (
	"context"
	"github.com/portdock/portdock/internal/auth"
	"net/http"
	"regexp"
	"strings"

	"github.com/portdock/portdock/internal/deploy"
	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/kube"
	"github.com/portdock/portdock/internal/proxy"
	"github.com/portdock/portdock/internal/store"
)

// ---- SSH keys ----

func (s *Server) listSSHKeys(w http.ResponseWriter, r *http.Request) error {
	keys, err := store.Select[store.SSHKey](r.Context(), s.db, "SELECT * FROM ssh_keys ORDER BY id")
	if err != nil {
		return err
	}
	return ok(w, keys)
}

func (s *Server) createSSHKey(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name       string `json:"name"`
		PrivateKey string `json:"private_key"` // empty = generate a new Ed25519 key
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if strings.TrimSpace(in.Name) == "" {
		return badRequest("name is required")
	}
	priv := strings.TrimSpace(in.PrivateKey)
	var pub string
	var err error
	if priv == "" {
		if priv, pub, err = executor.GenerateKeyPair(); err != nil {
			return err
		}
	} else {
		priv += "\n"
		if pub, err = executor.PublicKeyFromPrivate(priv); err != nil {
			return badRequest("invalid private key: %v", err)
		}
	}
	k := store.SSHKey{Name: in.Name, PublicKey: pub, PrivateKey: s.box.Encrypt(priv), CreatedAt: store.Now()}
	if _, err := store.Insert(r.Context(), s.db, "ssh_keys", &k); err != nil {
		return err
	}
	s.activity(r, "ssh_key.create", k.Name)
	return ok(w, k)
}

func (s *Server) deleteSSHKey(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var n int
	_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM servers WHERE ssh_key_id = ?", id).Scan(&n)
	if n > 0 {
		return badRequest("key is used by %d server(s)", n)
	}
	if err := store.Delete(r.Context(), s.db, "ssh_keys", id); err != nil {
		return err
	}
	return ok(w, map[string]bool{"ok": true})
}

// ---- servers ----

// serverOut prepares a server for a response: the kubeconfig of a cluster is
// never sent, only whether there is one.
func serverOut(srv store.Server) store.Server {
	srv.HasKubeconfig = srv.Kubeconfig != ""
	if srv.Kind == "" {
		srv.Kind = "docker"
	}
	return srv
}

// serverInput is a server as the API receives it: the kubeconfig is write-only.
type serverInput struct {
	*store.Server
	Kubeconfig *string `json:"kubeconfig"`
}

func (s *Server) listServers(w http.ResponseWriter, r *http.Request) error {
	srvs, err := store.Select[store.Server](r.Context(), s.db, "SELECT * FROM servers ORDER BY is_local DESC, id")
	if err != nil {
		return err
	}
	for i := range srvs {
		srvs[i] = serverOut(srvs[i])
	}
	return ok(w, srvs)
}

func (s *Server) loadServer(r *http.Request) (store.Server, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.Server{}, err
	}
	return store.ByID[store.Server](r.Context(), s.db, "servers", id)
}

func (s *Server) serverExec(r *http.Request) (executor.Executor, store.Server, error) {
	srv, err := s.loadServer(r)
	if err != nil {
		return nil, srv, err
	}
	x, err := s.pool.For(r.Context(), srv)
	if err != nil {
		return nil, srv, badRequest("%v", err)
	}
	return x, srv, nil
}

func (s *Server) getServer(w http.ResponseWriter, r *http.Request) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	return ok(w, serverOut(srv))
}

var (
	kubeNameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)
	kubeSizeRe = regexp.MustCompile(`^[1-9][0-9]{0,5}(Mi|Gi|Ti)$`)
)

// validateCluster checks the settings of a server that is a Kubernetes cluster.
func validateCluster(srv *store.Server) error {
	srv.KubeNamespace = strings.TrimSpace(srv.KubeNamespace)
	if srv.KubeNamespace == "" {
		srv.KubeNamespace = "portdock"
	}
	if err := kube.CheckNamespace(srv.KubeNamespace); err != nil {
		return badRequest("%v", err)
	}
	for _, f := range []struct {
		name string
		val  *string
	}{{"ingress class", &srv.KubeIngressClass}, {"cluster issuer", &srv.KubeClusterIssuer}, {"storage class", &srv.KubeStorageClass}} {
		*f.val = strings.TrimSpace(*f.val)
		if *f.val != "" && !kubeNameRe.MatchString(*f.val) {
			return badRequest("invalid %s %q: lowercase letters, digits, '-' and '.'", f.name, *f.val)
		}
	}
	srv.KubeVolumeSize = strings.TrimSpace(srv.KubeVolumeSize)
	if srv.KubeVolumeSize != "" && !kubeSizeRe.MatchString(srv.KubeVolumeSize) {
		return badRequest("invalid volume size %q: a number followed by Mi, Gi or Ti (e.g. 5Gi)", srv.KubeVolumeSize)
	}
	// A cluster has no SSH connection and no proxy of PortDock's: its ingress
	// controller routes the domains.
	srv.IsLocal, srv.SSHKeyID, srv.Port, srv.User, srv.ProxyEnabled = false, 0, 0, "", false
	return nil
}

// setKubeconfig validates and stores the kubeconfig of a cluster; its
// cluster's address becomes the server's host.
func (s *Server) setKubeconfig(srv *store.Server, raw string) error {
	server, err := kube.CheckKubeconfig(raw)
	if err != nil {
		return badRequest("%v", err)
	}
	srv.Kubeconfig, srv.Host = s.box.Encrypt(raw), server
	return nil
}

func validateServer(srv *store.Server) error {
	srv.Name = strings.TrimSpace(srv.Name)
	srv.Host = strings.TrimSpace(srv.Host)
	if srv.Name == "" {
		return badRequest("name is required")
	}
	if srv.IsKubernetes() {
		return validateCluster(srv)
	}
	srv.Kind = "docker"
	srv.Kubeconfig, srv.KubeNamespace, srv.KubeIngressClass, srv.KubeClusterIssuer, srv.KubeStorageClass, srv.KubeVolumeSize = "", "", "", "", "", ""
	if !srv.IsLocal {
		if srv.Host == "" {
			return badRequest("host is required")
		}
		if srv.SSHKeyID == 0 {
			return badRequest("an SSH key is required")
		}
	}
	if srv.Port == 0 {
		srv.Port = 22
	}
	if srv.User == "" {
		srv.User = "root"
	}
	return nil
}

func (s *Server) createServer(w http.ResponseWriter, r *http.Request) error {
	srv := store.Server{Port: 22, User: "root", ProxyEnabled: true}
	in := serverInput{Server: &srv}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	srv.ID, srv.Status, srv.ProxyStatus, srv.CreatedAt = 0, "unknown", "unknown", store.Now()
	srv.IsLocal = false
	// The private network is set in its own settings.
	srv.OverlayJoin, srv.OverlayIP, srv.OverlayPublicKey, srv.OverlayPrivateKey, srv.OverlayEndpoint = false, "", "", "", ""
	if srv.IsKubernetes() {
		switch {
		case in.Kubeconfig != nil && strings.TrimSpace(*in.Kubeconfig) != "":
			if err := s.setKubeconfig(&srv, *in.Kubeconfig); err != nil {
				return err
			}
		case kube.InCluster():
			// PortDock runs in a pod of the cluster and uses its service account.
			srv.Host = "in-cluster"
		default:
			return badRequest("a kubeconfig is required")
		}
	}
	if err := validateServer(&srv); err != nil {
		return err
	}
	if srv.IsKubernetes() {
		var n int
		_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM servers WHERE kind = ? AND host = ? AND kube_namespace = ?",
			store.KindKubernetes, srv.Host, srv.KubeNamespace).Scan(&n)
		if n > 0 {
			return badRequest("the namespace %q of this cluster is already a server", srv.KubeNamespace)
		}
	}
	if _, err := store.Insert(r.Context(), s.db, "servers", &srv); err != nil {
		return err
	}
	s.activity(r, "server.create", srv.Name)
	srv, _ = s.engine.ValidateServer(r.Context(), srv)
	return ok(w, serverOut(srv))
}

func (s *Server) updateServer(w http.ResponseWriter, r *http.Request) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	id, local, created := srv.ID, srv.IsLocal, srv.CreatedAt
	orig := srv
	in := serverInput{Server: &srv}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	srv.ID, srv.IsLocal, srv.CreatedAt = id, local, created
	srv.OverlayJoin, srv.OverlayIP, srv.OverlayPublicKey, srv.OverlayPrivateKey, srv.OverlayEndpoint = orig.OverlayJoin, orig.OverlayIP, orig.OverlayPublicKey, orig.OverlayPrivateKey, orig.OverlayEndpoint
	// A Docker server does not become a cluster, nor the reverse: what runs
	// on it would be lost from sight.
	srv.Kind = orig.Kind
	if srv.IsKubernetes() {
		srv.Host = orig.Host
		if in.Kubeconfig != nil && strings.TrimSpace(*in.Kubeconfig) != "" {
			if err := s.setKubeconfig(&srv, *in.Kubeconfig); err != nil {
				return err
			}
		}
	}
	if err := validateServer(&srv); err != nil {
		return err
	}
	if srv.IsKubernetes() && (srv.KubeNamespace != orig.KubeNamespace || srv.Host != orig.Host) {
		var n int
		_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM applications WHERE server_id = ?", srv.ID).Scan(&n)
		if n > 0 {
			return badRequest("%d application(s) run in the namespace %q of this cluster: move or delete them before pointing the server elsewhere", n, orig.KubeNamespace)
		}
	}
	if err := store.Update(r.Context(), s.db, "servers", &srv); err != nil {
		return err
	}
	s.pool.Forget(srv.ID)
	s.activity(r, "server.update", srv.Name)
	return ok(w, serverOut(srv))
}

func (s *Server) deleteServer(w http.ResponseWriter, r *http.Request) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	var n int
	_ = s.db.QueryRowContext(r.Context(), `SELECT (SELECT COUNT(*) FROM applications WHERE server_id = ?1) +
		(SELECT COUNT(*) FROM databases WHERE server_id = ?1) + (SELECT COUNT(*) FROM services WHERE server_id = ?1)`, srv.ID).Scan(&n)
	if n > 0 {
		return badRequest("server still hosts %d resource(s); move or delete them first", n)
	}
	if err := store.Delete(r.Context(), s.db, "servers", srv.ID); err != nil {
		return err
	}
	_ = s.pool.ResetHostKey(r.Context(), srv.ID)
	if srv.IsKubernetes() {
		kube.RemoveConfig(s.cfg.DataDir, srv.ID)
	}
	s.activity(r, "server.delete", srv.Name)
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) validateServer(w http.ResponseWriter, r *http.Request) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	srv, _ = s.engine.ValidateServer(r.Context(), srv)
	return ok(w, serverOut(srv))
}

func (s *Server) setupServer(w http.ResponseWriter, r *http.Request) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	var in struct {
		Nixpacks bool `json:"nixpacks"`
	}
	_ = readJSON(r, &in)
	go func() { _ = s.engine.SetupServer(context.Background(), srv, in.Nixpacks) }()
	s.activity(r, "server.setup", srv.Name)
	return ok(w, map[string]bool{"started": true})
}

func (s *Server) serverSetupStream(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	return s.sseTopic(w, r, deploy.ServerTopic(id), "")
}

func (s *Server) resetHostKey(w http.ResponseWriter, r *http.Request) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	if err := s.pool.ResetHostKey(r.Context(), srv.ID); err != nil {
		return err
	}
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) serverMetrics(w http.ResponseWriter, r *http.Request) error {
	x, _, err := s.serverExec(r)
	if err != nil {
		return err
	}
	m, err := docker.ServerMetrics(r.Context(), x)
	if err != nil {
		return badRequest("%v", err)
	}
	return ok(w, m)
}

func (s *Server) serverStats(w http.ResponseWriter, r *http.Request) error {
	x, _, err := s.serverExec(r)
	if err != nil {
		return err
	}
	stats, err := docker.Stats(r.Context(), x)
	if err != nil {
		return badRequest("%v", err)
	}
	if !auth.UserFrom(r.Context()).IsAdmin() {
		// Members see the usage of their own containers only.
		cs, err := docker.PS(r.Context(), x, true)
		if err != nil {
			return badRequest("%v", err)
		}
		visible := map[string]bool{}
		for _, c := range s.visibleContainers(r, cs) {
			visible[c.Name] = true
			if len(c.ID) >= 12 {
				visible[c.ID[:12]] = true
			}
		}
		stats = filterBy(stats, func(st docker.Stat) bool {
			id := st.Container
			if len(id) >= 12 {
				id = id[:12]
			}
			return visible[st.Name] || visible[id]
		})
	}
	return ok(w, stats)
}

// visibleContainers keeps the containers of resources the user can access.
func (s *Server) visibleContainers(r *http.Request, cs []docker.Container) []docker.Container {
	if auth.UserFrom(r.Context()).IsAdmin() {
		return cs
	}
	uuids := map[string]int64{}
	for _, t := range []string{"applications", "databases", "services"} {
		type row struct {
			UUID string `db:"uuid"`
			Env  int64  `db:"environment_id"`
		}
		rows, _ := store.Select[row](r.Context(), s.db, "SELECT uuid, environment_id FROM "+t)
		for _, x := range rows {
			uuids[x.UUID] = x.Env
		}
	}
	visible := s.envVisibility(r.Context())
	return filterBy(cs, func(c docker.Container) bool {
		env, ok := uuids[c.Labels[docker.LabelResource]]
		return ok && visible(env)
	})
}

func (s *Server) serverContainers(w http.ResponseWriter, r *http.Request) error {
	x, _, err := s.serverExec(r)
	if err != nil {
		return err
	}
	cs, err := docker.PS(r.Context(), x, true)
	if err != nil {
		return badRequest("%v", err)
	}
	return ok(w, s.visibleContainers(r, cs))
}

func (s *Server) containerAction(w http.ResponseWriter, r *http.Request) error {
	x, srv, err := s.serverExec(r)
	if err != nil {
		return err
	}
	cid, action := r.PathValue("cid"), r.PathValue("action")
	if err := s.authorizeContainer(r, x, cid, permWrite); err != nil {
		return err
	}
	if err := docker.ContainerAction(r.Context(), x, action, cid); err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "container."+action, srv.Name+"/"+cid)
	s.cache.forget("ps:")
	s.cache.forget("inspect:")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) containerInspect(w http.ResponseWriter, r *http.Request) error {
	x, _, err := s.serverExec(r)
	if err != nil {
		return err
	}
	if err := s.authorizeContainer(r, x, r.PathValue("cid"), permRead); err != nil {
		return err
	}
	raw, err := docker.Inspect(r.Context(), x, r.PathValue("cid"))
	if err != nil {
		return badRequest("%v", err)
	}
	if s.authorizeContainer(r, x, r.PathValue("cid"), permWrite) != nil {
		raw = stripEnv(raw)
	}
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(raw)
	return err
}

func (s *Server) containerLogs(w http.ResponseWriter, r *http.Request) error {
	x, _, err := s.serverExec(r)
	if err != nil {
		return err
	}
	if err := s.authorizeContainer(r, x, r.PathValue("cid"), permRead); err != nil {
		return err
	}
	return s.sseContainerLogs(w, r, x, r.PathValue("cid"))
}

func (s *Server) serverImages(w http.ResponseWriter, r *http.Request) error {
	x, _, err := s.serverExec(r)
	if err != nil {
		return err
	}
	imgs, err := docker.Images(r.Context(), x)
	if err != nil {
		return badRequest("%v", err)
	}
	return ok(w, imgs)
}

func (s *Server) deleteImage(w http.ResponseWriter, r *http.Request) error {
	x, _, err := s.serverExec(r)
	if err != nil {
		return err
	}
	if err := docker.RemoveImage(r.Context(), x, r.PathValue("iid")); err != nil {
		return badRequest("%v", err)
	}
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) serverVolumes(w http.ResponseWriter, r *http.Request) error {
	x, _, err := s.serverExec(r)
	if err != nil {
		return err
	}
	vols, err := docker.Volumes(r.Context(), x)
	if err != nil {
		return badRequest("%v", err)
	}
	return ok(w, vols)
}

func (s *Server) deleteVolume(w http.ResponseWriter, r *http.Request) error {
	x, _, err := s.serverExec(r)
	if err != nil {
		return err
	}
	if err := docker.RemoveVolume(r.Context(), x, r.PathValue("name")); err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "volume.delete", r.PathValue("name"))
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) serverNetworks(w http.ResponseWriter, r *http.Request) error {
	x, _, err := s.serverExec(r)
	if err != nil {
		return err
	}
	nets, err := docker.Networks(r.Context(), x)
	if err != nil {
		return badRequest("%v", err)
	}
	return ok(w, nets)
}

func (s *Server) pruneServer(w http.ResponseWriter, r *http.Request) error {
	x, srv, err := s.serverExec(r)
	if err != nil {
		return err
	}
	var in struct {
		Scope string `json:"scope"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	out, err := docker.Prune(r.Context(), x, in.Scope)
	if err != nil {
		return badRequest("%v", err)
	}
	s.activity(r, "server.prune."+in.Scope, srv.Name)
	return ok(w, map[string]string{"output": out})
}

func (s *Server) proxyAction(w http.ResponseWriter, r *http.Request) error {
	x, srv, err := s.serverExec(r)
	if err != nil {
		return err
	}
	if srv.IsKubernetes() {
		return badRequest("a Kubernetes cluster routes domains with its own ingress controller")
	}
	switch r.PathValue("action") {
	case "start":
		err = proxy.Ensure(r.Context(), x, s.engine.ProxyOptions(r.Context()), false)
	case "restart":
		err = proxy.Ensure(r.Context(), x, s.engine.ProxyOptions(r.Context()), true)
	case "stop":
		err = proxy.Stop(r.Context(), x)
	default:
		return badRequest("unknown action")
	}
	if err != nil {
		return badRequest("%v", err)
	}
	status := proxy.Status(r.Context(), x)
	_, _ = s.db.ExecContext(r.Context(), "UPDATE servers SET proxy_status = ? WHERE id = ?", status, srv.ID)
	s.hub.Event("server", srv.ID, srv.Status)
	return ok(w, map[string]string{"proxy_status": status})
}
