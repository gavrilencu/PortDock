// Package sqlconsole runs SQL typed in the browser with the database's own
// command-line client inside its container: psql, mysql / mariadb, sqlcmd and
// clickhouse-client. The script is split into statements, each followed by a
// marker, so the output of every statement can be told apart and parsed.
//
// Read-only mode, used for members without an admin role, has two layers:
// only reading statements are accepted (SELECT, WITH, SHOW, EXPLAIN…), and the
// session itself cannot write: a read-only transaction under a role that can
// only read (PostgreSQL), a user with SELECT only (MySQL, MariaDB), an
// impersonated db_datareader user (SQL Server) or readonly=1 (ClickHouse).
package sqlconsole

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/portdock/portdock/internal/executor"
)

var q = executor.Quote

// MaxRows is the number of rows returned per result; the rest is counted only.
const MaxRows = 5000

// Timeout is the statement timeout, in seconds.
const Timeout = 60

// Supported reports whether the console is available for an engine.
func Supported(engine string) bool {
	switch engine {
	case "postgres", "mysql", "mariadb", "mssql", "clickhouse":
		return true
	}
	return false
}

// Statement is one statement of a script.
type Statement struct {
	Text    string // as typed, without the final semicolon
	Code    string // the text with strings, quoted names and comments blanked out
	Keyword string // first keyword, upper case
}

// Result is the outcome of one statement.
type Result struct {
	Statement string      `json:"statement"`
	Columns   []string    `json:"columns"`
	Rows      [][]*string `json:"rows"` // nil cells are NULL
	RowCount  int         `json:"row_count"`
	Affected  int64       `json:"affected"` // rows changed; -1 when unknown or a query
	Truncated bool        `json:"truncated"`
}

// ---- splitting ----

var errClientCommand = errors.New("client commands (backslash commands, system, source…) are not allowed; send SQL only")

// Split splits a script into statements for an engine, rejecting commands of
// the command-line clients (they could run programs in the container).
func Split(engine, script string) ([]Statement, error) {
	mysqlLike := engine == "mysql" || engine == "mariadb"
	backslashStrings := mysqlLike || engine == "clickhouse"
	var out []Statement
	var text, code strings.Builder
	flush := func() {
		t := strings.TrimSpace(text.String())
		c := code.String()
		text.Reset()
		code.Reset()
		if strings.TrimSpace(c) == "" {
			return // only comments
		}
		out = append(out, Statement{Text: t, Code: c, Keyword: firstKeyword(c)})
	}
	rs := []rune(script)
	lineStart := true
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		next := rune(0)
		if i+1 < len(rs) {
			next = rs[i+1]
		}
		// Client commands at the beginning of a line.
		if lineStart {
			j := i
			for j < len(rs) && (rs[j] == ' ' || rs[j] == '\t') {
				j++
			}
			rest := string(rs[j:min(len(rs), j+40)])
			if engine == "mssql" {
				if strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, "!!") {
					return nil, errClientCommand
				}
				// GO on its own line separates batches.
				if m := goLine.FindString(string(rs[j:])); m != "" {
					flush()
					i = j + len([]rune(m)) - 1
					lineStart = true
					continue
				}
			}
			if mysqlLike && strings.TrimSpace(code.String()) == "" && mysqlCommand.MatchString(rest) {
				return nil, errClientCommand
			}
		}
		lineStart = c == '\n'
		switch {
		case c == '\\' && engine != "mssql":
			return nil, errClientCommand
		case c == '-' && next == '-', c == '#' && mysqlLike:
			for i < len(rs) && rs[i] != '\n' {
				text.WriteRune(rs[i])
				i++
			}
			if i < len(rs) {
				text.WriteRune('\n')
				code.WriteRune('\n')
				lineStart = true
			}
		case c == '/' && next == '*':
			end := strings.Index(string(rs[i+2:]), "*/")
			n := len(rs) - i
			if end >= 0 {
				n = len([]rune(string(rs[i+2:])[:end])) + 4
			}
			text.WriteString(string(rs[i : i+n]))
			code.WriteRune(' ')
			i += n - 1
		case c == '\'' || c == '"' || c == '`' || (c == '[' && engine == "mssql"):
			closeCh := c
			if c == '[' {
				closeCh = ']'
			}
			// PostgreSQL E'…' strings take backslash escapes.
			esc := backslashStrings && c != '`'
			if engine == "postgres" && c == '\'' && i > 0 && (rs[i-1] == 'E' || rs[i-1] == 'e') {
				esc = true
			}
			j := i + 1
			for j < len(rs) {
				if esc && rs[j] == '\\' {
					j += 2
					continue
				}
				if rs[j] == closeCh {
					if j+1 < len(rs) && rs[j+1] == closeCh { // doubled quote
						j += 2
						continue
					}
					break
				}
				j++
			}
			j = min(j, len(rs)-1)
			text.WriteString(string(rs[i : j+1]))
			if c == '\'' || (c == '"' && mysqlLike) {
				code.WriteString("''") // a string literal
			} else {
				// A quoted name stays visible to the read-only check: "pg_read_file"(...)
				// is the same call as pg_read_file(...).
				code.WriteString(string(rs[i : j+1]))
			}
			i = j
		case c == '$' && engine == "postgres":
			if m := dollarTag.FindString(string(rs[i:])); m != "" {
				body := string(rs[i+len([]rune(m)):])
				end := strings.Index(body, m)
				n := len(rs) - i
				if end >= 0 {
					n = len([]rune(m)) + len([]rune(body[:end])) + len([]rune(m))
				}
				text.WriteString(string(rs[i : i+n]))
				code.WriteString("''")
				i += n - 1
				continue
			}
			text.WriteRune(c)
			code.WriteRune(c)
		case c == ';':
			flush()
		default:
			text.WriteRune(c)
			code.WriteRune(c)
		}
	}
	flush()
	if len(out) == 0 {
		return nil, errors.New("enter a statement to run")
	}
	return out, nil
}

var (
	goLine       = regexp.MustCompile(`^(?i)go[ \t]*(\r?\n|$)`)
	dollarTag    = regexp.MustCompile(`^\$[A-Za-z_]*\$`)
	mysqlCommand = regexp.MustCompile(`^(?i)(system|source|delimiter|tee|notee|pager|nopager|edit|connect|charset|prompt|warnings|nowarnings|rehash|status|exit|quit|print|resetconnection|query_attributes|ssl_session_data_print|ego|go|help)\b`)
	wordRe       = regexp.MustCompile(`[A-Za-z_]+`)
)

func firstKeyword(code string) string {
	code = strings.TrimLeftFunc(code, func(r rune) bool { return unicode.IsSpace(r) || r == '(' })
	return strings.ToUpper(wordRe.FindString(code))
}

// ---- read-only rules ----

var readKeywords = map[string]map[string]bool{
	"postgres":   {"SELECT": true, "WITH": true, "SHOW": true, "EXPLAIN": true, "VALUES": true, "TABLE": true},
	"mysql":      {"SELECT": true, "WITH": true, "SHOW": true, "EXPLAIN": true, "DESCRIBE": true, "DESC": true, "TABLE": true, "VALUES": true},
	"mariadb":    {"SELECT": true, "WITH": true, "SHOW": true, "EXPLAIN": true, "DESCRIBE": true, "DESC": true, "VALUES": true},
	"mssql":      {"SELECT": true, "WITH": true},
	"clickhouse": {"SELECT": true, "WITH": true, "SHOW": true, "EXPLAIN": true, "DESCRIBE": true, "DESC": true, "EXISTS": true},
}

// Words that may write or leave the database even inside a reading statement.
var readForbidden = map[string]*regexp.Regexp{
	"postgres":   regexp.MustCompile(`(?i)\b(set_config|pg_terminate_backend|pg_cancel_backend|pg_reload_conf|pg_rotate_logfile|dblink\w*|lo_\w+)\b`),
	"mysql":      regexp.MustCompile(`(?i)\binto\s+(outfile|dumpfile)\b`),
	"mariadb":    regexp.MustCompile(`(?i)\binto\s+(outfile|dumpfile)\b`),
	"mssql":      regexp.MustCompile(`(?i)\b(into|openrowset|opendatasource|openquery)\b`),
	"clickhouse": regexp.MustCompile(`(?i)\binto\s+outfile\b`),
}

// CheckReadOnly rejects statements that read-only mode does not run.
func CheckReadOnly(engine string, stmts []Statement) error {
	for _, s := range stmts {
		if !readKeywords[engine][s.Keyword] {
			return errors.New("read-only access: only reading statements (SELECT, SHOW, EXPLAIN…) can run")
		}
		if m := readForbidden[engine].FindString(s.Code); m != "" {
			return errors.New("read-only access: the statement uses a function or clause that can write or read outside the database")
		}
	}
	return nil
}

// ---- commands ----

const nullToken = "␀NULL␀"

// Command returns the shell command to run in the container and the script to
// send to its standard input. pass is the password PortDock set for the
// database (the MSSQL sa password, the MySQL root password…).
func Command(engine, user, pass, db string, stmts []Statement, readOnly bool) (string, string) {
	var in strings.Builder
	switch engine {
	case "postgres":
		in.WriteString("SET client_min_messages = warning;\n")
		fmt.Fprintf(&in, "SET statement_timeout = '%ds';\n", Timeout)
		if readOnly {
			in.WriteString("BEGIN READ ONLY;\n")
		}
		for _, s := range stmts {
			in.WriteString(s.Text + "\n;\n\\echo '<<<PD_END' :ROW_COUNT\n")
		}
		if readOnly {
			in.WriteString("ROLLBACK;\n")
		}
		psql := fmt.Sprintf("psql -U %s -d %s -X -q --csv -P %s -v ON_ERROR_STOP=1", q(user), q(db), q("null="+nullToken))
		if readOnly {
			// Read-only statements run as a real login role that can only read
			// (pg_read_all_data on PostgreSQL 14+): a superuser session that
			// merely SET ROLE could always come back with RESET ROLE or reach
			// the server through pg_read_file and friends. The database owner
			// creates or repairs the role first.
			ensure := `DO $pd$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'portdock_readonly') THEN
    BEGIN CREATE ROLE portdock_readonly LOGIN; EXCEPTION WHEN duplicate_object THEN NULL; END;
  END IF;
  ALTER ROLE portdock_readonly LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT;
  BEGIN EXECUTE 'GRANT pg_read_all_data TO portdock_readonly';
  EXCEPTION WHEN undefined_object THEN
    EXECUTE 'GRANT USAGE ON SCHEMA public TO portdock_readonly';
    EXECUTE 'GRANT SELECT ON ALL TABLES IN SCHEMA public TO portdock_readonly';
  END;
END $pd$;`
			setup := fmt.Sprintf("psql -U %s -d %s -X -q -v ON_ERROR_STOP=1 -c %s >/dev/null", q(user), q(db), q(ensure))
			return setup + " && " + fmt.Sprintf("psql -U %s -d %s -X -q --csv -P %s -v ON_ERROR_STOP=1", q("portdock_readonly"), q(db), q("null="+nullToken)), in.String()
		}
		return psql, in.String()

	case "mysql", "mariadb":
		client := "mysql"
		limit := fmt.Sprintf("SET SESSION max_execution_time = %d;\n", Timeout*1000)
		if engine == "mariadb" {
			client, limit = "mariadb", fmt.Sprintf("SET SESSION max_statement_time = %d;\n", Timeout)
		}
		in.WriteString(limit)
		if readOnly {
			in.WriteString("START TRANSACTION READ ONLY;\n")
		}
		for _, s := range stmts {
			in.WriteString(s.Text + "\n;\nSELECT CONCAT('<<<PD_END ', ROW_COUNT()) AS pd__marker;\n")
		}
		if readOnly {
			in.WriteString("ROLLBACK;\n")
		}
		name := "`" + strings.ReplaceAll(db, "`", "``") + "`"
		opts := " --batch --default-character-set=utf8mb4 " + q(db)
		if !readOnly {
			return "MYSQL_PWD=" + q(pass) + " " + client + " -uroot" + opts, in.String()
		}
		// A user that can only read this database, with a password derived from the database's.
		ro := ReadOnlyPassword(pass)
		setup := fmt.Sprintf("CREATE USER IF NOT EXISTS 'portdock_ro'@'localhost' IDENTIFIED BY '%s'; ALTER USER 'portdock_ro'@'localhost' IDENTIFIED BY '%s'; GRANT SELECT, SHOW VIEW ON %s.* TO 'portdock_ro'@'localhost';", ro, ro, name)
		return "MYSQL_PWD=" + q(pass) + " " + client + " -uroot -e " + q(setup) + " && MYSQL_PWD=" + q(ro) + " " + client + " -uportdock_ro" + opts, in.String()

	case "mssql":
		in.WriteString("SET NOCOUNT ON;\nGO\n")
		if readOnly {
			in.WriteString("IF DATABASE_PRINCIPAL_ID('portdock_readonly') IS NULL BEGIN CREATE USER portdock_readonly WITHOUT LOGIN; ALTER ROLE db_datareader ADD MEMBER portdock_readonly; END\nGO\nEXECUTE AS USER = 'portdock_readonly';\nGO\n")
		}
		for _, s := range stmts {
			in.WriteString(s.Text + "\nGO\nPRINT '<<<PD_END ' + CAST(@@ROWCOUNT AS varchar(20));\nGO\n")
		}
		cmd := `sep=$(printf '\037'); if [ -x /opt/mssql-tools18/bin/sqlcmd ]; then S=/opt/mssql-tools18/bin/sqlcmd; T=-C; else S=/opt/mssql-tools/bin/sqlcmd; T=; fi; ` +
			`"$S" $T -S localhost -U sa -P ` + q(pass) + ` -d ` + q(db) + ` -b -X -r 0 -W -s "$sep" -w 65535 -f 65001 -t ` + strconv.Itoa(Timeout)
		return cmd, in.String()

	case "clickhouse":
		for _, s := range stmts {
			in.WriteString(s.Text + "\n;\nSELECT 1 AS __pd_end;\n")
		}
		cmd := fmt.Sprintf("clickhouse-client --user %s --password %s --database %s --multiquery --format JSONCompactStrings --max_execution_time %d",
			q(user), q(pass), q(db), Timeout)
		if readOnly {
			cmd += " --readonly 1"
		}
		return cmd, in.String()
	}
	return "", ""
}

// ReadOnlyPassword derives the password of the read-only MySQL user from the database password.
func ReadOnlyPassword(pass string) string {
	m := hmac.New(sha256.New, []byte(pass))
	m.Write([]byte("portdock-readonly"))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// TablesQuery lists the tables and views of the database.
func TablesQuery(engine string) string {
	switch engine {
	case "postgres":
		return `SELECT table_schema AS schema, table_name AS name, table_type AS type FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog', 'information_schema') ORDER BY 1, 2`
	case "mysql", "mariadb":
		return `SELECT table_name AS name, table_type AS type, table_rows AS row_estimate FROM information_schema.tables WHERE table_schema = DATABASE() ORDER BY 1`
	case "mssql":
		return `SELECT TABLE_SCHEMA AS [schema], TABLE_NAME AS name, TABLE_TYPE AS type FROM INFORMATION_SCHEMA.TABLES ORDER BY 1, 2`
	case "clickhouse":
		return `SELECT name, engine AS type, total_rows AS row_estimate FROM system.tables WHERE database = currentDatabase() AND NOT is_temporary ORDER BY name`
	}
	return ""
}

// ---- parsing ----

var endMarker = regexp.MustCompile(`^<<<PD_END (-?\d+)\s*$`)

// Parse reads the client output of the statements that ran (all of them, or
// those before the one that failed).
func Parse(engine, out string, stmts []Statement) []Result {
	res := []Result{}
	switch engine {
	case "clickhouse":
		res = parseClickHouse(out)
	default:
		blocks, counts := splitBlocks(engine, out)
		for i, b := range blocks {
			var r Result
			switch engine {
			case "postgres":
				r = parseCSV(b)
			case "mysql", "mariadb":
				r = parseTSV(b)
				// The client prints nothing for a query without rows (ROW_COUNT() is -1).
				if r.Columns == nil && counts[i] == -1 {
					r.Columns = []string{}
				}
			case "mssql":
				r = parseSqlcmd(b)
			}
			r.Affected = counts[i]
			res = append(res, r)
		}
	}
	for i := range res {
		if i < len(stmts) {
			res[i].Statement = stmts[i].Text
		}
		if res[i].Columns != nil {
			if engine == "mysql" || engine == "mariadb" || engine == "mssql" || engine == "postgres" {
				res[i].Affected = -1
			}
		} else if res[i].Affected < 0 {
			res[i].Affected = 0
		}
		if res[i].Rows == nil && res[i].Columns != nil {
			res[i].Rows = [][]*string{}
		}
	}
	return res
}

// splitBlocks cuts the output at the end markers, returning each block and the row count of the marker.
func splitBlocks(engine, out string) ([]string, []int64) {
	var blocks []string
	var counts []int64
	var cur []string
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if m := endMarker.FindStringSubmatch(line); m != nil {
			if (engine == "mysql" || engine == "mariadb") && len(cur) > 0 && strings.TrimSpace(cur[len(cur)-1]) == "pd__marker" {
				cur = cur[:len(cur)-1]
			}
			n, _ := strconv.ParseInt(m[1], 10, 64)
			blocks = append(blocks, strings.Join(cur, "\n"))
			counts = append(counts, n)
			cur = nil
			continue
		}
		cur = append(cur, line)
	}
	return blocks, counts
}

func cell(s string, null string) *string {
	if s == null {
		return nil
	}
	return &s
}

func addRow(r *Result, row []*string) {
	r.RowCount++
	if len(r.Rows) >= MaxRows {
		r.Truncated = true
		return
	}
	r.Rows = append(r.Rows, row)
}

// parseCSV reads psql --csv output (header first; NULL is nullToken).
func parseCSV(block string) Result {
	if strings.TrimSpace(block) == "" {
		return Result{}
	}
	rd := csv.NewReader(strings.NewReader(block))
	rd.FieldsPerRecord = -1
	rd.LazyQuotes = true
	var r Result
	for {
		rec, err := rd.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		if r.Columns == nil {
			r.Columns = rec
			continue
		}
		row := make([]*string, len(rec))
		for i, v := range rec {
			row[i] = cell(v, nullToken)
		}
		addRow(&r, row)
	}
	return r
}

var tsvUnescape = strings.NewReplacer(`\t`, "\t", `\n`, "\n", `\r`, "\r", `\0`, "\x00", `\\`, `\`)

// parseTSV reads mysql --batch output (tab separated, backslash escapes, NULL as text).
func parseTSV(block string) Result {
	if strings.TrimSpace(block) == "" {
		return Result{}
	}
	var r Result
	for _, line := range strings.Split(block, "\n") {
		fields := strings.Split(line, "\t")
		if r.Columns == nil {
			for i := range fields {
				fields[i] = tsvUnescape.Replace(fields[i])
			}
			r.Columns = fields
			continue
		}
		row := make([]*string, len(fields))
		for i, v := range fields {
			if v == "NULL" {
				continue
			}
			v = tsvUnescape.Replace(v)
			row[i] = &v
		}
		addRow(&r, row)
	}
	return r
}

const sep = "\x1f"

var dashLine = regexp.MustCompile("^-+(\x1f-+)*$")

// parseSqlcmd reads sqlcmd output with the unit separator: a header, a line of
// dashes, then the rows. Values spanning lines are joined back.
func parseSqlcmd(block string) Result {
	lines := strings.Split(block, "\n")
	var r Result
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if r.Columns == nil {
			if i+1 < len(lines) && dashLine.MatchString(strings.TrimSpace(lines[i+1])) && strings.Count(lines[i+1], sep) == strings.Count(line, sep) {
				r.Columns = strings.Split(line, sep)
				for k := range r.Columns {
					r.Columns[k] = strings.TrimSpace(r.Columns[k])
				}
				i++
			}
			continue
		}
		if line == "" && i == len(lines)-1 {
			continue
		}
		fields := strings.Split(line, sep)
		// A value with line breaks: the next lines without a separator continue it.
		for len(r.Columns) > 1 && i+1 < len(lines) && !strings.Contains(lines[i+1], sep) && lines[i+1] != "" {
			fields[len(fields)-1] += "\n" + lines[i+1]
			i++
		}
		row := make([]*string, len(fields))
		for k, v := range fields {
			row[k] = cell(v, "NULL")
		}
		addRow(&r, row)
	}
	return r
}

// parseClickHouse reads the JSONCompactStrings objects of each query; a
// SELECT 1 AS __pd_end closes the results of a statement.
func parseClickHouse(out string) []Result {
	dec := json.NewDecoder(strings.NewReader(out))
	res := []Result{}
	cur := Result{Affected: -1}
	for {
		var obj struct {
			Meta []struct {
				Name string `json:"name"`
			} `json:"meta"`
			Data [][]*string `json:"data"`
			Rows int         `json:"rows"`
		}
		if err := dec.Decode(&obj); err != nil {
			break
		}
		if len(obj.Meta) == 1 && obj.Meta[0].Name == "__pd_end" {
			res = append(res, cur)
			cur = Result{Affected: -1}
			continue
		}
		cur.Columns = make([]string, len(obj.Meta))
		for i, m := range obj.Meta {
			cur.Columns[i] = m.Name
		}
		for _, row := range obj.Data {
			for i, v := range row {
				if v != nil && *v == "ᴺᵁᴸᴸ" { // NULL of the Nothing type
					row[i] = nil
				}
			}
			addRow(&cur, row)
		}
	}
	return res
}

var pgPrefix = regexp.MustCompile(`(?m)^psql:<stdin>:\d+: `)

// CleanError shortens the error output of a client.
func CleanError(engine, stderr string) string {
	s := strings.TrimSpace(stderr)
	switch engine {
	case "postgres":
		s = pgPrefix.ReplaceAllString(s, "")
		var keep []string
		for _, l := range strings.Split(s, "\n") {
			if !strings.HasPrefix(l, "NOTICE:") {
				keep = append(keep, l)
			}
		}
		s = strings.TrimSpace(strings.Join(keep, "\n"))
	case "mysql", "mariadb":
		var keep []string
		// MariaDB repeats the failing statement between lines of dashes.
		quoted := false
		for _, l := range strings.Split(s, "\n") {
			if strings.TrimSpace(l) == "--------------" {
				quoted = !quoted
				continue
			}
			if !quoted && !strings.Contains(l, "Using a password on the command line") {
				keep = append(keep, l)
			}
		}
		s = strings.TrimSpace(strings.Join(keep, "\n"))
	case "clickhouse":
		if i := strings.Index(s, "Code: "); i > 0 {
			s = s[i:]
		}
		if i := strings.Index(s, ". (version"); i > 0 {
			s = s[:i]
		}
	}
	if len(s) > 4000 {
		s = s[:4000] + "…"
	}
	return s
}
