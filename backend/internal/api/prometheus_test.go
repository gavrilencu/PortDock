package api

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

// promLine is one sample of the text format.
var promLine = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{([a-zA-Z_][a-zA-Z0-9_]*="(\\.|[^"\\])*",?)*\})? (-?[0-9.e+-]+|NaN|[+-]Inf)$`)

// checkPromText verifies the syntax and that each family is contiguous.
func checkPromText(t *testing.T, text string) {
	t.Helper()
	done, current := map[string]bool{}, ""
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if strings.HasPrefix(line, "# HELP ") || strings.HasPrefix(line, "# TYPE ") {
			name := strings.Fields(line)[2]
			if name != current {
				if done[name] {
					t.Errorf("family %s split", name)
				}
				done[current], current = true, name
			}
			continue
		}
		m := promLine.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("invalid line %q", line)
			continue
		}
		if m[1] != current {
			t.Errorf("sample %s outside its family (%s)", m[1], current)
		}
	}
}

func TestPrometheusMetrics(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status, proxy_status) VALUES (1, 'edge-1', 'localhost', 'reachable', 'running')"); err != nil {
		t.Fatal(err)
	}
	owner := e.login("owner@corp.com")
	mkProject := func(name string) (int64, any) {
		p := e.expect(owner, "POST", "/api/projects", map[string]any{"name": name}, 200)
		return int64(p["id"].(float64)), p["environments"].([]any)[0].(map[string]any)["id"]
	}
	pa, envA := mkProject("alpha")
	_, envB := mkProject("beta")
	mkApp := func(env any, name string) int64 {
		a := e.expect(owner, "POST", "/api/applications", map[string]any{"name": name, "environment_id": env, "server_id": 1, "source_type": "image", "docker_image": "nginx"}, 200)
		return int64(a["id"].(float64))
	}
	appA, appB := mkApp(envA, "web-a"), mkApp(envB, `we"b-b`)

	now := time.Now().Unix()
	ago := func(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339) }
	for _, q := range []string{
		fmt.Sprintf("INSERT INTO metrics(ts, scope, ref_id, server_id, cpu, mem, mem_total, disk, disk_total, rx, tx, load) VALUES (%d, 'server', 1, 1, 12.5, 2e9, 8e9, 4e10, 1e11, 1000, 2000, 0.7)", now-30),
		fmt.Sprintf("INSERT INTO metrics(ts, scope, ref_id, server_id, cpu, mem, mem_total, rx, tx, restarts, up) VALUES (%d, 'application', %d, 1, 50, 1e8, 0, 10, 20, 0, 1)", now-90, appA),
		fmt.Sprintf("INSERT INTO metrics(ts, scope, ref_id, server_id, cpu, mem, mem_total, rx, tx, restarts, up) VALUES (%d, 'application', %d, 1, 75, 2e8, 5e8, 10, 20, 2, 0.5)", now-30, appA),
		fmt.Sprintf("INSERT INTO metrics(ts, scope, ref_id, server_id, cpu, mem) VALUES (%d, 'application', %d, 1, 99, 1)", now-3600, appB), // stale
		fmt.Sprintf("INSERT INTO deployments(uuid, application_id, status, started_at, finished_at) VALUES ('d1', %d, 'success', '%s', '%s')", appA, ago(time.Hour+time.Minute), ago(time.Hour)),
		fmt.Sprintf("INSERT INTO deployments(uuid, application_id, status, started_at, finished_at) VALUES ('d2', %d, 'failed', '%s', '%s')", appA, ago(10*time.Minute+30*time.Second), ago(10*time.Minute)),
		fmt.Sprintf("INSERT INTO deployments(uuid, application_id, status, started_at, finished_at) VALUES ('d3', %d, 'success', '%s', '%s')", appB, ago(time.Hour), ago(time.Hour)),
		fmt.Sprintf("INSERT INTO app_jobs(application_id, name, schedule, command, last_run_at, last_status) VALUES (%d, 'report', '@daily', 'true', '%s', 'failed')", appA, ago(time.Hour)),
		fmt.Sprintf("INSERT INTO app_domains(application_id, host, path, cert_expires_at) VALUES (%d, 'a.example.com', '', '%s')", appA, ago(-30*24*time.Hour)),
		"INSERT INTO monitors(name, url, status, last_response_ms) VALUES ('site', 'https://example.com', 'up', 250)",
		"INSERT INTO alert_rules(id, name, scope, metric, severity) VALUES (1, 'cpu', 'server', 'cpu', 'critical')",
		"INSERT INTO alert_events(rule_id, scope, ref_id, status, started_at) VALUES (1, 'server', 1, 'firing', '2026-01-01T00:00:00Z')",
	} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	scrape := func(token string) (int, string) {
		req, _ := http.NewRequest("GET", e.srv.URL+"/api/metrics", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode == 200 && !strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain; version=0.0.4") {
			t.Errorf("content type %q", res.Header.Get("Content-Type"))
		}
		return res.StatusCode, string(b)
	}
	if code, _ := scrape(""); code != 401 {
		t.Fatalf("anonymous scrape: %d", code)
	}

	all := e.expect(owner, "POST", "/api/tokens", map[string]any{"name": "prometheus", "scope": "read"}, 200)["token"].(string)
	code, text := scrape(all)
	if code != 200 {
		t.Fatalf("scrape: %d %s", code, text)
	}
	checkPromText(t, text)
	a := fmt.Sprintf(`kind="application",project="alpha",environment="production",name="web-a",id="%d"`, appA)
	for _, want := range []string{
		`portdock_build_info{version="` + Version + `"} 1`,
		`portdock_server_up{server="edge-1"} 1`,
		`portdock_server_proxy_up{server="edge-1"} 1`,
		`portdock_server_cpu_percent{server="edge-1"} 12.5`,
		`portdock_server_memory_total_bytes{server="edge-1"} 8e+09`,
		`portdock_resource_cpu_percent{` + a + `,server="edge-1"} 75`, // the latest sample only
		`portdock_resource_memory_limit_bytes{` + a + `,server="edge-1"} 5e+08`,
		`portdock_resource_restarts{` + a + `,server="edge-1"} 2`,
		`portdock_resource_up{` + a + `,server="edge-1"} 0.5`,
		`portdock_resource_info{` + a + `,status="stopped",server="edge-1"} 1`,
		`name="we\"b-b"`,
		`portdock_deployments{` + a + `,status="failed"} 1`,
		`portdock_deployment_last_success{` + a + `} 0`,
		`portdock_deployment_last_duration_seconds{` + a + `} 30`,
		`portdock_job_last_run_success{project="alpha",environment="production",application="web-a",job="report"} 0`,
		`portdock_certificate_expiry_timestamp_seconds{project="alpha",environment="production",application="web-a",domain="a.example.com"}`,
		`portdock_monitor_up{monitor="site",url="https://example.com"} 1`,
		`portdock_monitor_response_seconds{monitor="site",url="https://example.com"} 0.25`,
		`portdock_alerts_firing{severity="critical"} 1`,
		`portdock_alerts_firing{severity="warning"} 0`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(text, "} 99\n") {
		t.Error("a stale sample was exported")
	}
	if !strings.Contains(text, "portdock_deployment_last_success_timestamp_seconds{"+a+"}") {
		t.Error("missing the last successful deployment time")
	}

	// A token limited to one project exports only that project, and no
	// organization-wide alerts.
	limited := e.expect(owner, "POST", "/api/tokens", map[string]any{"name": "alpha", "scope": "read", "project_ids": []int64{pa}}, 200)["token"].(string)
	_, text = scrape(limited)
	checkPromText(t, text)
	if !strings.Contains(text, `name="web-a"`) || strings.Contains(text, "beta") || strings.Contains(text, "portdock_alerts_firing") {
		t.Errorf("limited token export:\n%s", text)
	}
}
