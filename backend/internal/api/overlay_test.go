package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/portdock/portdock/internal/overlay"
	"github.com/portdock/portdock/internal/store"
)

func TestOverlaySettings(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("dev@corp.com", "correct-horse", "member")
	for _, q := range []string{
		"INSERT INTO servers(id, name, host, status, is_local) VALUES (1, 'local', 'localhost', 'unreachable', 1)",
		"INSERT INTO servers(id, name, host, status) VALUES (2, 'eu-1', '203.0.113.10', 'unreachable')",
		"INSERT INTO servers(id, name, host, status) VALUES (3, 'eu-2', '203.0.113.11', 'unreachable')",
	} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	owner, dev := e.login("owner@corp.com"), e.login("dev@corp.com")
	e.expect(dev, "GET", "/api/overlay", nil, 403)
	got := e.expect(owner, "GET", "/api/overlay", nil, 200)
	if got["enabled"] != false || got["subnet"] != overlay.DefaultSubnet || got["port"] != float64(overlay.DefaultPort) || len(got["servers"].([]any)) != 3 {
		t.Fatalf("defaults: %v", got)
	}

	servers := func(join ...int64) []map[string]any {
		var out []map[string]any
		for _, id := range []int64{1, 2, 3} {
			j := false
			for _, x := range join {
				j = j || x == id
			}
			ep := ""
			if id == 1 {
				ep = "vpn.example.com"
			}
			out = append(out, map[string]any{"id": id, "join": j, "endpoint": ep})
		}
		return out
	}
	e.expect(owner, "PUT", "/api/overlay", map[string]any{"enabled": true, "subnet": "8.8.8.0/24", "servers": servers(2, 3)}, 400)
	e.expect(owner, "PUT", "/api/overlay", map[string]any{"enabled": true, "servers": servers(2)}, 400)
	got = e.expect(owner, "PUT", "/api/overlay", map[string]any{"enabled": true, "subnet": "10.77.0.0/24", "port": 51821, "servers": servers(1, 2)}, 200)
	list := got["servers"].([]any)
	s1, s2, s3 := list[0].(map[string]any), list[1].(map[string]any), list[2].(map[string]any)
	if s1["ip"] != "10.77.0.1" || s2["ip"] != "10.77.0.2" || s3["ip"] != "" || len(s1["public_key"].(string)) != 44 ||
		s1["effective_endpoint"] != "vpn.example.com:51821" || s2["effective_endpoint"] != "203.0.113.10:51821" {
		t.Errorf("servers: %v", list)
	}
	var enc string
	_ = e.db.QueryRow("SELECT overlay_private_key FROM servers WHERE id = 2").Scan(&enc)
	priv, err := e.api.box.Decrypt(enc)
	if err != nil {
		t.Fatal(err)
	}
	if pub, _ := overlay.PublicKey(priv); pub != s2["public_key"] {
		t.Error("the stored private key does not match the public key")
	}
	if strings.Contains(fmt.Sprint(got), priv) {
		t.Error("a private key is returned")
	}

	// Server edits keep the network's fields.
	key := e.expect(owner, "POST", "/api/ssh-keys", map[string]any{"name": "k"}, 200)["id"]
	e.expect(owner, "PATCH", "/api/servers/2", map[string]any{"name": "eu-1", "host": "203.0.113.10", "port": 22, "user": "root", "ssh_key_id": key, "overlay_ip": "10.77.0.99", "overlay_join": false}, 200)
	srv, _ := store.ByID[store.Server](t.Context(), e.db, "servers", 2)
	if srv.OverlayIP != "10.77.0.2" || !srv.OverlayJoin {
		t.Errorf("server edit changed the network: %+v", srv)
	}

	// A new subnet gives new addresses; keys stay.
	got = e.expect(owner, "PUT", "/api/overlay", map[string]any{"enabled": true, "subnet": "10.99.0.0/28", "servers": servers(1, 2, 3)}, 200)
	list = got["servers"].([]any)
	if list[0].(map[string]any)["ip"] != "10.99.0.1" || list[2].(map[string]any)["ip"] != "10.99.0.3" || list[0].(map[string]any)["public_key"] != s1["public_key"] {
		t.Errorf("new subnet: %v", list)
	}

	// A database's private port needs its server in the network.
	p := e.expect(owner, "POST", "/api/projects", map[string]any{"name": "shop"}, 200)
	env := p["environments"].([]any)[0].(map[string]any)["id"]
	db := e.expect(owner, "POST", "/api/databases", map[string]any{"name": "pg", "engine": "postgres", "environment_id": env, "server_id": 2, "start": false}, 200)
	id := int64(db["id"].(float64))
	got = e.expect(owner, "PATCH", fmt.Sprintf("/api/databases/%d", id), map[string]any{"overlay_port": 15432}, 200)
	if got["private_address"] != "10.99.0.2:15432" || !strings.Contains(got["private_url"].(string), "@10.99.0.2:15432/") {
		t.Errorf("private URL: %v %v", got["private_address"], got["private_url"])
	}
	e.expect(owner, "PATCH", fmt.Sprintf("/api/databases/%d", id), map[string]any{"overlay_port": 70000}, 400)
}
