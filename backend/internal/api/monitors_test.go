package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMonitorsAndStatusPage(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer site.Close()
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("dev@corp.com", "correct-horse", "member")
	c := e.login("owner@corp.com")

	e.expect(e.login("dev@corp.com"), "POST", "/api/monitors", map[string]any{"name": "x", "url": site.URL}, 403)
	for _, bad := range []map[string]any{
		{"name": "", "url": site.URL},
		{"name": "x", "url": "ftp://example.com"},
		{"name": "x", "url": site.URL, "interval_seconds": 5},
		{"name": "x", "url": site.URL, "timeout_seconds": 90},
		{"name": "x", "url": site.URL, "expected_status": "abc"},
	} {
		e.expect(c, "POST", "/api/monitors", bad, 400)
	}
	m := e.expect(c, "POST", "/api/monitors", map[string]any{"name": "Shop", "url": site.URL, "expected_status": "200, 204"}, 200)
	if m["expected_status"] != "200,204" || m["interval_seconds"].(float64) != 60 || m["public"] != true {
		t.Fatalf("defaults: %v", m)
	}
	id := int64(m["id"].(float64))
	res := e.expect(c, "POST", fmt.Sprintf("/api/monitors/%d/check", id), nil, 200)
	if res["ok"] != true || res["status_code"].(float64) != 200 {
		t.Fatalf("check: %v", res)
	}
	d := e.expect(c, "GET", fmt.Sprintf("/api/monitors/%d", id), nil, 200)
	if d["monitor"].(map[string]any)["status"] != "up" || len(d["days"].([]any)) != 90 || len(d["checks"].([]any)) == 0 {
		t.Fatalf("detail: %v", d)
	}
	_, list := e.do(e.login("dev@corp.com"), "GET", "/api/monitors", nil)
	if !strings.Contains(list["_raw"].(string), `"name":"Shop"`) {
		t.Fatalf("members can see monitors: %v", list)
	}

	// Pausing keeps the history; a paused monitor is only tested, not recorded.
	p := e.expect(c, "PATCH", fmt.Sprintf("/api/monitors/%d", id), map[string]any{"enabled": false}, 200)
	if p["status"] != "paused" {
		t.Fatalf("paused: %v", p)
	}
	e.expect(c, "PATCH", fmt.Sprintf("/api/monitors/%d", id), map[string]any{"enabled": true}, 200)

	// The status page is off until enabled, and never exposes URLs.
	e.expect(e.client(), "GET", "/api/public/status", nil, 404)
	e.expect(c, "PATCH", "/api/settings", map[string]any{"status_page_enabled": "true", "status_page_title": "Acme status"}, 200)
	hidden := e.expect(c, "POST", "/api/monitors", map[string]any{"name": "Internal", "url": site.URL, "public": false}, 200)
	code, raw := e.do(e.client(), "GET", "/api/public/status", nil)
	if code != 200 || raw["title"] != "Acme status" || raw["status"] != "operational" {
		t.Fatalf("status page: %d %v", code, raw)
	}
	ms := raw["monitors"].([]any)
	if len(ms) != 1 || ms[0].(map[string]any)["name"] != "Shop" || len(ms[0].(map[string]any)["days"].([]any)) != 90 {
		t.Fatalf("public monitors: %v", ms)
	}
	body := fmt.Sprint(raw)
	if strings.Contains(body, site.URL) || strings.Contains(body, "127.0.0.1") {
		t.Fatalf("the status page leaks URLs: %s", body)
	}

	e.expect(c, "DELETE", fmt.Sprintf("/api/monitors/%v", hidden["id"]), nil, 200)
	e.expect(c, "DELETE", fmt.Sprintf("/api/monitors/%d", id), nil, 200)
	e.expect(c, "GET", fmt.Sprintf("/api/monitors/%d", id), nil, 404)
}
