package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestMapperRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	p := Project{Name: "demo", CreatedAt: Now()}
	if _, err := Insert(ctx, db, "projects", &p); err != nil || p.ID == 0 {
		t.Fatalf("insert: %v id=%d", err, p.ID)
	}
	p.Description = "updated"
	if err := Update(ctx, db, "projects", &p); err != nil {
		t.Fatal(err)
	}
	got, err := ByID[Project](ctx, db, "projects", p.ID)
	if err != nil || got.Description != "updated" {
		t.Fatalf("get: %v %+v", err, got)
	}

	srv := Server{Name: "s", IsLocal: true, ProxyEnabled: true, CreatedAt: Now()}
	if _, err := Insert(ctx, db, "servers", &srv); err != nil {
		t.Fatal(err)
	}
	s2, _ := ByID[Server](ctx, db, "servers", srv.ID)
	if !s2.IsLocal || !s2.ProxyEnabled {
		t.Error("bools not persisted")
	}

	// Embedded structs + extra columns.
	type row struct {
		Project
		Extra string `db:"extra"`
	}
	rows, err := Select[row](ctx, db, "SELECT *, 'x' AS extra FROM projects")
	if err != nil || len(rows) != 1 || rows[0].Name != "demo" || rows[0].Extra != "x" {
		t.Fatalf("embedded select: %v %+v", err, rows)
	}

	if _, err := ByID[Project](ctx, db, "projects", 999); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// A query issued while another cursor is still open must not wait for it:
// with a single connection that was a deadlock of the whole process.
func TestReadsDoNotBlockOnOpenCursor(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := range 3 {
		if _, err := Insert(ctx, db, "projects", &Project{Name: "p" + string(rune('a'+i)), CreatedAt: Now()}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.QueryContext(ctx, "SELECT id FROM projects")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	done := make(chan error, 1)
	go func() {
		var n int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects").Scan(&n); err != nil {
			done <- err
			return
		}
		_, err := db.ExecContext(ctx, "INSERT INTO projects(name, created_at) VALUES ('w', '')")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a query blocked behind an open cursor")
	}
	// The open cursor still works and the write is visible to a fresh read.
	n := 0
	for rows.Next() {
		n++
	}
	if n != 3 {
		t.Fatalf("cursor rows: %d", n)
	}
	var total int
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects").Scan(&total)
	if total != 4 {
		t.Fatalf("read after write: %d", total)
	}
	for _, idx := range []string{"idx_metrics_hourly_ts", "idx_app_domains_app", "idx_deployments_status", "idx_monitor_checks_recent"} {
		var k int
		_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?", idx).Scan(&k)
		if k != 1 {
			t.Errorf("index %s missing", idx)
		}
	}
}
