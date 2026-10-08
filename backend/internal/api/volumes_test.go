package api

import (
	"fmt"
	"testing"
)

// Persistent volumes: members manage Docker volumes, only administrators mount
// server directories, and PortDock's own data can never be mounted.
func TestAppVolumes(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("dev@corp.com", "correct-horse", "member")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	owner := e.login("owner@corp.com")
	dev := e.login("dev@corp.com")
	p := e.expect(dev, "POST", "/api/projects", map[string]any{"name": "shop"}, 200)
	env := p["environments"].([]any)[0].(map[string]any)["id"]
	app := e.expect(dev, "POST", "/api/applications", map[string]any{
		"name": "web", "environment_id": env, "server_id": 1, "source_type": "image", "docker_image": "nginx",
	}, 200)
	base := fmt.Sprintf("/api/applications/%v/volumes", app["id"])

	for _, bad := range []map[string]any{
		{"name": "Data!", "mount_path": "/data"},
		{"name": "data", "mount_path": "data"},
		{"name": "data", "mount_path": "/"},
		{"name": "data", "mount_path": "/a:b"},
		{"name": "data", "mount_path": "/data", "host_path": "/srv/media,ro"},
	} {
		e.expect(owner, "POST", base, bad, 400)
	}
	res := e.expect(dev, "POST", base, map[string]any{"name": " Data ", "mount_path": "/var/lib/app/"}, 200)
	vols := res["volumes"].([]any)
	if len(vols) != 1 || vols[0].(map[string]any)["name"] != "data" || vols[0].(map[string]any)["mount_path"] != "/var/lib/app" {
		t.Fatalf("normalized volume: %v", vols)
	}
	vid := vols[0].(map[string]any)["id"]
	e.expect(dev, "POST", base, map[string]any{"name": "data", "mount_path": "/other"}, 409)
	e.expect(dev, "POST", base, map[string]any{"name": "other", "mount_path": "/var/lib/app"}, 409)

	// Server directories: administrators only, never system or PortDock paths.
	e.expect(dev, "POST", base, map[string]any{"name": "media", "mount_path": "/media", "host_path": "/srv/media"}, 403)
	e.expect(owner, "POST", base, map[string]any{"name": "sock", "mount_path": "/s", "host_path": "/var/run/docker.sock"}, 400)
	e.expect(owner, "POST", base, map[string]any{"name": "pd", "mount_path": "/pd", "host_path": e.dir}, 400)
	e.expect(owner, "POST", base, map[string]any{"name": "pd", "mount_path": "/pd", "host_path": e.dir + "/apps"}, 400)
	res = e.expect(owner, "POST", base, map[string]any{"name": "media", "mount_path": "/media", "host_path": "/srv/media", "read_only": true}, 200)
	var media map[string]any
	for _, v := range res["volumes"].([]any) {
		if m := v.(map[string]any); m["name"] == "media" {
			media = m
		}
	}
	// The member may move the administrator's mount, not point it elsewhere.
	e.expect(dev, "PATCH", fmt.Sprintf("/api/volumes/%v", media["id"]), map[string]any{"mount_path": "/srv"}, 200)
	e.expect(dev, "PATCH", fmt.Sprintf("/api/volumes/%v", media["id"]), map[string]any{"host_path": "/home"}, 403)

	// A Docker volume keeps its data under its name.
	e.expect(dev, "PATCH", fmt.Sprintf("/api/volumes/%v", vid), map[string]any{"name": "renamed"}, 400)
	e.expect(dev, "PATCH", fmt.Sprintf("/api/volumes/%v", vid), map[string]any{"read_only": true}, 200)

	e.expect(dev, "DELETE", fmt.Sprintf("/api/volumes/%v", vid), nil, 200)
	if got := e.expect(dev, "GET", base, nil, 200)["volumes"].([]any); len(got) != 1 {
		t.Fatalf("after delete: %v", got)
	}

	// Compose applications declare volumes in the compose file.
	compose := e.expect(dev, "POST", "/api/applications", map[string]any{
		"name": "stack", "environment_id": env, "server_id": 1, "source_type": "git", "git_repository": "https://example.com/r.git", "build_pack": "compose",
	}, 200)
	e.expect(dev, "POST", fmt.Sprintf("/api/applications/%v/volumes", compose["id"]), map[string]any{"name": "data", "mount_path": "/data"}, 400)
}
