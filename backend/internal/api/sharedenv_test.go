package api

import (
	"fmt"
	"testing"
)

func TestSharedVariablesAndPromotion(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("viewer@corp.com", "correct-horse", "member")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	owner, viewer := e.login("owner@corp.com"), e.login("viewer@corp.com")
	p := e.expect(owner, "POST", "/api/projects", map[string]any{"name": "shop", "default_role": "viewer"}, 200)
	pid := int64(p["id"].(float64))
	prod := p["environments"].([]any)[0].(map[string]any)["id"]
	staging := e.expect(owner, "POST", fmt.Sprintf("/api/projects/%d/environments", pid), map[string]any{"name": "staging"}, 200)["id"]
	mkApp := func(env any) int64 {
		a := e.expect(owner, "POST", "/api/applications", map[string]any{"name": "web", "environment_id": env, "server_id": 1, "source_type": "image", "docker_image": "nginx"}, 200)
		return int64(a["id"].(float64))
	}
	prodApp, stagingApp := mkApp(prod), mkApp(staging)

	org := e.expect(owner, "POST", "/api/organization/envs", map[string]any{"key": "COMPANY", "value": "acme"}, 200)
	proj := e.expect(owner, "POST", fmt.Sprintf("/api/projects/%d/envs", pid), map[string]any{"key": "LOG_LEVEL", "value": "info"}, 200)
	e.expect(owner, "POST", fmt.Sprintf("/api/environments/%v/envs", staging), map[string]any{"key": "LOG_LEVEL", "value": "debug"}, 200)
	e.expect(owner, "POST", fmt.Sprintf("/api/environments/%v/envs", staging), map[string]any{"key": "API_KEY", "value": "s3cr3t", "is_secret": true}, 200)
	e.expect(owner, "POST", fmt.Sprintf("/api/applications/%d/envs", stagingApp), map[string]any{"key": "COMPANY", "value": "acme-staging"}, 200)
	e.expect(owner, "POST", fmt.Sprintf("/api/applications/%d/envs", stagingApp), map[string]any{"key": "FEATURE_X", "value": "on"}, 200)

	// What a deployment receives: the narrowest scope wins.
	for _, c := range []struct {
		app  int64
		want map[string]string
	}{
		{prodApp, map[string]string{"COMPANY": "acme", "LOG_LEVEL": "info"}},
		{stagingApp, map[string]string{"COMPANY": "acme-staging", "LOG_LEVEL": "debug", "API_KEY": "s3cr3t", "FEATURE_X": "on"}},
	} {
		env, err := e.api.engine.ResolveEnv(t.Context(), "application", c.app, false)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range c.want {
			if env.Runtime[k] != v {
				t.Errorf("app %d: %s = %q, want %q (%v)", c.app, k, env.Runtime[k], v, env.Runtime)
			}
		}
		if c.app == stagingApp && (len(env.Secrets) == 0 || env.Secrets[0] != "s3cr3t") {
			t.Errorf("the shared secret is not masked in logs: %v", env.Secrets)
		}
	}

	inh := e.expectList(owner, "GET", fmt.Sprintf("/api/applications/%d/envs/inherited", stagingApp))
	got := map[string]map[string]any{}
	for _, v := range inh {
		got[v["key"].(string)+"@"+v["scope"].(string)] = v
	}
	if got["COMPANY@organization"]["overridden"] != true || got["LOG_LEVEL@project"]["overridden"] != true || got["LOG_LEVEL@environment"]["overridden"] != false {
		t.Errorf("inherited: %v", inh)
	}
	if got["API_KEY@environment"]["value"] != "" {
		t.Error("a shared secret's value is shown")
	}

	// Permissions: viewers cannot read or change shared variables; only
	// administrators manage the organization's.
	e.expect(viewer, "GET", fmt.Sprintf("/api/projects/%d/envs", pid), nil, 403)
	e.expect(viewer, "GET", "/api/organization/envs", nil, 403)
	e.expect(viewer, "PATCH", fmt.Sprintf("/api/envs/%v", proj["id"]), map[string]any{"value": "x"}, 403)
	e.expect(viewer, "DELETE", fmt.Sprintf("/api/envs/%v", org["id"]), nil, 404) // not visible
	e.expect(owner, "PATCH", fmt.Sprintf("/api/envs/%v", proj["id"]), map[string]any{"value": "warn"}, 200)

	// Compare staging with production, then promote.
	cmp := e.expect(owner, "GET", fmt.Sprintf("/api/applications/%d/envs/compare?with=%d", stagingApp, prodApp), nil, 200)
	status := map[string]string{}
	for _, v := range cmp["variables"].([]any) {
		m := v.(map[string]any)
		status[m["key"].(string)] = m["status"].(string)
	}
	// Effective values: the application's own COMPANY against the organization's,
	// the environment's LOG_LEVEL against the project's.
	if status["COMPANY"] != "different" || status["LOG_LEVEL"] != "different" || status["FEATURE_X"] != "only_here" || status["API_KEY"] != "only_here" {
		t.Errorf("compare: %v", cmp)
	}
	e.expect(owner, "POST", fmt.Sprintf("/api/applications/%d/envs", prodApp), map[string]any{"key": "FEATURE_X", "value": "off"}, 200)
	cmp = e.expect(owner, "GET", fmt.Sprintf("/api/applications/%d/envs/compare?with=%d", stagingApp, prodApp), nil, 200)
	for _, v := range cmp["variables"].([]any) {
		if m := v.(map[string]any); m["key"] == "FEATURE_X" && (m["status"] != "different" || m["here"] != "on" || m["there"] != "off") {
			t.Errorf("different: %v", m)
		}
	}
	res := e.expect(owner, "POST", fmt.Sprintf("/api/applications/%d/envs/promote", stagingApp), map[string]any{"to": fmt.Sprint(prodApp), "keys": []string{"FEATURE_X"}}, 200)
	if res["copied"] != float64(1) {
		t.Errorf("promote: %v", res)
	}
	env, _ := e.api.engine.ResolveEnv(t.Context(), "application", prodApp, false)
	if env.Runtime["FEATURE_X"] != "on" || env.Runtime["LOG_LEVEL"] != "warn" {
		t.Errorf("production after the promotion: %v", env.Runtime)
	}
	e.expect(owner, "POST", fmt.Sprintf("/api/applications/%d/envs/promote", stagingApp), map[string]any{"to": fmt.Sprint(prodApp), "keys": []string{"NOPE"}}, 400)
	e.expect(viewer, "GET", fmt.Sprintf("/api/applications/%d/envs/compare?with=%d", stagingApp, prodApp), nil, 403)

	// Deleting the environment removes its shared variables.
	_, _ = e.db.Exec("DELETE FROM applications WHERE id = ?", stagingApp) // no server to clean up in tests
	e.expect(owner, "DELETE", fmt.Sprintf("/api/environments/%v", staging), nil, 200)
	var n int
	_ = e.db.QueryRow("SELECT COUNT(*) FROM env_vars WHERE owner_type = 'environment'").Scan(&n)
	if n != 0 {
		t.Errorf("%d shared variables of a deleted environment left", n)
	}
}
