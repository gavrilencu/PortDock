package uptime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/portdock/portdock/internal/store"
)

func TestStatusMatches(t *testing.T) {
	cases := []struct {
		expected string
		code     int
		want     bool
	}{
		{"", 200, true}, {"", 302, true}, {"", 404, false},
		{"200", 200, true}, {"200", 201, false},
		{"200,204", 204, true}, {"2xx", 299, true}, {"2xx", 301, false},
		{"2xx,301", 301, true}, {"400-499", 404, true}, {"400-499", 500, false},
	}
	for _, c := range cases {
		if got := StatusMatches(c.expected, c.code); got != c.want {
			t.Errorf("StatusMatches(%q, %d) = %v", c.expected, c.code, got)
		}
	}
	for _, bad := range []string{"abc", "300-200", "2x", "200,"} {
		if ValidStatus(bad) == nil {
			t.Errorf("ValidStatus(%q) accepted", bad)
		}
	}
	if ValidStatus("200-399,418,5xx") != nil {
		t.Error("valid list rejected")
	}
}

func TestCheckerStates(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			http.Error(w, "boom", 503)
			return
		}
		_, _ = w.Write([]byte("<h1>Shop is open</h1>"))
	}))
	defer srv.Close()

	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := New(db, nil, nil)
	ctx := context.Background()
	m := store.Monitor{Name: "shop", URL: srv.URL, IntervalSeconds: 60, TimeoutSeconds: 5, ExpectedStatus: "200-399", Keyword: "open",
		FailuresToAlert: 2, Enabled: true, Public: true, Status: "pending", CreatedAt: store.Now()}
	if _, err := store.Insert(ctx, db, "monitors", &m); err != nil {
		t.Fatal(err)
	}
	load := func() store.Monitor {
		m, _ := store.ByID[store.Monitor](ctx, db, "monitors", m.ID)
		return m
	}
	check := func() Result { return c.CheckNow(ctx, load()) }

	if r := check(); !r.OK || load().Status != "up" {
		t.Fatalf("first check: %+v %+v", r, load())
	}
	healthy.Store(false)
	if r := check(); r.OK || r.Error != "HTTP 503 Service Unavailable" || load().Status != "up" {
		t.Fatalf("one failure must not mark the monitor down: %+v %s", r, load().Status)
	}
	check()
	if got := load(); got.Status != "down" || got.ConsecutiveFailures != 2 {
		t.Fatalf("two failures: %+v", got)
	}
	var open int
	_ = db.QueryRow("SELECT COUNT(*) FROM monitor_incidents WHERE resolved_at = ''").Scan(&open)
	if open != 1 {
		t.Fatalf("open incidents: %d", open)
	}
	check() // still down: no second incident
	healthy.Store(true)
	check()
	_ = db.QueryRow("SELECT COUNT(*) FROM monitor_incidents WHERE resolved_at = ''").Scan(&open)
	if got := load(); got.Status != "up" || got.ConsecutiveFailures != 0 || open != 0 {
		t.Fatalf("recovery: %+v, open incidents %d", got, open)
	}

	// The keyword must be in the body.
	m2 := load()
	m2.Keyword = "closed"
	if r := Probe(ctx, m2); r.OK || r.Error == "" {
		t.Fatalf("keyword: %+v", r)
	}

	days := Days(ctx, db, m.ID, 90)
	if len(days) != 90 || days[89].Checks != 5 || days[89].Failures != 3 || days[0].Uptime != nil {
		t.Fatalf("days: %+v", days[89])
	}
	if u := Uptime(days); u == nil || *u != 40 {
		t.Fatalf("uptime: %v", u)
	}
	if u := Uptime24h(ctx, db, m.ID); u == nil || *u != 40 {
		t.Fatalf("uptime 24h: %v", u)
	}

	// Unreachable hosts fail with a readable message.
	m3 := load()
	m3.URL = "http://127.0.0.1:1"
	if r := Probe(ctx, m3); r.OK || r.Error == "" {
		t.Fatalf("unreachable: %+v", r)
	}
}
