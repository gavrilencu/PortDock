package deploy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/sqlconsole"
	"github.com/portdock/portdock/internal/store"
)

// QueryOutput is the outcome of a script run in the SQL console.
type QueryOutput struct {
	Results    []sqlconsole.Result `json:"results"`
	Error      string              `json:"error"` // the statement after the last result failed
	ReadOnly   bool                `json:"read_only"`
	DurationMs int64               `json:"duration_ms"`
	// OutputTruncated: the client printed more than PortDock reads; later results are missing.
	OutputTruncated bool `json:"output_truncated"`
}

// InvalidQuery is a script the console refuses to run.
type InvalidQuery struct{ Err error }

func (e *InvalidQuery) Error() string { return e.Err.Error() }

const (
	maxQueryOutput = 32 << 20
	maxQueryErrors = 1 << 20
)

// capped keeps the first n bytes written to it and discards the rest, so a
// huge result can't exhaust memory; the client keeps writing without blocking.
type capped struct {
	b    strings.Builder
	n    int
	over bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.n - c.b.Len(); room > 0 {
		if len(p) > room {
			c.b.Write(p[:room])
			c.over = true
		} else {
			c.b.Write(p)
		}
	} else if len(p) > 0 {
		c.over = true
	}
	return len(p), nil
}

// RunQuery runs a script with the database's command-line client inside its
// container, read-only unless readOnly is false.
func (e *Engine) RunQuery(ctx context.Context, db store.Database, script string, readOnly bool) (QueryOutput, error) {
	o := QueryOutput{ReadOnly: readOnly, Results: []sqlconsole.Result{}}
	if !sqlconsole.Supported(db.Engine) {
		return o, &InvalidQuery{errors.New("the SQL console is not available for this database engine")}
	}
	stmts, err := sqlconsole.Split(db.Engine, script)
	if err != nil {
		return o, &InvalidQuery{err}
	}
	if len(stmts) > 100 {
		return o, &InvalidQuery{errors.New("run at most 100 statements at a time")}
	}
	if readOnly {
		if err := sqlconsole.CheckReadOnly(db.Engine, stmts); err != nil {
			return o, &InvalidQuery{err}
		}
	}
	x, _, err := e.pool.ForID(ctx, db.ServerID)
	if err != nil {
		return o, err
	}
	if state, _, err := docker.State(ctx, x, db.UUID); err != nil || state != "running" {
		return o, &InvalidQuery{errors.New("the database is not running; start it to run queries")}
	}
	user := db.Username
	if db.Engine == "mssql" {
		user = "sa"
	}
	cmd, in := sqlconsole.Command(db.Engine, user, e.dbPassword(db), db.DatabaseName, stmts, readOnly)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(sqlconsole.Timeout+30)*time.Second)
	defer cancel()
	out, errOut := &capped{n: maxQueryOutput}, &capped{n: maxQueryErrors}
	start := time.Now()
	runErr := x.Run(ctx, "docker exec -i "+q(db.UUID)+" sh -c "+q(cmd), executor.RunOpts{Stdin: strings.NewReader(in), Stdout: out, Stderr: errOut})
	o.DurationMs = time.Since(start).Milliseconds()
	o.Results = sqlconsole.Parse(db.Engine, out.b.String(), stmts)
	o.OutputTruncated = out.over
	if runErr != nil {
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			o.Error = fmt.Sprintf("the query was stopped after %d seconds", sqlconsole.Timeout+30)
		default:
			o.Error = sqlconsole.CleanError(db.Engine, errOut.b.String())
		}
		if o.Error == "" {
			o.Error = runErr.Error()
		}
	}
	// The client ended without the output of every statement and without an
	// error: show what it printed instead of an empty result.
	if o.Error == "" && len(o.Results) < len(stmts) && !o.OutputTruncated {
		o.Error = fmt.Sprintf("the database client returned no result for statement %d", len(o.Results)+1)
		if detail := strings.TrimSpace(sqlconsole.CleanError(db.Engine, errOut.b.String()+"\n"+tail(out.b.String(), 2000))); detail != "" {
			o.Error += ":\n" + detail
		}
	}
	return o, nil
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// DatabaseTables lists the tables and views of a database (read-only).
func (e *Engine) DatabaseTables(ctx context.Context, db store.Database) (sqlconsole.Result, error) {
	o, err := e.RunQuery(ctx, db, sqlconsole.TablesQuery(db.Engine), true)
	if err != nil {
		return sqlconsole.Result{}, err
	}
	if o.Error != "" {
		return sqlconsole.Result{}, errors.New(o.Error)
	}
	if len(o.Results) == 0 {
		return sqlconsole.Result{Columns: []string{}, Rows: [][]*string{}}, nil
	}
	return o.Results[0], nil
}
