package deploy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/portdock/portdock/internal/config"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/netcfg"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
)

func testEngine(t *testing.T) *Engine {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	box, _ := secrets.LoadOrCreate("", dir)
	return New(config.Config{DataDir: filepath.ToSlash(dir), ProxyNetwork: "pd-sec-test"}, db, box, nil, nil, nil, nil)
}

const vulnerablePython = `import os, subprocess, pickle, hashlib
from flask import Flask, request
app = Flask(__name__)

@app.route("/run")
def run():
    subprocess.call(request.args.get("cmd"), shell=True)
    return os.system("ls " + request.args.get("dir"))

@app.route("/load")
def load():
    return pickle.loads(request.data)

def weak(p):
    return hashlib.md5(p.encode()).hexdigest()

if __name__ == "__main__":
    app.run(debug=True, host="0.0.0.0")
`

// TestSecurityScansE2E runs Semgrep and OWASP ZAP for real:
// PORTDOCK_SEC_E2E=1 go test ./internal/deploy -run SecurityScansE2E
func TestSecurityScansE2E(t *testing.T) {
	if os.Getenv("PORTDOCK_SEC_E2E") != "1" {
		t.Skip("set PORTDOCK_SEC_E2E=1 to run Semgrep and OWASP ZAP in Docker")
	}
	os.Setenv("MSYS_NO_PATHCONV", "1")
	e := testEngine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	x := executor.Local{}
	for _, q := range []string{
		"INSERT INTO projects(id, name, created_at) VALUES (1, 'p', '')",
		"INSERT INTO environments(id, project_id, name, created_at) VALUES (1, 1, 'production', '')",
		"INSERT INTO servers(id, name, host, status, is_local) VALUES (1, 'local', 'localhost', 'reachable', 1)",
		"INSERT INTO applications(id, uuid, environment_id, server_id, name) VALUES (1, 'pdsectest', 1, 1, 'shop')",
	} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	app := store.Application{ID: 1, Name: "shop", UUID: "pdsectest"}

	t.Run("sast", func(t *testing.T) {
		src := filepath.ToSlash(t.TempDir())
		if err := os.WriteFile(filepath.Join(src, "app.py"), []byte(vulnerablePython), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := executor.Output(ctx, x, semgrepCmd(DefaultSemgrepImage, []string{DefaultSemgrepConfig}, src, netcfg.Config{}))
		if err != nil && !strings.Contains(out, `"runs"`) {
			t.Fatalf("semgrep: %v", err)
		}
		fs, err := ParseSARIF([]byte(out))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("semgrep: %d findings", len(fs))
		for _, f := range fs[:min(len(fs), 5)] {
			t.Logf("  %s %s %s:%d %s", f.Severity, f.ID, f.Target, f.Line, f.CWE)
		}
		if len(fs) < 2 || fs[0].Target != "app.py" || fs[0].Line == 0 || fs[0].Title == "" {
			t.Fatalf("expected findings in app.py: %+v", fs)
		}
		s := store.ImageScan{ID: 101, Kind: "sast", Status: "success", CommitSHA: "abc123", CreatedAt: store.Now(), FinishedAt: store.Now()}
		countFindings(&s, fs)
		e.saveReports(ctx, app, &s, fs, map[string][]byte{"semgrep.sarif": []byte(out)})
		checkReports(t, e, s, []string{"findings.json", "report.csv", "report.html", "semgrep.sarif"}, fs[0].ID)
	})

	t.Run("dast", func(t *testing.T) {
		_ = exec.Command("docker", "network", "create", "pd-sec-test").Run()
		t.Cleanup(func() { _ = exec.Command("docker", "network", "rm", "pd-sec-test").Run() })
		_ = exec.Command("docker", "rm", "-f", "pdsectest").Run()
		if out, err := exec.Command("docker", "run", "-d", "--name", "pdsectest", "--network", "pd-sec-test", "--network-alias", "pdsectest", "nginx:alpine").CombinedOutput(); err != nil {
			t.Fatalf("start nginx: %v %s", err, out)
		}
		t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", "pdsectest").Run() })
		time.Sleep(2 * time.Second)
		app.Port = 80
		target, err := e.DASTTarget(ctx, app)
		if err != nil || target != "http://pdsectest:80/" {
			t.Fatalf("target: %s %v", target, err)
		}
		s, err := e.startScan(ctx, store.ImageScan{ApplicationID: app.ID, Kind: "dast", Trigger: "manual", Target: target})
		if err != nil {
			t.Fatal(err)
		}
		e.runDAST(ctx, x, app, &s)
		got, _ := store.ByID[store.ImageScan](ctx, e.db, "image_scans", s.ID)
		if got.Status != "success" {
			t.Fatalf("dast: %s %s", got.Status, got.Error)
		}
		fs := ScanFindings(got)
		t.Logf("zap: %d alerts (%d medium, %d low, %d info)", len(fs), got.Medium, got.Low, got.Unknown)
		for _, f := range fs[:min(len(fs), 5)] {
			t.Logf("  %s %s %q x%d %s", f.Severity, f.ID, f.Title, f.Count, f.CWE)
		}
		if len(fs) == 0 || fs[0].Title == "" || fs[0].Target == "" {
			t.Fatalf("expected alerts for a default nginx: %+v", fs)
		}
		checkReports(t, e, got, []string{"findings.json", "report.csv", "report.html", "zap.html", "zap.json"}, fs[0].Title)
	})
}

func checkReports(t *testing.T, e *Engine, s store.ImageScan, want []string, mustContain string) {
	t.Helper()
	var names []string
	for _, r := range e.Reports(s) {
		names = append(names, r.Name)
		if r.Size == 0 {
			t.Errorf("%s is empty", r.Name)
		}
	}
	for _, w := range want {
		if !strings.Contains(","+strings.Join(names, ",")+",", ","+w+",") {
			t.Fatalf("reports %v, want %v", names, want)
		}
	}
	html, _ := os.ReadFile(e.ReportPath(s, "report.html"))
	if !strings.Contains(string(html), strings.ReplaceAll(mustContain, "'", "&#39;")) {
		t.Errorf("report.html doesn't mention %q", mustContain)
	}
	var zipBuf strings.Builder
	if err := e.WriteReportsZip(&zipBuf, []store.ImageScan{s}); err != nil || zipBuf.Len() == 0 {
		t.Fatalf("zip: %v", err)
	}
}
