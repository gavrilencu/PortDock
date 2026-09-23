package api

import (
	"fmt"
	"testing"
)

func TestComposeDomainsPerService(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	c := e.login("owner@corp.com")
	p := e.expect(c, "POST", "/api/projects", map[string]any{"name": "shop"}, 200)
	env := p["environments"].([]any)[0].(map[string]any)["id"]

	app := e.expect(c, "POST", "/api/applications", map[string]any{
		"name": "shop", "environment_id": env, "server_id": 1, "source_type": "git",
		"git_repository": "https://github.com/acme/shop.git", "build_pack": "compose",
		"domains": "",
		"domain_routes": []map[string]any{
			{"domain": "shop.example.com", "service": "frontend", "port": 80},
			{"domain": "api.shop.example.com", "service": "backend"},
		},
	}, 200)
	id := int64(app["id"].(float64))
	ds := e.expect(c, "GET", fmt.Sprintf("/api/applications/%d/domains", id), nil, 200)["domains"].([]any)
	got := map[string]string{}
	for _, d := range ds {
		m := d.(map[string]any)
		got[m["host"].(string)] = fmt.Sprintf("%v:%v", m["service"], m["port"])
	}
	if got["shop.example.com"] != "frontend:80" || got["api.shop.example.com"] != "backend:0" || len(got) != 2 {
		t.Fatalf("domains: %v", got)
	}

	// A domain can be moved to another service later.
	first := ds[0].(map[string]any)
	e.expect(c, "PATCH", fmt.Sprintf("/api/domains/%v", first["id"]), map[string]any{"service": "admin"}, 200)
	e.expect(c, "PATCH", fmt.Sprintf("/api/domains/%v", first["id"]), map[string]any{"service": "bad name!"}, 400)
	e.expect(c, "GET", fmt.Sprintf("/api/applications/%d/compose-services", id), nil, 200)

	// Per-service domains need a compose application.
	e.expect(c, "POST", "/api/applications", map[string]any{
		"name": "web", "environment_id": env, "server_id": 1, "source_type": "image", "docker_image": "nginx",
		"domain_routes": []map[string]any{{"domain": "web.example.com", "service": "web"}},
	}, 400)
}
