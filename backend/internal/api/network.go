package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/deploy"
	"github.com/portdock/portdock/internal/netcfg"
)

// Corporate network: proxy and mirrors (Settings → Network), and the Docker
// daemon of each server (Servers → server → Settings).

// validateNetworkSettings checks the network settings of an update merged with the stored ones.
func (s *Server) validateNetworkSettings(ctx context.Context, in map[string]string) error {
	touched := false
	for _, k := range netcfg.SettingKeys {
		if _, ok := in[k]; ok {
			touched = true
		}
	}
	if !touched {
		return nil
	}
	if v, ok := in["build_env"]; ok {
		if _, err := netcfg.ParseEnvLines(v); err != nil {
			return badRequest("build variables: %v", err)
		}
	}
	cfg := netcfg.FromSettings(func(k string) string {
		if v, ok := in[k]; ok {
			return strings.TrimSpace(v)
		}
		return s.db.Setting(ctx, k)
	})
	if err := cfg.Validate(); err != nil {
		return badRequest("%v", err)
	}
	return nil
}

// testNetwork requests a URL through the proxy settings being edited (not saved yet).
func (s *Server) testNetwork(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Settings map[string]string `json:"settings"`
		URL      string            `json:"url"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	cfg := netcfg.FromSettings(func(k string) string {
		if v, ok := in.Settings[k]; ok && !(k == "proxy_password" && v == "") {
			return strings.TrimSpace(v)
		}
		v := s.db.Setting(r.Context(), k)
		if k == "proxy_password" && v != "" {
			return s.box.MustDecrypt(v)
		}
		return v
	})
	if err := cfg.Validate(); err != nil {
		return badRequest("%v", err)
	}
	target := strings.TrimSpace(in.URL)
	if target == "" {
		target = "https://registry-1.docker.io/v2/"
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
		return badRequest("enter an http:// or https:// URL")
	}
	proxyURL, _ := cfg.ProxyFor(req.URL)
	via := "direct"
	if proxyURL != nil {
		via = proxyURL.Host
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}}
	start := time.Now()
	res, err := client.Do(req)
	out := map[string]any{"via": via, "ms": time.Since(start).Milliseconds()}
	if err != nil {
		out["ok"], out["error"] = false, err.Error()
		return ok(w, out)
	}
	res.Body.Close()
	// Any HTTP answer proves the route works (the registry answers 401 without credentials).
	out["ok"], out["status"] = res.StatusCode != http.StatusProxyAuthRequired, res.StatusCode
	if res.StatusCode == http.StatusProxyAuthRequired {
		out["error"] = "the proxy asks for a user name and password (HTTP 407)"
	}
	return ok(w, out)
}

func (s *Server) serverDockerNetwork(w http.ResponseWriter, r *http.Request) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	st, err := s.engine.DaemonStatus(r.Context(), srv)
	if err != nil {
		return badRequest("%v", err)
	}
	return ok(w, st)
}

func (s *Server) applyServerDockerNetwork(w http.ResponseWriter, r *http.Request) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	var in struct {
		Mode string `json:"mode"` // write | reload | restart
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.Mode == "" {
		in.Mode = deploy.DaemonReload
	}
	if in.Mode != deploy.DaemonWrite && in.Mode != deploy.DaemonReload && in.Mode != deploy.DaemonRestart {
		return badRequest("mode must be write, reload or restart")
	}
	done, err := s.engine.ApplyDaemonNetwork(r.Context(), srv, in.Mode)
	s.activity(r, "server.docker_network", fmt.Sprintf("%s: %s", srv.Name, done))
	if errors.Is(err, deploy.ErrNoSystemd) {
		return ok(w, map[string]any{"done": done, "warning": err.Error()})
	}
	if err != nil {
		return badRequest("%v", err)
	}
	return ok(w, map[string]any{"done": done})
}
