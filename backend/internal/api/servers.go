package api

import (
	"context"
	"github.com/portdock/portdock/internal/auth"
	"net/http"
	"strings"

	"github.com/portdock/portdock/internal/deploy"
	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
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

func (s *Server) listServers(w http.ResponseWriter, r *http.Request) error {
	srvs, err := store.Select[store.Server](r.Context(), s.db, "SELECT * FROM servers ORDER BY is_local DESC, id")
	if err != nil {
		return err
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
	return ok(w, srv)
}

func validateServer(srv *store.Server) error {
	srv.Name = strings.TrimSpace(srv.Name)
	srv.Host = strings.TrimSpace(srv.Host)
	if srv.Name == "" {
		return badRequest("name is required")
	}
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
	if err := readJSON(r, &srv); err != nil {
		return err
	}
	srv.ID, srv.Status, srv.ProxyStatus, srv.CreatedAt = 0, "unknown", "unknown", store.Now()
	srv.IsLocal = false
	if err := validateServer(&srv); err != nil {
		return err
	}
	if err := s.checkServerLimit(r.Context()); err != nil {
		return err
	}
	if _, err := store.Insert(r.Context(), s.db, "servers", &srv); err != nil {
		return err
	}
	s.activity(r, "server.create", srv.Name)
	srv, _ = s.engine.ValidateServer(r.Context(), srv)
	return ok(w, srv)
}

func (s *Server) updateServer(w http.ResponseWriter, r *http.Request) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	id, local, created := srv.ID, srv.IsLocal, srv.CreatedAt
	if err := readJSON(r, &srv); err != nil {
		return err
	}
	srv.ID, srv.IsLocal, srv.CreatedAt = id, local, created
	if err := validateServer(&srv); err != nil {
		return err
	}
	if err := store.Update(r.Context(), s.db, "servers", &srv); err != nil {
		return err
	}
	s.pool.Forget(srv.ID)
	s.activity(r, "server.update", srv.Name)
	return ok(w, srv)
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
	s.activity(r, "server.delete", srv.Name)
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) validateServer(w http.ResponseWriter, r *http.Request) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	srv, _ = s.engine.ValidateServer(r.Context(), srv)
	return ok(w, srv)
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
	return ok(w, stats)
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
	if !auth.UserFrom(r.Context()).IsAdmin() {
		// Members only see containers of resources they can access.
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
		cs = filterBy(cs, func(c docker.Container) bool {
			env, ok := uuids[c.Labels[docker.LabelResource]]
			return ok && visible(env)
		})
	}
	return ok(w, cs)
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
