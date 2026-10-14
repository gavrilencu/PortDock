package api

import (
	"net/http"
	"strings"

	"github.com/portdock/portdock/internal/deploy"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/store"
)

// Git over SSH: the deploy key of an application and the known host keys of
// Git servers (deploy/gitssh.go).

// setDeployKey stores a private deploy key ("" removes it).
func (s *Server) setDeployKey(a *store.Application, priv string) error {
	priv = strings.TrimSpace(priv)
	if priv == "" {
		a.GitSSHKey, a.GitSSHPublicKey = "", ""
		return nil
	}
	pub, err := executor.PublicKeyFromPrivate(priv)
	if err != nil {
		return badRequest("the deploy key is not a valid unencrypted OpenSSH private key: %v", err)
	}
	a.GitSSHKey, a.GitSSHPublicKey = s.box.Encrypt(priv), pub+" portdock-"+a.UUID
	return nil
}

// createDeployKey generates (or imports, with private_key) the deploy key of
// an application and returns its public key.
func (s *Server) createDeployKey(w http.ResponseWriter, r *http.Request) error {
	a, err := s.loadApp(r)
	if err != nil {
		return err
	}
	var in struct {
		PrivateKey string `json:"private_key"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	priv := in.PrivateKey
	if strings.TrimSpace(priv) == "" {
		if priv, _, err = executor.GenerateKeyPair(); err != nil {
			return err
		}
	}
	if err := s.setDeployKey(&a, priv); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(r.Context(), "UPDATE applications SET git_ssh_key = ?, git_ssh_public_key = ? WHERE id = ?", a.GitSSHKey, a.GitSSHPublicKey, a.ID); err != nil {
		return err
	}
	s.activity(r, "application.deploy_key", a.Name)
	return ok(w, map[string]string{"public_key": a.GitSSHPublicKey})
}

func (s *Server) deleteDeployKey(w http.ResponseWriter, r *http.Request) error {
	a, err := s.loadApp(r)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(r.Context(), "UPDATE applications SET git_ssh_key = '', git_ssh_public_key = '' WHERE id = ?", a.ID); err != nil {
		return err
	}
	s.activity(r, "application.deploy_key_delete", a.Name)
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) listGitHosts(w http.ResponseWriter, r *http.Request) error {
	hs, err := store.Select[deploy.GitKnownHost](r.Context(), s.db, "SELECT * FROM git_known_hosts ORDER BY host")
	if err != nil {
		return err
	}
	if hs == nil {
		hs = []deploy.GitKnownHost{}
	}
	return ok(w, map[string]any{"hosts": hs, "strict": s.db.Setting(r.Context(), "git_ssh_strict") == "true"})
}

// scanGitHost reads the host keys of a Git server (from the local server)
// so an administrator can compare the fingerprints before trusting them.
func (s *Server) scanGitHost(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Host string `json:"host"` // host, host:port, or an SSH repository URL
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	host, port, err := gitHostOf(in.Host)
	if err != nil {
		return err
	}
	srv, err := store.Get[store.Server](r.Context(), s.db, "SELECT * FROM servers ORDER BY is_local DESC, id LIMIT 1")
	if err != nil {
		return err
	}
	x, err := s.pool.For(r.Context(), srv)
	if err != nil {
		return err
	}
	keys, fps, err := s.engine.KeyScan(r.Context(), x, host, port)
	if err != nil {
		return badRequest("%v", err)
	}
	return ok(w, map[string]any{"host": deploy.KnownHostName(host, port), "keys": keys, "fingerprints": fps})
}

// gitHostOf accepts "host", "host:port" or a repository URL.
func gitHostOf(raw string) (string, int, error) {
	raw = strings.TrimSpace(raw)
	if host, port, isSSH, err := deploy.ParseSSHRepo(raw); isSSH {
		if err != nil {
			return "", 0, badRequest("%v", err)
		}
		return host, port, nil
	}
	host, port, isSSH, err := deploy.ParseSSHRepo("ssh://git@" + raw + "/x")
	if !isSSH || err != nil {
		return "", 0, badRequest("enter a host name, host:port or an SSH repository URL")
	}
	return host, port, nil
}

func (s *Server) addGitHost(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Host string `json:"host"` // as known_hosts names it: host or [host]:port
		Keys string `json:"keys"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	in.Host = strings.TrimSpace(in.Host)
	fps, err := deploy.ValidKnownHosts(in.Host, in.Keys)
	if err != nil {
		return badRequest("%v", err)
	}
	h := deploy.GitKnownHost{Host: in.Host, Keys: strings.TrimSpace(in.Keys) + "\n", Fingerprints: strings.Join(fps, "\n"), Source: "admin", CreatedAt: store.Now()}
	if _, err := s.db.ExecContext(r.Context(), `INSERT INTO git_known_hosts(host, keys, fingerprints, source, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(host) DO UPDATE SET keys = excluded.keys, fingerprints = excluded.fingerprints, source = excluded.source, created_at = excluded.created_at`,
		h.Host, h.Keys, h.Fingerprints, h.Source, h.CreatedAt); err != nil {
		return err
	}
	s.activity(r, "git_host.trust", h.Host)
	return ok(w, h)
}

func (s *Server) deleteGitHost(w http.ResponseWriter, r *http.Request) error {
	host := r.URL.Query().Get("host")
	res, err := s.db.ExecContext(r.Context(), "DELETE FROM git_known_hosts WHERE host = ?", host)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errStatus(404, "not found")
	}
	s.activity(r, "git_host.delete", host)
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) setGitStrict(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Strict bool `json:"strict"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	v := ""
	if in.Strict {
		v = "true"
	}
	if err := s.db.SetSetting(r.Context(), "git_ssh_strict", v); err != nil {
		return err
	}
	s.activity(r, "git_host.strict", v)
	return ok(w, map[string]bool{"strict": in.Strict})
}
