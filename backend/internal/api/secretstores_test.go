package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecretStores(t *testing.T) {
	reads := 0
	vault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "hvs.portdock" {
			w.WriteHeader(403)
			w.Write([]byte(`{"errors":["permission denied"]}`))
			return
		}
		switch r.URL.Path {
		case "/v1/auth/token/lookup-self":
			w.Write([]byte(`{"data":{}}`))
		case "/v1/secret/data/shop/db":
			reads++
			w.Write([]byte(`{"data":{"data":{"password":"vault-pa55","user":"shop"}}}`))
		case "/v1/secret/data/shop/api":
			reads++
			w.Write([]byte(`{"data":{"data":{"token":"tok-123"}}}`))
		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"errors":[]}`))
		}
	}))
	defer vault.Close()

	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("dev@corp.com", "correct-horse", "member")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status, is_local) VALUES (1, 'local', 'localhost', 'reachable', 1)"); err != nil {
		t.Fatal(err)
	}
	owner, dev := e.login("owner@corp.com"), e.login("dev@corp.com")
	shop := e.expect(owner, "POST", "/api/projects", map[string]any{"name": "shop", "default_role": "developer"}, 200)
	shopID := int64(shop["id"].(float64))
	prodEnv := shop["environments"].([]any)[0].(map[string]any)["id"]
	stagingEnv := e.expect(owner, "POST", fmt.Sprintf("/api/projects/%d/environments", shopID), map[string]any{"name": "staging"}, 200)["id"]
	other := e.expect(owner, "POST", "/api/projects", map[string]any{"name": "other", "default_role": "developer"}, 200)
	otherEnv := other["environments"].([]any)[0].(map[string]any)["id"]
	mkApp := func(env any) int64 {
		return int64(e.expect(owner, "POST", "/api/applications", map[string]any{"name": "web", "environment_id": env, "server_id": 1, "source_type": "image", "docker_image": "nginx"}, 200)["id"].(float64))
	}
	app, stagingApp, otherApp := mkApp(prodEnv), mkApp(stagingEnv), mkApp(otherEnv)

	// Administrators manage the stores; the token is never returned.
	e.expect(dev, "POST", "/api/secret-stores", map[string]any{"name": "x", "type": "vault"}, 403)
	e.expect(owner, "POST", "/api/secret-stores", map[string]any{"name": "Vault Prod", "type": "vault", "url": vault.URL, "token": "t"}, 400)
	e.expect(owner, "POST", "/api/secret-stores", map[string]any{"name": "vault-prod", "type": "vault", "url": vault.URL}, 400)
	if res := e.expect(owner, "POST", "/api/secret-stores/test", map[string]any{"type": "vault", "url": vault.URL, "token": "wrong"}, 200); res["ok"] != false || !strings.Contains(res["error"].(string), "permission denied") {
		t.Errorf("test with a wrong token: %v", res)
	}
	if res := e.expect(owner, "POST", "/api/secret-stores/test", map[string]any{"type": "vault", "url": vault.URL, "token": "hvs.portdock", "test_path": "shop/db"}, 200); res["ok"] != true || fmt.Sprint(res["keys"]) != "[password user]" {
		t.Errorf("test: %v", res)
	}
	st := e.expect(owner, "POST", "/api/secret-stores", map[string]any{"name": "vault-prod", "type": "vault", "url": vault.URL + "/", "token": "hvs.portdock",
		"path_prefix": "shop/", "project_ids": []int64{shopID}}, 200)
	storeID := int64(st["id"].(float64))
	if st["token_set"] != true || st["mount"] != "secret" || st["kv_version"] != float64(2) || st["path_prefix"] != "shop" || st["url"] != vault.URL {
		t.Errorf("store: %v", st)
	}
	code, body, _ := e.raw(owner, "/api/secret-stores")
	if code != 200 || strings.Contains(body, "hvs.portdock") {
		t.Errorf("the token leaks: %d %s", code, body)
	}
	if names := e.expectList(dev, "GET", "/api/secret-stores/names"); len(names) != 1 || names[0]["name"] != "vault-prod" || names[0]["type"] != "vault" {
		t.Errorf("names: %v", names)
	}
	// Saving without the token keeps it.
	e.expect(owner, "PATCH", fmt.Sprintf("/api/secret-stores/%d", storeID), map[string]any{"token": ""}, 200)
	if res := e.expect(owner, "POST", "/api/secret-stores/test", map[string]any{"id": storeID}, 200); res["ok"] != true {
		t.Errorf("test of the saved store: %v", res)
	}

	// A developer of the project references a secret; nothing is stored.
	ref := func(c *http.Client, path string, body map[string]any, want int) map[string]any {
		return e.expect(c, "POST", path, body, want)
	}
	v := ref(dev, fmt.Sprintf("/api/applications/%d/envs", app), map[string]any{"key": "DB_PASSWORD", "value": "ignored", "ref_store_id": storeID, "ref_path": "/shop/db/", "ref_key": "password"}, 200)
	if v["value"] != "" || v["is_secret"] != true || v["ref_path"] != "shop/db" {
		t.Errorf("reference: %v", v)
	}
	var stored string
	_ = e.db.QueryRow("SELECT value FROM env_vars WHERE key = 'DB_PASSWORD'").Scan(&stored)
	if plain, _ := e.api.box.Decrypt(stored); plain != "" {
		t.Errorf("a value is stored: %q", plain)
	}
	ref(dev, fmt.Sprintf("/api/applications/%d/envs", app), map[string]any{"key": "X", "ref_store_id": storeID, "ref_path": "billing/db", "ref_key": "k"}, 400)   // outside the prefix
	ref(dev, fmt.Sprintf("/api/applications/%d/envs", app), map[string]any{"key": "X", "ref_store_id": storeID, "ref_path": "shop/db"}, 400)                      // no key
	ref(dev, fmt.Sprintf("/api/applications/%d/envs", otherApp), map[string]any{"key": "X", "ref_store_id": storeID, "ref_path": "shop/db", "ref_key": "k"}, 400) // another project
	ref(owner, "/api/organization/envs", map[string]any{"key": "X", "ref_store_id": storeID, "ref_path": "shop/db", "ref_key": "k"}, 400)                         // limited store
	ref(dev, fmt.Sprintf("/api/applications/%d/envs", app), map[string]any{"key": "X", "ref_store_id": 999, "ref_path": "shop/db", "ref_key": "k"}, 400)

	// The editor checks a reference and lists the keys, never the values.
	chk := e.expect(dev, "POST", fmt.Sprintf("/api/applications/%d/envs/check-ref", app), map[string]any{"ref_store_id": storeID, "ref_path": "shop/db", "ref_key": "pasword"}, 200)
	if chk["ok"] != false || !strings.Contains(chk["error"].(string), "no key pasword") || fmt.Sprint(chk["keys"]) != "[password user]" || strings.Contains(fmt.Sprint(chk), "vault-pa55") {
		t.Errorf("check: %v", chk)
	}
	if chk := e.expect(dev, "POST", fmt.Sprintf("/api/environments/%v/envs/check-ref", stagingEnv), map[string]any{"ref_store_id": storeID, "ref_path": "shop/nope", "ref_key": "x"}, 200); chk["ok"] != false || !strings.Contains(chk["error"].(string), "no secret at secret/shop/nope") {
		t.Errorf("check of a missing path: %v", chk)
	}

	// Deployments get the value, masked in logs; each path is read once.
	ref(dev, fmt.Sprintf("/api/applications/%d/envs", app), map[string]any{"key": "DB_USER", "ref_store_id": storeID, "ref_path": "shop/db", "ref_key": "user"}, 200)
	ref(dev, fmt.Sprintf("/api/projects/%d/envs", shopID), map[string]any{"key": "API_TOKEN", "ref_store_id": storeID, "ref_path": "shop/api", "ref_key": "token"}, 200)
	reads = 0
	env, err := e.api.engine.ResolveEnv(t.Context(), "application", app, false)
	if err != nil {
		t.Fatal(err)
	}
	if env.Runtime["DB_PASSWORD"] != "vault-pa55" || env.Runtime["DB_USER"] != "shop" || env.Runtime["API_TOKEN"] != "tok-123" || len(env.Secrets) != 3 || reads != 2 {
		t.Errorf("resolved: %v secrets %v reads %d", env.Runtime, env.Secrets, reads)
	}

	// The developer view keeps references.
	e.expect(dev, "PUT", fmt.Sprintf("/api/applications/%d/envs", app), map[string]any{"content": "LOG_LEVEL=info\nDB_PASSWORD=plain"}, 200)
	vars := e.expectList(dev, "GET", fmt.Sprintf("/api/applications/%d/envs", app))
	byKey := map[string]map[string]any{}
	for _, v := range vars {
		byKey[v["key"].(string)] = v
	}
	if len(vars) != 3 || byKey["DB_PASSWORD"]["ref_store_id"] != float64(storeID) || byKey["LOG_LEVEL"]["value"] != "info" {
		t.Errorf("after bulk: %v", vars)
	}
	dbPass := int64(byKey["DB_PASSWORD"]["id"].(float64))
	e.expect(dev, "PATCH", fmt.Sprintf("/api/envs/%d", dbPass), map[string]any{"value": "x"}, 400)

	// Inherited and compared by reference.
	inh := e.expectList(dev, "GET", fmt.Sprintf("/api/applications/%d/envs/inherited", app))
	if len(inh) != 1 || inh[0]["ref"] != "vault-prod: shop/api#token" || inh[0]["value"] != "" {
		t.Errorf("inherited: %v", inh)
	}
	cmp := e.expect(dev, "GET", fmt.Sprintf("/api/applications/%d/envs/compare?with=%d", app, stagingApp), nil, 200)
	for _, d := range cmp["variables"].([]any) {
		m := d.(map[string]any)
		if m["key"] == "DB_PASSWORD" && (m["here"] != "↗ vault-prod: shop/db#password" || m["status"] != "only_here") {
			t.Errorf("compare: %v", m)
		}
	}
	e.expect(dev, "POST", fmt.Sprintf("/api/applications/%d/envs/promote", app), map[string]any{"to": fmt.Sprint(stagingApp), "keys": []string{"DB_PASSWORD"}}, 200)
	if env, err := e.api.engine.ResolveEnv(t.Context(), "application", stagingApp, false); err != nil || env.Runtime["DB_PASSWORD"] != "vault-pa55" {
		t.Errorf("promoted: %v %v", env.Runtime, err)
	}
	e.expect(owner, "POST", fmt.Sprintf("/api/applications/%d/envs/promote", app), map[string]any{"to": fmt.Sprint(otherApp), "keys": []string{"DB_PASSWORD"}}, 400)

	// Configuration as code: secret_refs in portdock.yaml.
	code, out, _ := e.raw(owner, fmt.Sprintf("/api/applications/%d/export", app))
	if code != 200 || !strings.Contains(out, "secret_refs:") || !strings.Contains(out, "DB_PASSWORD: vault-prod:shop/db#password") || strings.Contains(out, "vault-pa55") {
		t.Errorf("export: %d %s", code, out)
	}
	yaml := "version: 1\nproject: shop\nenvironment: production\napplications:\n  - name: web\n    image: nginx\n    server: local\n    env:\n      LOG_LEVEL: info\n" +
		"    secret_refs:\n      DB_PASSWORD: vault-prod:shop/db#password\n      DB_USER: vault-prod:shop/db#password\n      NEW_TOKEN: vault-prod:shop/api#token\n"
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/gitops/apply", strings.NewReader(yaml))
	req.Header.Set("Content-Type", "application/yaml")
	resp, err := owner.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var res applyResult
	_ = json.Unmarshal(b, &res)
	plan := []string{}
	for _, c := range res.Changes {
		if c.Resource == "variable" {
			plan = append(plan, c.Name+":"+c.Action)
		}
	}
	if resp.StatusCode != 200 || !strings.Contains(strings.Join(plan, " "), "DB_PASSWORD:unchanged DB_USER:update NEW_TOKEN:create") {
		t.Errorf("apply: %d %v %s", resp.StatusCode, plan, b)
	}
	if env, _ := e.api.engine.ResolveEnv(t.Context(), "application", app, false); env.Runtime["DB_USER"] != "vault-pa55" || env.Runtime["NEW_TOKEN"] != "tok-123" {
		t.Errorf("after apply: %v", env.Runtime)
	}

	// A store in use cannot be deleted; restricting it fails deployments clearly.
	e.expect(owner, "DELETE", fmt.Sprintf("/api/secret-stores/%d", storeID), nil, 400)
	e.expect(owner, "PATCH", fmt.Sprintf("/api/secret-stores/%d", storeID), map[string]any{"project_ids": []int64{int64(other["id"].(float64))}}, 200)
	if _, err := e.api.engine.ResolveEnv(t.Context(), "application", app, false); err == nil || !strings.Contains(err.Error(), "not available to this project") {
		t.Errorf("restricted store: %v", err)
	}
	// Turning a reference into a plain value.
	e.expect(dev, "PATCH", fmt.Sprintf("/api/envs/%d", dbPass), map[string]any{"ref_store_id": 0, "value": "local"}, 200)
	_, _ = e.db.Exec("DELETE FROM env_vars WHERE ref_store_id > 0")
	e.expect(owner, "DELETE", fmt.Sprintf("/api/secret-stores/%d", storeID), nil, 200)
}
