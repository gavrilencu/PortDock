package api

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestBackupJobsE2E drives backups through the API against real containers:
// the job queue (a second backup waits as pending), the stored file and its
// download, a restore that first takes a safety backup, and deleting a backup.
// It needs Docker, so it only runs with PORTDOCK_DB_E2E=1.
func TestBackupJobsE2E(t *testing.T) {
	if os.Getenv("PORTDOCK_DB_E2E") != "1" {
		t.Skip("set PORTDOCK_DB_E2E=1 to run the backup job tests against Docker")
	}
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status, is_local) VALUES (1, 'local', 'localhost', 'reachable', 1)"); err != nil {
		t.Fatal(err)
	}
	c := e.login("owner@corp.com")
	p := e.expect(c, "POST", "/api/projects", map[string]any{"name": "shop"}, 200)
	env := p["environments"].([]any)[0].(map[string]any)["id"]

	type engineCase struct {
		engine, write, change, read, want, magic string
	}
	cases := []engineCase{
		{
			engine: "postgres",
			write:  `psql -U postgres -d app -v ON_ERROR_STOP=1 -c "CREATE TABLE t(id int primary key, v text); INSERT INTO t VALUES (1, 'hello');"`,
			change: `psql -U postgres -d app -v ON_ERROR_STOP=1 -c "UPDATE t SET v = 'changed';"`,
			read:   `psql -U postgres -d app -tAc "SELECT v FROM t WHERE id = 1"`,
			want:   "hello", magic: "PGDMP",
		},
		{
			engine: "mssql",
			write:  `/opt/mssql-tools18/bin/sqlcmd -C -S localhost -U sa -P "$MSSQL_SA_PASSWORD" -b -d app -Q "CREATE TABLE t(id int primary key, v nvarchar(20)); INSERT INTO t VALUES (1, 'hello');"`,
			change: `/opt/mssql-tools18/bin/sqlcmd -C -S localhost -U sa -P "$MSSQL_SA_PASSWORD" -b -d app -Q "UPDATE t SET v = 'changed';"`,
			read:   `/opt/mssql-tools18/bin/sqlcmd -C -S localhost -U sa -P "$MSSQL_SA_PASSWORD" -b -d app -h -1 -W -Q "SET NOCOUNT ON; SELECT v FROM t WHERE id = 1"`,
			want:   "hello", magic: "\x1f\x8b",
		},
	}
	only := os.Getenv("PORTDOCK_DB_E2E_ENGINES")
	for _, tc := range cases {
		if only != "" && !strings.Contains(only, tc.engine) {
			continue
		}
		t.Run(tc.engine, func(t *testing.T) {
			d := e.expect(c, "POST", "/api/databases", map[string]any{"name": "app", "engine": tc.engine, "environment_id": env, "server_id": 1}, 200)
			id := int64(d["id"].(float64))
			uuid := d["uuid"].(string)
			base := fmt.Sprintf("/api/databases/%d", id)
			t.Cleanup(func() {
				e.do(c, "DELETE", base, nil)
				_ = exec.Command("docker", "rm", "-f", uuid).Run()
				_ = exec.Command("docker", "volume", "rm", "-f", "sy-"+uuid).Run()
			})
			sh := func(script string) (string, error) {
				out, err := exec.Command("docker", "exec", uuid, "sh", "-c", script).CombinedOutput()
				return strings.TrimSpace(string(out)), err
			}
			// Wait until the database accepts the test's writes (for SQL Server, until PortDock created it).
			deadline := time.Now().Add(5 * time.Minute)
			for {
				if _, err := sh(tc.write); err == nil {
					break
				}
				if time.Now().After(deadline) {
					out, _ := exec.Command("docker", "logs", "--tail", "20", uuid).CombinedOutput()
					t.Fatalf("database never became writable:\n%s", out)
				}
				time.Sleep(3 * time.Second)
			}

			// Two backups asked at once: the second waits for the first.
			r1 := e.expect(c, "POST", base+"/backup-now", nil, 200)
			r2 := e.expect(c, "POST", base+"/backup-now", nil, 200)
			if r1["status"] != "pending" || r2["status"] != "pending" || r1["kind"] != "manual" {
				t.Fatalf("queued runs: %v %v", r1, r2)
			}
			sawWaiting := false
			waitRuns := func(done func(runs []map[string]any) bool) []map[string]any {
				t.Helper()
				for i := 0; i < 300; i++ {
					res := e.expect(c, "GET", base+"/backup-runs", nil, 200)
					var runs []map[string]any
					for _, r := range res["runs"].([]any) {
						runs = append(runs, r.(map[string]any))
					}
					running := 0
					for _, r := range runs {
						if r["status"] == "running" {
							running++
						}
						if r["status"] == "pending" {
							sawWaiting = true
						}
					}
					if running > 1 {
						t.Fatal("two backups of the same database ran at the same time")
					}
					if done(runs) {
						return runs
					}
					time.Sleep(500 * time.Millisecond)
				}
				t.Fatal("backups didn't finish")
				return nil
			}
			runs := waitRuns(func(runs []map[string]any) bool {
				ok := 0
				for _, r := range runs {
					if r["status"] == "failed" {
						t.Fatalf("backup failed: %v", r["message"])
					}
					if r["status"] == "success" {
						ok++
					}
				}
				return ok == 2
			})
			if !sawWaiting {
				t.Error("the second backup should have waited as pending")
			}
			res := e.expect(c, "GET", base+"/backup-runs", nil, 200)
			dir := res["directory"].(string)
			first := runs[len(runs)-1]
			file := first["file"].(string)
			if !strings.HasPrefix(file, dir+"/") || first["size"].(float64) <= 0 {
				t.Fatalf("backup file %q outside %q or empty", file, dir)
			}
			if _, err := os.Stat(file); err != nil {
				t.Fatalf("backup file missing on the server: %v", err)
			}

			// Download it.
			dl, err := c.Get(e.srv.URL + fmt.Sprintf("/api/backup-runs/%v/download", first["id"]))
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(dl.Body)
			dl.Body.Close()
			if dl.StatusCode != 200 || !strings.HasPrefix(string(body), tc.magic) || !strings.Contains(dl.Header.Get("Content-Disposition"), "attachment") {
				t.Fatalf("download: %d %q %q", dl.StatusCode, body[:min(8, len(body))], dl.Header.Get("Content-Disposition"))
			}

			// Restore: a safety backup of the current data comes first.
			if _, err := sh(tc.change); err != nil {
				t.Fatalf("change: %v", err)
			}
			e.expect(c, "POST", fmt.Sprintf("/api/backup-runs/%v/restore", first["id"]), nil, 200)
			runs = waitRuns(func(runs []map[string]any) bool {
				for _, r := range runs {
					if r["id"] == first["id"] {
						if r["restore_status"] == "failed" {
							t.Fatalf("restore failed: %v", r["restore_message"])
						}
						return r["restore_status"] == "success"
					}
				}
				return false
			})
			if got, err := sh(tc.read); err != nil || got != tc.want {
				t.Fatalf("after restore: %q %v", got, err)
			}
			safety := 0
			for _, r := range runs {
				if r["kind"] == "pre-restore" && r["status"] == "success" {
					safety++
				}
			}
			if safety != 1 {
				t.Fatalf("expected one safety backup, got %d", safety)
			}

			// Delete the second backup: the file goes too.
			second := runs[len(runs)-2]
			e.expect(c, "DELETE", fmt.Sprintf("/api/backup-runs/%v", second["id"]), nil, 200)
			if _, err := os.Stat(second["file"].(string)); !os.IsNotExist(err) {
				t.Fatalf("deleted backup file still exists: %v", err)
			}
		})
	}
}
