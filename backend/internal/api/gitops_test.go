package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/portdock/portdock/internal/gitops"
)

const shopYAML = `version: 1
project: shop
environment: staging
applications:
  - name: api
    git:
      repository: https://github.com/acme/api.git
      branch: main
    build:
      pack: dockerfile
    port: 8080
    replicas: 2
    resources: {cpu: "1", memory: 512m}
    health_check: {path: /health, timeout: 90}
    domains:
      - domain: https://api.example.com
    env:
      LOG_LEVEL: info
      FEATURE_X: "on"
    volumes:
      - {name: uploads, path: /app/uploads}
    jobs:
      - {name: cleanup, schedule: "0 4 * * *", command: "node scripts/cleanup.js"}
    protection:
      auth: sso
      ip_allow: [10.0.0.0/8]
      rate_limit: {average: 50, burst: 100}
`

func TestGitOpsApplyAndExport(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status, is_local) VALUES (1, 'local', 'localhost', 'reachable', 1)"); err != nil {
		t.Fatal(err)
	}
	owner := e.login("owner@corp.com")
	apply := func(c *http.Client, body string, dry bool) (int, applyResult) {
		url := e.srv.URL + "/api/gitops/apply"
		if dry {
			url += "?dry_run=true"
		}
		req, _ := http.NewRequest("POST", url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/yaml")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var res applyResult
		_ = json.NewDecoder(resp.Body).Decode(&res)
		return resp.StatusCode, res
	}
	summary := func(res applyResult) string {
		var parts []string
		for _, c := range res.Changes {
			parts = append(parts, c.Resource+":"+c.Name+":"+c.Action)
		}
		return strings.Join(parts, " ")
	}

	// Invalid files are refused before anything changes.
	if code, _ := apply(owner, "version: 1\nproject: shop\napplications:\n  - name: x\n    imag: nginx\n", false); code != 400 {
		t.Fatalf("typo accepted: %d", code)
	}

	// Dry run: the plan, and nothing created.
	code, res := apply(owner, shopYAML, true)
	if code != 200 || !res.DryRun || !strings.Contains(summary(res), "project:shop:create") || !strings.Contains(summary(res), "application:api:create") {
		t.Fatalf("dry run: %d %s %s", code, summary(res), res.Error)
	}
	if n := len(e.expectList(owner, "GET", "/api/projects")); n != 0 {
		t.Fatalf("dry run created %d project(s)", n)
	}

	// Apply creates everything.
	code, res = apply(owner, shopYAML, false)
	if code != 200 {
		t.Fatalf("apply: %d %s (%s)", code, res.Error, summary(res))
	}
	for _, want := range []string{"project:shop:create", "application:api:create", "domain:api.example.com:create", "variable:FEATURE_X:create", "volume:uploads:create", "job:cleanup:create", "protection:api:update"} {
		if !strings.Contains(summary(res), want) {
			t.Errorf("missing %s in %s", want, summary(res))
		}
	}
	apps := e.expectList(owner, "GET", "/api/applications")
	if len(apps) != 1 || apps[0]["port"] != float64(8080) || apps[0]["replicas"] != float64(2) || apps[0]["memory_limit"] != "512m" || apps[0]["health_check_timeout"] != float64(90) {
		t.Fatalf("application: %v", apps)
	}
	appID := apps[0]["id"]
	if p := e.expect(owner, "GET", fmt.Sprintf("/api/applications/%v/protection", appID), nil, 200); p["auth"] != "sso" || p["rate_average"] != float64(50) {
		t.Fatalf("protection: %v", p)
	}

	// Applying again changes nothing.
	_, res = apply(owner, shopYAML, false)
	for _, c := range res.Changes {
		if c.Action != "unchanged" {
			t.Errorf("second apply changed %v", c)
		}
	}

	// Edits and pruning.
	edited := strings.NewReplacer("port: 8080", "port: 9090", "LOG_LEVEL: info", "LOG_LEVEL: debug", `"0 4 * * *"`, `"30 4 * * *"`,
		"    volumes:\n      - {name: uploads, path: /app/uploads}\n", "").Replace(shopYAML)
	edited = strings.Replace(edited, "environment: staging", "environment: staging\nprune: true", 1)
	_, res = apply(owner, edited, false)
	for _, want := range []string{"application:api:update", "variable:LOG_LEVEL:update", "job:cleanup:update", "volume:uploads:delete"} {
		if !strings.Contains(summary(res), want) {
			t.Errorf("missing %s in %s", want, summary(res))
		}
	}

	// Secrets must exist in PortDock; the file only names them.
	withSecret := strings.Replace(shopYAML, "    volumes:", "    secrets: [DATABASE_URL]\n    volumes:", 1)
	if code, res := apply(owner, withSecret, false); code != 400 || !strings.Contains(res.Error, "DATABASE_URL") {
		t.Fatalf("missing secret: %d %s", code, res.Error)
	}
	e.expect(owner, "POST", fmt.Sprintf("/api/applications/%v/envs", appID), map[string]any{"key": "DATABASE_URL", "value": "postgres://db/x", "is_secret": true}, 200)
	if code, res := apply(owner, withSecret, false); code != 200 {
		t.Fatalf("with the secret set: %d %s", code, res.Error)
	}

	// Export renders a file that applies cleanly, without secret values.
	resp, _ := owner.Get(fmt.Sprintf("%s/api/applications/%v/export", e.srv.URL, appID))
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || bytes.Contains(out, []byte("postgres://db/x")) || !bytes.Contains(out, []byte("DATABASE_URL")) {
		t.Fatalf("export: %d\n%s", resp.StatusCode, out)
	}
	f, err := gitops.Parse(out)
	if err != nil || f.Project != "shop" || f.Environment != "staging" || len(f.Applications) != 1 || f.Applications[0].Port != 8080 { // the last apply restored the original file
		t.Fatalf("exported file: %v %+v", err, f)
	}
	_, res = apply(owner, string(out), false)
	for _, c := range res.Changes {
		if c.Action != "unchanged" {
			t.Errorf("re-applying the export changed %v", c)
		}
	}

	// Tokens: deploy scope cannot apply; a project-limited token cannot create projects.
	deployTok := e.expect(owner, "POST", "/api/tokens", map[string]any{"name": "ci", "scope": "deploy"}, 200)["token"].(string)
	limited := e.expect(owner, "POST", "/api/tokens", map[string]any{"name": "tf", "scope": "write", "project_ids": []any{1}}, 200)["token"].(string)
	bearer := func(tok string) *http.Client {
		return &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
			r.Header.Set("Authorization", "Bearer "+tok)
			return http.DefaultTransport.RoundTrip(r)
		})}
	}
	if code, _ := apply(bearer(deployTok), shopYAML, false); code != 403 {
		t.Fatalf("deploy token applied: %d", code)
	}
	other := strings.Replace(shopYAML, "project: shop", "project: other", 1)
	if code, res := apply(bearer(limited), other, false); code != 400 || !strings.Contains(res.Error, "cannot create new ones") {
		t.Fatalf("limited token created a project: %d %s", code, res.Error)
	}
	if code, res := apply(bearer(limited), shopYAML, true); code != 200 {
		t.Fatalf("limited token on its own project: %d %s", code, res.Error)
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
