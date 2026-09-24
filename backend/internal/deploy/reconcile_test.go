package deploy

import (
	"context"
	"testing"
	"time"

	"github.com/portdock/portdock/internal/store"
)

// Unreachable servers are retried with a growing wait, reachable ones every tick.
func TestReconcileBackoff(t *testing.T) {
	e := testEngine(t)
	srv := store.Server{ID: 5}
	if !e.reconcileDue(srv) {
		t.Fatal("a fresh server should be due")
	}
	e.reconcileNote(5, false)
	if e.reconcileDue(srv) {
		t.Fatal("due right after a failure")
	}
	waits := []time.Duration{}
	for i := range 6 {
		e.reconcileNote(5, false)
		e.recon.Lock()
		waits = append(waits, time.Until(e.recon.next[5]).Round(time.Second))
		e.recon.Unlock()
		if i > 0 && waits[i] < waits[i-1] {
			t.Fatalf("wait shrank: %v", waits)
		}
	}
	if waits[len(waits)-1] > 10*time.Minute {
		t.Fatalf("wait above the cap: %v", waits)
	}
	e.reconcileNote(5, true)
	if !e.reconcileDue(srv) {
		t.Fatal("not due after a success")
	}
	if !e.proxyCheckDue(5) || e.proxyCheckDue(5) {
		t.Fatal("the proxy check should run once per interval")
	}
}

// Applications left "deploying" by a restart are marked failed at start.
func TestStartResetsStuckApplications(t *testing.T) {
	e := testEngine(t)
	for _, q := range []string{
		"INSERT INTO projects(id, name, created_at) VALUES (1, 'p', '')",
		"INSERT INTO environments(id, project_id, name, created_at) VALUES (1, 1, 'production', '')",
		"INSERT INTO servers(id, name, host, status, is_local) VALUES (1, 'local', 'localhost', 'reachable', 1)",
		"INSERT INTO applications(id, uuid, environment_id, server_id, name, status) VALUES (1, 'a1', 1, 1, 'stuck', 'deploying')",
		"INSERT INTO applications(id, uuid, environment_id, server_id, name, status) VALUES (2, 'a2', 1, 1, 'queued-for-real', 'queued')",
		"INSERT INTO applications(id, uuid, environment_id, server_id, name, status) VALUES (3, 'a3', 1, 1, 'fine', 'running')",
		"INSERT INTO deployments(id, uuid, application_id, status, created_at) VALUES (1, 'd1', 1, 'running', '')",
		"INSERT INTO deployments(id, uuid, application_id, status, created_at) VALUES (2, 'd2', 2, 'queued', '')",
	} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	e.cfg.MaxConcurrentBuilds = 0 // no workers: the queued deployment stays queued
	e.Start(context.Background())
	want := map[int64]string{1: "failed", 2: "queued", 3: "running"}
	for id, status := range want {
		var got string
		_ = e.db.QueryRow("SELECT status FROM applications WHERE id = ?", id).Scan(&got)
		if got != status {
			t.Errorf("application %d: %s, want %s", id, got, status)
		}
	}
}
