package store

import (
	"context"
	"path/filepath"
	"testing"
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
