package api

import (
	"fmt"
	"testing"

	"github.com/portdock/portdock/internal/store"
)

func TestImageScansAPI(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	c := e.login("owner@corp.com")
	p := e.expect(c, "POST", "/api/projects", map[string]any{"name": "shop"}, 200)
	env := p["environments"].([]any)[0].(map[string]any)["id"]
	mk := func(name string) map[string]any {
		return e.expect(c, "POST", "/api/applications", map[string]any{
			"name": name, "environment_id": env, "server_id": 1, "source_type": "image", "docker_image": "nginx",
		}, 200)
	}
	app, other := mk("web"), mk("api")
	id := int64(app["id"].(float64))
	base := fmt.Sprintf("/api/applications/%d", id)

	// Settings: the blocking policy is validated.
	app["scan_on_deploy"], app["scan_block"], app["scan_ignore_unfixed"] = true, "high", true
	got := e.expect(c, "PATCH", base, app, 200)
	if got["scan_on_deploy"] != true || got["scan_block"] != "high" || got["scan_ignore_unfixed"] != true {
		t.Fatalf("settings: %v", got)
	}
	app["scan_block"] = "medium"
	e.expect(c, "PATCH", base, app, 400)

	// Nothing runs yet: a manual scan needs a running image.
	e.expect(c, "POST", base+"/scan", nil, 400)

	sc := store.ImageScan{ApplicationID: id, Image: "nginx:1.25", Trigger: "deploy", Status: "success", Critical: 1, High: 2,
		Findings: `[{"id":"CVE-1","severity":"CRITICAL","package":"openssl","installed":"3.0","fixed":"3.1"}]`, CreatedAt: store.Now()}
	if _, err := store.Insert(t.Context(), e.db, "image_scans", &sc); err != nil {
		t.Fatal(err)
	}
	list := e.expect(c, "GET", base+"/scans", nil, 200)
	scans := list["scans"].([]any)
	if len(scans) != 1 || list["running"] != false || list["scanner"] == "" || scans[0].(map[string]any)["findings"] != nil {
		t.Fatalf("list: %v", list)
	}
	one := e.expect(c, "GET", fmt.Sprintf("%s/scans/%d", base, sc.ID), nil, 200)
	fs := one["findings"].([]any)
	if len(fs) != 1 || fs[0].(map[string]any)["id"] != "CVE-1" {
		t.Fatalf("findings: %v", one)
	}
	// A scan is only visible through its application.
	e.expect(c, "GET", fmt.Sprintf("/api/applications/%v/scans/%d", other["id"], sc.ID), nil, 404)

	// One scan at a time.
	_, _ = e.db.Exec("UPDATE applications SET current_image = 'nginx:1.25' WHERE id = ?", id)
	_, _ = e.db.Exec("UPDATE image_scans SET status = 'running' WHERE id = ?", sc.ID)
	e.expect(c, "POST", base+"/scan", nil, 409)
}
