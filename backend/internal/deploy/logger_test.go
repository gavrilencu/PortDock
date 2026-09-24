package deploy

import (
	"context"
	"strings"
	"testing"

	"github.com/portdock/portdock/internal/store"
	"github.com/portdock/portdock/internal/stream"
)

// Logs are appended to the database in increments and capped in size.
func TestLoggerAppendsAndCaps(t *testing.T) {
	e := testEngine(t)
	e.hub = stream.NewHub()
	ctx := context.Background()
	for _, q := range []string{
		"INSERT INTO projects(id, name, created_at) VALUES (1, 'p', '')",
		"INSERT INTO environments(id, project_id, name, created_at) VALUES (1, 1, 'production', '')",
		"INSERT INTO servers(id, name, host, status, is_local) VALUES (1, 'local', 'localhost', 'reachable', 1)",
		"INSERT INTO applications(id, uuid, environment_id, server_id, name) VALUES (1, 'pdlogtest', 1, 1, 'shop')",
		"INSERT INTO deployments(id, uuid, application_id, status, created_at) VALUES (7, 'd7', 1, 'running', '')",
	} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	l := newLogger(e, 7)
	l.Printf("first line")
	l.mu.Lock()
	l.flushLocked()
	l.mu.Unlock()
	logs := func() string {
		d, err := store.ByID[store.Deployment](ctx, e.db, "deployments", 7)
		if err != nil {
			t.Fatal(err)
		}
		return d.Logs
	}
	if got := logs(); !strings.Contains(got, "first line") {
		t.Fatalf("first flush: %q", got)
	}
	l.Printf("second line")
	l.mu.Lock()
	l.flushLocked()
	l.mu.Unlock()
	if got := logs(); strings.Count(got, "first line") != 1 || !strings.Contains(got, "second line") {
		t.Fatalf("second flush rewrote or lost lines: %q", got)
	}
	// Past the cap, lines are dropped after one note.
	big := strings.Repeat("x", 64<<10)
	for range (maxLogBytes / len(big)) + 2 {
		l.Printf("%s", big)
	}
	l.Printf("after the cap")
	l.Close()
	got := logs()
	if len(got) > maxLogBytes+(128<<10) || !strings.Contains(got, "not kept") || strings.Contains(got, "after the cap") {
		t.Fatalf("cap: len=%d truncated=%v", len(got), strings.Contains(got, "not kept"))
	}
}
