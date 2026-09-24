package sqlconsole

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/portdock/portdock/internal/templates"
)

// TestConsoleE2E runs the console against every SQL engine in Docker:
// PORTDOCK_DB_E2E=1 go test ./internal/sqlconsole -run ConsoleE2E
// (PORTDOCK_DB_E2E_ENGINES=postgres,mssql limits it to some engines).
func TestConsoleE2E(t *testing.T) {
	if os.Getenv("PORTDOCK_DB_E2E") != "1" {
		t.Skip("set PORTDOCK_DB_E2E=1 to run the SQL console against Docker")
	}
	os.Setenv("MSYS_NO_PATHCONV", "1")
	only := map[string]bool{}
	for _, e := range strings.Split(os.Getenv("PORTDOCK_DB_E2E_ENGINES"), ",") {
		if e = strings.TrimSpace(e); e != "" {
			only[e] = true
		}
	}
	const pass, db = "Pd3xamplePassw0rd", "app"
	users := map[string]string{"postgres": "portdock", "mysql": "portdock", "mariadb": "portdock", "clickhouse": "portdock", "mssql": "sa"}
	type script struct{ create, insert, update, bypass string }
	scripts := map[string]script{
		"postgres": {
			create: "CREATE TABLE t (id int PRIMARY KEY, name text, note text)",
			insert: "INSERT INTO t VALUES (1, 'Ана; café', NULL), (2, 'b,c', E'line1\\nline2\\ttab')",
			update: "UPDATE t SET note = 'x' WHERE id = 1",
			bypass: "WITH d AS (DELETE FROM t RETURNING *) SELECT count(*) FROM d",
		},
		"mysql": {
			create: "CREATE TABLE t (id int PRIMARY KEY, name varchar(50), note text) CHARSET utf8mb4",
			insert: "INSERT INTO t VALUES (1, 'Ана; café', NULL), (2, 'b,c', 'line1\\nline2\\ttab')",
			update: "UPDATE t SET note = 'x' WHERE id = 1",
			bypass: "DELETE FROM t",
		},
		"mariadb": {
			create: "CREATE TABLE t (id int PRIMARY KEY, name varchar(50), note text) CHARSET utf8mb4",
			insert: "INSERT INTO t VALUES (1, 'Ана; café', NULL), (2, 'b,c', 'line1\\nline2\\ttab')",
			update: "UPDATE t SET note = 'x' WHERE id = 1",
			bypass: "DELETE FROM t",
		},
		"mssql": {
			create: "CREATE TABLE t (id int PRIMARY KEY, name nvarchar(50), note nvarchar(100))",
			insert: "INSERT INTO t VALUES (1, N'Ана; café', NULL), (2, 'b,c', 'line1' + CHAR(10) + 'line2' + CHAR(9) + 'tab')",
			update: "UPDATE t SET note = 'x' WHERE id = 1",
			bypass: "DELETE FROM t",
		},
		"clickhouse": {
			create: "CREATE TABLE t (id UInt32, name String, note Nullable(String)) ENGINE = MergeTree ORDER BY id",
			insert: "INSERT INTO t VALUES (1, 'Ана; café', NULL), (2, 'b,c', 'line1\\nline2\\ttab')",
			update: "ALTER TABLE t UPDATE note = 'x' WHERE id = 1 SETTINGS mutations_sync = 1",
			bypass: "INSERT INTO t VALUES (3, 'z', NULL)",
		},
	}
	for id, sc := range scripts {
		if len(only) > 0 && !only[id] {
			continue
		}
		eng := templates.Engines[id]
		t.Run(id, func(t *testing.T) {
			u := users[id]
			name := "portdock-sql-" + id
			_ = exec.Command("docker", "rm", "-f", name).Run()
			args := []string{"run", "-d", "--name", name}
			for k, v := range eng.Env(u, pass, db) {
				args = append(args, "-e", k+"="+v)
			}
			args = append(args, eng.DefaultImage)
			if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
				t.Fatalf("start: %v\n%s", err, out)
			}
			t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
			sh := func(cmd, stdin string) (string, string, error) {
				c := exec.Command("docker", "exec", "-i", name, "sh", "-c", cmd)
				c.Stdin = strings.NewReader(stdin)
				var out, errb bytes.Buffer
				c.Stdout, c.Stderr = &out, &errb
				err := c.Run()
				return out.String(), errb.String(), err
			}
			deadline := time.Now().Add(4 * time.Minute)
			for {
				if _, _, err := sh(eng.HealthCmd(u, pass, db), ""); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("not healthy in time")
				}
				time.Sleep(2 * time.Second)
			}
			if init := eng.InitCmd(u, pass, db); init != "" {
				if _, e, err := sh(init, ""); err != nil {
					t.Fatalf("init: %v %s", err, e)
				}
			}
			run := func(script string, readOnly bool) ([]Result, string) {
				t.Helper()
				st, err := Split(id, script)
				if err != nil {
					t.Fatalf("split %q: %v", script, err)
				}
				cmd, in := Command(id, u, pass, db, st, readOnly)
				out, errOut, err := sh(cmd, in)
				if os.Getenv("PORTDOCK_SQL_DEBUG") == "1" {
					t.Logf("script:\n%s\nstdout:\n%q\nstderr:\n%q\nerr: %v", in, out, errOut, err)
				}
				msg := ""
				if err != nil {
					msg = CleanError(id, errOut)
					if msg == "" {
						msg = err.Error()
					}
				}
				return Parse(id, out, st), msg
			}

			res, msg := run(sc.create+";\n"+sc.insert+";\n"+sc.update+";\nSELECT id, name, note FROM t ORDER BY id;\nSELECT id FROM t WHERE id > 100", false)
			if msg != "" {
				t.Fatalf("write script: %s", msg)
			}
			if len(res) != 5 {
				t.Fatalf("results: %d %+v", len(res), res)
			}
			if id != "clickhouse" && res[1].Affected != 2 {
				t.Errorf("insert affected %d", res[1].Affected)
			}
			if id != "clickhouse" && res[2].Affected != 1 {
				t.Errorf("update affected %d", res[2].Affected)
			}
			sel := res[3]
			if strings.Join(sel.Columns, ",") != "id,name,note" || len(sel.Rows) != 2 {
				t.Fatalf("select: %+v", sel)
			}
			if *sel.Rows[0][1] != "Ана; café" || *sel.Rows[0][2] != "x" || *sel.Rows[1][1] != "b,c" {
				t.Errorf("values: %q %q %q", *sel.Rows[0][1], *sel.Rows[0][2], *sel.Rows[1][1])
			}
			if v := sel.Rows[1][2]; v == nil || *v != "line1\nline2\ttab" {
				t.Errorf("multi-line value: %v", v)
			}
			if res[4].Columns == nil || len(res[4].Rows) != 0 {
				t.Errorf("empty result: %+v", res[4])
			}

			// NULL is NULL.
			res, _ = run("SELECT note FROM t WHERE id = 2 AND note IS NOT NULL; SELECT NULL AS n", false)
			if len(res) != 2 || res[1].Rows[0][0] != nil {
				t.Errorf("null: %+v", res)
			}

			// Read-only: reading works, and the session refuses writes even
			// when a writing statement gets past the keyword check.
			res, msg = run("SELECT count(*) AS n FROM t", true)
			if msg != "" || len(res) != 1 || *res[0].Rows[0][0] != "2" {
				t.Fatalf("read-only select: %s %+v", msg, res)
			}
			_, msg = run(sc.bypass, true)
			if msg == "" {
				t.Fatalf("read-only session accepted %q", sc.bypass)
			}
			t.Logf("write refused in read-only mode: %s", firstLine(msg))
			res, _ = run("SELECT count(*) AS n FROM t", false)
			if *res[0].Rows[0][0] != "2" {
				t.Fatalf("data changed in read-only mode: %+v", res)
			}

			// An error stops the script; earlier results are kept.
			res, msg = run("SELECT 1 AS a; SELECT * FROM missing_table; SELECT 2", false)
			if msg == "" || len(res) != 1 {
				t.Fatalf("error: %q %+v", msg, res)
			}
			t.Logf("error: %s", firstLine(msg))

			// The tables of the database.
			res, msg = run(TablesQuery(id), true)
			if msg != "" || len(res) != 1 || !strings.Contains(fmt.Sprint(rowsText(res[0])), "t") {
				t.Fatalf("tables: %s %+v", msg, res)
			}
		})
	}
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

func rowsText(r Result) []string {
	var out []string
	for _, row := range r.Rows {
		for _, c := range row {
			if c != nil {
				out = append(out, *c)
			}
		}
	}
	return out
}
