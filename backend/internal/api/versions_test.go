package api

import (
	"fmt"
	"testing"
	"time"
)

func TestVersionsAPI(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	c := e.login("owner@corp.com")
	p := e.expect(c, "POST", "/api/projects", map[string]any{"name": "shop"}, 200)
	env := p["environments"].([]any)[0].(map[string]any)["id"]
	app := e.expect(c, "POST", "/api/applications", map[string]any{"name": "shop", "environment_id": env, "server_id": 1, "source_type": "git",
		"git_repository": "https://github.com/acme/shop.git", "build_pack": "compose"}, 200)
	id := int64(app["id"].(float64))
	base := fmt.Sprintf("/api/applications/%d", id)

	// Three builds: v1, v2 (expired) and v3 (live).
	now := time.Now().UTC()
	for n := 1; n <= 3; n++ {
		status := "available"
		if n == 2 {
			status = "expired"
		}
		created := now.Add(-time.Duration(4-n) * time.Hour).Format(time.RFC3339)
		if _, err := e.db.Exec(`INSERT INTO deployments(id, uuid, application_id, status, trigger, image, commit_sha, version_id, created_at)
			VALUES (?, ?, ?, 'success', 'webhook', ?, ?, ?, ?)`, n, fmt.Sprintf("d%d", n), id, fmt.Sprintf("compose@v%d", n), fmt.Sprintf("sha%d", n), n, created); err != nil {
			t.Fatal(err)
		}
		if _, err := e.db.Exec(`INSERT INTO app_versions(id, application_id, number, deployment_id, commit_sha, image, services, compose_file, status, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 'services: {}', ?, ?)`, n, id, n, n, fmt.Sprintf("sha%d", n), fmt.Sprintf("compose@v%d", n),
			fmt.Sprintf(`{"frontend":"portdock/x-frontend:v%d"}`, n), status, created); err != nil {
			t.Fatal(err)
		}
	}

	res := e.expect(c, "GET", base+"/versions", nil, 200)
	vs := res["versions"].([]any)
	if len(vs) != 2 || res["live_version_id"].(float64) != 3 || res["kept"].(float64) != 5 || res["max_age_days"].(float64) != 15 {
		t.Fatalf("versions: %v", res)
	}
	v3, v1 := vs[0].(map[string]any), vs[1].(map[string]any)
	if v3["tag"] != "v3" || v3["live"] != true || v3["expires_at"] != nil {
		t.Errorf("live version: %v", v3)
	}
	if v1["tag"] != "v1" || v1["live"] != false || v1["expires_at"] == nil || v1["services"].(map[string]any)["frontend"] != "portdock/x-frontend:v1" {
		t.Errorf("previous version: %v", v1)
	}

	// Going back to v1 queues a deployment of exactly that version.
	d := e.expect(c, "POST", base+"/versions/1/deploy", nil, 200)
	if d["trigger"] != "promote" || d["version_id"].(float64) != 1 || d["image"] != "compose@v1" || d["commit_sha"] != "sha1" || d["rollback_of"].(float64) != 1 {
		t.Fatalf("promotion: %v", d)
	}
	if res := e.expect(c, "GET", base+"/versions", nil, 200); res["pending"] == nil {
		t.Error("the queued promotion must show as pending")
	}
	e.expect(c, "POST", base+"/versions/2/deploy", nil, 400)  // expired
	e.expect(c, "POST", base+"/versions/99/deploy", nil, 400) // unknown
}
