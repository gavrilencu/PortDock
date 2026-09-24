package api

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

	sc := store.ImageScan{ApplicationID: id, Kind: "image", DeploymentID: 7, Reports: "report.html,trivy.json", Image: "nginx:1.25", Trigger: "deploy", Status: "success", Critical: 1, High: 2,
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
	if rs := scans[0].(map[string]any)["reports"].([]any); len(rs) != 2 || rs[0].(map[string]any)["name"] != "report.html" {
		t.Fatalf("reports: %v", rs)
	}
	// Reports are downloaded from the data directory; unknown names and missing files are 404.
	dir := filepath.Join(e.dir, "reports", fmt.Sprint(sc.ID))
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "report.html"), []byte("<h1>report</h1>"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "trivy.json"), []byte(`{"Results":[]}`), 0o644)
	code, body, hdr := e.raw(c, fmt.Sprintf("%s/scans/%d/reports/report.html", base, sc.ID))
	if code != 200 || body != "<h1>report</h1>" || !strings.Contains(hdr.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download: %d %q %v", code, body, hdr)
	}
	if code, _, hdr := e.raw(c, fmt.Sprintf("%s/scans/%d/reports/report.html?inline=1", base, sc.ID)); code != 200 || !strings.Contains(hdr.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("inline: %d %v", code, hdr)
	}
	for _, bad := range []string{"report.csv", "..%2Fsecret.json", "x.exe"} {
		if code, _, _ := e.raw(c, fmt.Sprintf("%s/scans/%d/reports/%s", base, sc.ID, bad)); code != 404 {
			t.Errorf("%s: %d", bad, code)
		}
	}
	code, body, _ = e.raw(c, fmt.Sprintf("%s/scans/%d/reports.zip", base, sc.ID))
	zr, err := zip.NewReader(strings.NewReader(body), int64(len(body)))
	if code != 200 || err != nil || len(zr.File) != 2 || zr.File[0].Name != fmt.Sprintf("image-%d/report.html", sc.ID) {
		t.Fatalf("zip: %d %v", code, err)
	}
	// The reports of a deployment.
	if _, err := e.db.Exec("INSERT INTO deployments(id, uuid, application_id, status, trigger) VALUES (7, 'd7', ?, 'success', 'manual')", id); err != nil {
		t.Fatal(err)
	}
	_, dl := e.do(c, "GET", "/api/deployments/7/reports", nil)
	if !strings.Contains(dl["_raw"].(string), `"kind":"image"`) {
		t.Fatalf("deployment reports: %v", dl)
	}
	if code, _, hdr := e.raw(c, "/api/deployments/7/reports.zip"); code != 200 || hdr.Get("Content-Type") != "application/zip" {
		t.Fatalf("deployment zip: %d", code)
	}

	// SAST needs the source; DAST settings are validated.
	e.expect(c, "POST", base+"/scan", map[string]any{"kind": "sast"}, 400)
	e.expect(c, "POST", base+"/scan", map[string]any{"kind": "nope"}, 400)
	app["sast_block"] = "critical"
	e.expect(c, "PATCH", base, app, 400)
	app["sast_block"], app["scan_block"], app["dast_target"] = "high", "high", "ftp://x"
	e.expect(c, "PATCH", base, app, 400)
	app["dast_target"] = "https://shop.example.com/"
	if got := e.expect(c, "PATCH", base, app, 200); got["dast_target"] != "https://shop.example.com/" || got["sast_block"] != "high" {
		t.Fatalf("dast target: %v", got)
	}
	if l := e.expect(c, "GET", base+"/scans?kind=dast", nil, 200); l["target"] != "https://shop.example.com/" || len(l["scans"].([]any)) != 0 {
		t.Fatalf("dast list: %v", l)
	}
	e.expect(c, "GET", base+"/scans?kind=nope", nil, 400)

	// A scan is only visible through its application.
	e.expect(c, "GET", fmt.Sprintf("/api/applications/%v/scans/%d", other["id"], sc.ID), nil, 404)

	// One scan at a time.
	_, _ = e.db.Exec("UPDATE applications SET current_image = 'nginx:1.25' WHERE id = ?", id)
	_, _ = e.db.Exec("UPDATE image_scans SET status = 'running' WHERE id = ?", sc.ID)
	e.expect(c, "POST", base+"/scan", nil, 409)
}

// raw GETs a path and returns the status, the body and the headers.
func (e *testEnv) raw(c *http.Client, path string) (int, string, http.Header) {
	e.t.Helper()
	res, err := c.Get(e.srv.URL + path)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b), res.Header
}
