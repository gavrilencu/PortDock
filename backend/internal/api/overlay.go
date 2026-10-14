package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/overlay"
	"github.com/portdock/portdock/internal/store"
)

// The private network between servers (Settings → Network & proxy):
// WireGuard between the servers that join it, for databases reachable from
// other servers and load balancing across servers. Administrators only.

type overlayServer struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Host      string `json:"host"`
	IsLocal   bool   `json:"is_local"`
	Join      bool   `json:"join"`
	IP        string `json:"ip"`
	PublicKey string `json:"public_key"`
	// Endpoint overrides how the others reach it (host or host:port); Effective is the result.
	Endpoint  string `json:"endpoint"`
	Effective string `json:"effective_endpoint"`
}

func (s *Server) getOverlay(w http.ResponseWriter, r *http.Request) error {
	set, err := s.overlay.Settings(r.Context())
	if err != nil {
		// Saved settings are valid; this only guards hand-edited ones.
		set = overlay.Settings{Port: overlay.DefaultPort}
	}
	servers, err := store.Select[store.Server](r.Context(), s.db, "SELECT * FROM servers ORDER BY id")
	if err != nil {
		return err
	}
	out := make([]overlayServer, 0, len(servers))
	for _, srv := range servers {
		out = append(out, overlayServer{ID: srv.ID, Name: srv.Name, Host: srv.Host, IsLocal: srv.IsLocal, Join: srv.OverlayJoin, IP: srv.OverlayIP,
			PublicKey: srv.OverlayPublicKey, Endpoint: srv.OverlayEndpoint, Effective: overlay.Endpoint(srv.Host, srv.OverlayEndpoint, set.Port)})
	}
	subnet := overlay.DefaultSubnet
	if set.Subnet.IsValid() {
		subnet = set.Subnet.String()
	}
	return ok(w, map[string]any{"enabled": set.Enabled, "subnet": subnet, "port": set.Port, "servers": out})
}

func (s *Server) putOverlay(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Enabled bool   `json:"enabled"`
		Subnet  string `json:"subnet"`
		Port    int    `json:"port"`
		Servers []struct {
			ID       int64  `json:"id"`
			Join     bool   `json:"join"`
			Endpoint string `json:"endpoint"`
		} `json:"servers"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	values := map[string]string{"overlay_enabled": strconv.FormatBool(in.Enabled), "overlay_subnet": strings.TrimSpace(in.Subnet), "overlay_port": strconv.Itoa(in.Port)}
	if in.Port == 0 {
		values["overlay_port"] = ""
	}
	set, err := overlay.ParseSettings(func(k string) string { return values[k] })
	if err != nil {
		return badRequest("%v", err)
	}
	joined := 0
	for _, sv := range in.Servers {
		if strings.ContainsAny(strings.TrimSpace(sv.Endpoint), ` /\@`) {
			return badRequest("the address of server %d must be a host or host:port", sv.ID)
		}
		if _, err := store.ByID[store.Server](r.Context(), s.db, "servers", sv.ID); err != nil {
			return badRequest("server %d does not exist", sv.ID)
		}
		if sv.Join {
			joined++
		}
	}
	if set.Enabled && joined < 2 {
		return badRequest("choose at least two servers for the private network")
	}
	for _, sv := range in.Servers {
		if _, err := s.db.ExecContext(r.Context(), "UPDATE servers SET overlay_join = ?, overlay_endpoint = ? WHERE id = ?", sv.Join, strings.TrimSpace(sv.Endpoint), sv.ID); err != nil {
			return err
		}
	}
	for k, v := range values {
		if err := s.db.SetSetting(r.Context(), k, v); err != nil {
			return err
		}
	}
	// Addresses and keys now, so the page shows them at once.
	if set.Enabled {
		if _, err := s.overlay.Prepare(r.Context(), set); err != nil {
			return badRequest("%v", err)
		}
	}
	s.activity(r, "overlay.update", map[bool]string{true: "enabled", false: "disabled"}[set.Enabled]+" "+set.Subnet.String())
	s.overlay.Trigger()
	return s.getOverlay(w, r)
}

func (s *Server) overlayStatus(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	st, err := s.overlay.Status(ctx)
	if err != nil {
		return err
	}
	return ok(w, st)
}
