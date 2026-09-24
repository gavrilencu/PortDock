package deploy

import (
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
