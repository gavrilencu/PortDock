package api

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestComplianceReport(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("dev@corp.com", "correct-horse", "member")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	owner, dev := e.login("owner@corp.com"), e.login("dev@corp.com")
	p := e.expect(owner, "POST", "/api/projects", map[string]any{"name": "bank"}, 200)
	env := p["environments"].([]any)[0].(map[string]any)["id"]
	a := e.expect(owner, "POST", "/api/applications", map[string]any{"name": "core", "environment_id": env, "server_id": 1, "source_type": "image", "docker_image": "nginx"}, 200)
	now := time.Now().UTC().Format(time.RFC3339)
	for _, q := range []string{
		fmt.Sprintf("INSERT INTO deployments(uuid, application_id, status, trigger, commit_sha, requested_by, reviewed_by, reviewed_at, review_comment, created_at) VALUES ('d1', %v, 'success', 'manual', 'abcdef1234567890', 2, 1, '%s', 'looks good', '%s')", a["id"], now, now),
		fmt.Sprintf("INSERT INTO deployments(uuid, application_id, status, trigger, created_at) VALUES ('d2', %v, 'failed', 'webhook', '%s')", a["id"], now),
		fmt.Sprintf("INSERT INTO image_scans(application_id, kind, status, critical, high, created_at) VALUES (%v, 'image', 'success', 2, 5, '%s')", a["id"], now),
		"INSERT INTO monitors(id, name, url) VALUES (1, 'site', 'https://bank.example')",
		fmt.Sprintf("INSERT INTO monitor_incidents(monitor_id, error, started_at) VALUES (1, 'HTTP 502', '%s')", now),
		fmt.Sprintf("INSERT INTO activity(user_id, action, target, ip, created_at) VALUES (1, 'project.create', '=cmd|calc', '10.0.0.1', '%s')", now),
	} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	e.expect(dev, "GET", "/api/compliance/report", nil, 403)
	e.expect(owner, "GET", "/api/compliance/report?from=2026-13-01", nil, 400)
	e.expect(owner, "GET", "/api/compliance/report?from=2026-09-01&to=2026-08-01", nil, 400)

	js := e.expect(owner, "GET", "/api/compliance/report?format=json", nil, 200)
	sum := js["summary"].(map[string]any)
	if sum["Deployments"] != float64(2) || sum["DeploymentsFailed"] != float64(1) || sum["Approved"] != float64(1) || sum["ScansWithCritical"] != float64(1) || sum["Incidents"] != float64(1) {
		t.Errorf("summary: %v", sum)
	}
	if sum["Users"] != float64(2) || len(js["audit"].([]any)) == 0 {
		t.Errorf("users/audit: %v", js)
	}

	get := func(path string) (*http.Response, []byte) {
		req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
		res, err := owner.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res, b
	}
	res, html := get("/api/compliance/report")
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") || !strings.Contains(res.Header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("html: %d %v", res.StatusCode, res.Header)
	}
	for _, want := range []string{"Compliance report", "dev@corp.com", "looks good", "HTTP 502", "project.create", "bank / production / core"} {
		if !bytes.Contains(html, []byte(want)) {
			t.Errorf("the HTML report lacks %q", want)
		}
	}

	res, raw := get("/api/compliance/report?format=zip")
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Disposition"), ".zip") {
		t.Fatalf("zip: %d", res.StatusCode)
	}
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range z.File {
		rc, _ := f.Open()
		files[f.Name], _ = io.ReadAll(rc)
		rc.Close()
	}
	for _, n := range []string{"report.html", "report.json", "audit.csv", "deployments.csv", "backups.csv", "users.csv", "SHA256SUMS"} {
		if files[n] == nil {
			t.Errorf("missing %s", n)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(string(files["SHA256SUMS"])), "\n") {
		sumHex, name, _ := strings.Cut(line, "  ")
		got := sha256.Sum256(files[name])
		if hex.EncodeToString(got[:]) != sumHex {
			t.Errorf("checksum of %s", name)
		}
	}
	if !bytes.Contains(files["audit.csv"], []byte("'=cmd|calc")) {
		t.Errorf("formula injection not neutralised:\n%s", files["audit.csv"])
	}
	// One project: its data, without the organization's audit log.
	pj := e.expect(owner, "GET", fmt.Sprintf("/api/compliance/report?format=json&project=%v", p["id"]), nil, 200)
	if pj["scope"] != "bank" || len(pj["audit"].([]any)) != 0 || pj["summary"].(map[string]any)["Deployments"] != float64(2) {
		t.Errorf("project report: %v", pj)
	}
}
