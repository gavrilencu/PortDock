package sqlconsole

import (
	"strings"
	"testing"
)

func texts(st []Statement) []string {
	var out []string
	for _, s := range st {
		out = append(out, s.Text)
	}
	return out
}

func TestSplit(t *testing.T) {
	cases := []struct {
		engine, script string
		want           []string
	}{
		{"postgres", "SELECT 1; SELECT 'a;b' AS \"x;y\";\n-- only a comment;\n", []string{"SELECT 1", `SELECT 'a;b' AS "x;y"`}},
		{"postgres", "CREATE FUNCTION f() RETURNS int AS $body$ SELECT 1; $body$ LANGUAGE sql; SELECT f()", []string{"CREATE FUNCTION f() RETURNS int AS $body$ SELECT 1; $body$ LANGUAGE sql", "SELECT f()"}},
		{"postgres", "SELECT E'it\\'s;' ; /* a; b */ SELECT 2", []string{"SELECT E'it\\'s;'", "/* a; b */ SELECT 2"}},
		{"mysql", "SELECT 'it\\'s;', `a;b` FROM t # c;\n; SELECT \"x;\"", []string{"SELECT 'it\\'s;', `a;b` FROM t # c;", `SELECT "x;"`}},
		{"mssql", "SELECT [a;b] FROM t\nGO\nSELECT 'it''s;'\ngo\n", []string{"SELECT [a;b] FROM t", "SELECT 'it''s;'"}},
		{"clickhouse", "SELECT 'a\\';' ; SHOW TABLES", []string{"SELECT 'a\\';'", "SHOW TABLES"}},
	}
	for _, c := range cases {
		got, err := Split(c.engine, c.script)
		if err != nil {
			t.Errorf("%s %q: %v", c.engine, c.script, err)
			continue
		}
		if strings.Join(texts(got), "|") != strings.Join(c.want, "|") {
			t.Errorf("%s %q:\n got %q\nwant %q", c.engine, c.script, texts(got), c.want)
		}
	}
	if _, err := Split("postgres", " -- nothing\n"); err == nil {
		t.Error("empty script accepted")
	}
}

func TestClientCommandsRejected(t *testing.T) {
	for _, c := range []struct{ engine, script string }{
		{"postgres", `\! id`},
		{"postgres", "SELECT 1;\n\\copy t TO '/tmp/x'"},
		{"postgres", `SELECT 1 \g /tmp/out`},
		{"mysql", "system id"},
		{"mysql", "SELECT 1;\n  source /etc/passwd"},
		{"mysql", `SELECT 1 \! id`},
		{"mariadb", "\\! id"},
		{"mssql", ":!! id"},
		{"mssql", "SELECT 1\n!! id"},
		{"mssql", ":r /etc/passwd"},
		{"clickhouse", `SELECT 1 \G`},
	} {
		if _, err := Split(c.engine, c.script); err == nil {
			t.Errorf("%s %q accepted", c.engine, c.script)
		}
	}
	// Backslashes in strings are data.
	if _, err := Split("postgres", `SELECT E'a\nb', 'c:\temp'`); err != nil {
		t.Error(err)
	}
	if _, err := Split("mssql", `SELECT 'C:\temp\x', 1 AS [a\b]`); err != nil {
		t.Error(err)
	}
}

func TestReadOnly(t *testing.T) {
	ok := map[string][]string{
		"postgres":   {"SELECT * FROM t", "(SELECT 1)", "WITH x AS (SELECT 1) SELECT * FROM x", "EXPLAIN SELECT 1", "SHOW search_path", "select 'set_config(' "},
		"mysql":      {"SELECT * FROM t", "SHOW TABLES", "DESCRIBE t", "EXPLAIN SELECT 1", "SELECT 'into outfile'"},
		"mssql":      {"SELECT TOP 10 * FROM t", "WITH x AS (SELECT 1 AS a) SELECT a FROM x", "SELECT sp_price FROM t"},
		"clickhouse": {"SELECT 1", "SHOW TABLES", "DESCRIBE t", "EXISTS TABLE t"},
	}
	bad := map[string][]string{
		"postgres":   {"INSERT INTO t VALUES (1)", "DELETE FROM t", "COMMIT", "SET ROLE postgres", "RESET ROLE", "SELECT set_config('role', 'none', false)", "SELECT pg_terminate_backend(1)", "COPY t TO STDOUT"},
		"mysql":      {"UPDATE t SET a = 1", "SELECT * FROM t INTO OUTFILE '/tmp/x'", "SET SESSION TRANSACTION READ WRITE", "CALL p()"},
		"mssql":      {"EXEC xp_cmdshell 'id'", "SELECT * INTO t2 FROM t", "REVERT", "SELECT * FROM OPENROWSET('x')", "UPDATE t SET a = 1"},
		"clickhouse": {"INSERT INTO t VALUES (1)", "SET readonly = 0", "SELECT 1 INTO OUTFILE 'x'", "SYSTEM SHUTDOWN"},
	}
	for eng, list := range ok {
		for _, s := range list {
			st, err := Split(eng, s)
			if err != nil {
				t.Fatal(err)
			}
			if err := CheckReadOnly(eng, st); err != nil {
				t.Errorf("%s %q rejected: %v", eng, s, err)
			}
		}
	}
	for eng, list := range bad {
		for _, s := range list {
			st, err := Split(eng, s)
			if err != nil {
				continue
			}
			if CheckReadOnly(eng, st) == nil {
				t.Errorf("%s %q accepted in read-only mode", eng, s)
			}
		}
	}
}

func TestParsers(t *testing.T) {
	st, _ := Split("postgres", "SELECT 1; UPDATE t SET a = 1")
	out := "id,name\n1,\"a,b\"\n2," + nullToken + "\n<<<PD_END 2\n<<<PD_END 5\n"
	res := Parse("postgres", out, st)
	if len(res) != 2 || strings.Join(res[0].Columns, "|") != "id|name" || *res[0].Rows[0][1] != "a,b" || res[0].Rows[1][1] != nil || res[0].Affected != -1 {
		t.Fatalf("postgres: %+v", res)
	}
	if res[1].Columns != nil || res[1].Affected != 5 || res[1].Statement != "UPDATE t SET a = 1" {
		t.Fatalf("postgres update: %+v", res[1])
	}

	out = "id\tv\n1\ta\\tb\\nc\n2\tNULL\npd__marker\n<<<PD_END -1\npd__marker\n<<<PD_END 3\n"
	res = Parse("mysql", out, st)
	if len(res) != 2 || *res[0].Rows[0][1] != "a\tb\nc" || res[0].Rows[1][1] != nil || res[1].Affected != 3 {
		t.Fatalf("mysql: %+v", res)
	}

	out = "id\x1fnote\n--\x1f----\n1\x1fline1\nline2\n2\x1fNULL\n<<<PD_END 2\n<<<PD_END 1\n"
	res = Parse("mssql", out, st)
	if len(res) != 2 || len(res[0].Rows) != 2 || *res[0].Rows[0][1] != "line1\nline2" || res[0].Rows[1][1] != nil || res[1].Affected != 1 {
		t.Fatalf("mssql: %+v", res)
	}

	out = `{"meta":[{"name":"n","type":"UInt8"}],"data":[["1"],[null]],"rows":2}` + "\n" + `{"meta":[{"name":"__pd_end","type":"UInt8"}],"data":[["1"]],"rows":1}` +
		`{"meta":[{"name":"__pd_end","type":"UInt8"}],"data":[["1"]],"rows":1}`
	res = Parse("clickhouse", out, st)
	if len(res) != 2 || res[0].Columns[0] != "n" || res[0].Rows[1][0] != nil || res[1].Columns != nil || res[1].Affected != 0 {
		t.Fatalf("clickhouse: %+v", res)
	}

	// Rows beyond the limit are counted, not returned.
	var b strings.Builder
	b.WriteString("n\n")
	for i := 0; i < MaxRows+10; i++ {
		b.WriteString("1\n")
	}
	b.WriteString("<<<PD_END 0\n")
	res = Parse("postgres", b.String(), st[:1])
	if len(res[0].Rows) != MaxRows || res[0].RowCount != MaxRows+10 || !res[0].Truncated {
		t.Fatalf("limit: %d rows, count %d", len(res[0].Rows), res[0].RowCount)
	}
}
