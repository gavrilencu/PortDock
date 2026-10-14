package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/portdock/portdock/internal/store"
)

func TestLogDrains(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("dev@corp.com", "correct-horse", "member")
	owner, dev := e.login("owner@corp.com"), e.login("dev@corp.com")
	p := e.expect(owner, "POST", "/api/projects", map[string]any{"name": "shop"}, 200)

	e.expect(dev, "GET", "/api/log-drains", nil, 403)
	e.expect(dev, "POST", "/api/log-drains", map[string]any{"name": "x", "type": "loki", "url": "http://loki:3100"}, 403)

	for _, bad := range []map[string]any{
		{"name": "x", "type": "kafka", "url": "http://k"},
		{"name": "x", "type": "loki", "url": "loki:3100"},
		{"name": "", "type": "loki", "url": "http://loki:3100"},
		{"name": "x", "type": "syslog", "url": "http://siem"},
		{"name": "x", "type": "splunk", "url": "https://splunk:8088"},
		{"name": "x", "type": "loki", "url": "http://loki:3100", "project_ids": []int64{999}},
	} {
		e.expect(owner, "POST", "/api/log-drains", bad, 400)
	}

	loki := e.expect(owner, "POST", "/api/log-drains", map[string]any{
		"name": "Grafana Loki", "type": "loki", "url": "http://loki:3100",
		"username": "tenant", "password": "s3cret-pass", "tenant": "team-a", "project_ids": []any{p["id"]},
	}, 200)
	raw, _ := json.Marshal(loki)
	if strings.Contains(string(raw), "s3cret-pass") || loki["password_set"] != true || loki["username"] != "tenant" || loki["tenant"] != "team-a" {
		t.Fatalf("drain output: %s", raw)
	}
	if ids := loki["project_ids"].([]any); len(ids) != 1 || ids[0] != p["id"] {
		t.Errorf("projects: %v", ids)
	}
	id := int64(loki["id"].(float64))
	stored, _ := store.ByID[store.LogDrain](t.Context(), e.db, "log_drains", id)
	if strings.Contains(stored.Config, "s3cret") {
		t.Fatal("the configuration is stored in clear")
	}

	// Updating without the password keeps it; clearing the user drops it.
	e.expect(owner, "PATCH", fmt.Sprintf("/api/log-drains/%d", id), map[string]any{"name": "Loki", "password": ""}, 200)
	stored, _ = store.ByID[store.LogDrain](t.Context(), e.db, "log_drains", id)
	if c, _ := e.api.drains.ConfigOf(stored); c.Password != "s3cret-pass" || stored.Name != "Loki" {
		t.Errorf("after update: %+v %+v", stored, c)
	}
	out := e.expect(owner, "PATCH", fmt.Sprintf("/api/log-drains/%d", id), map[string]any{"username": "", "token": "bearer-1", "project_ids": []int64{}}, 200)
	stored, _ = store.ByID[store.LogDrain](t.Context(), e.db, "log_drains", id)
	if c, _ := e.api.drains.ConfigOf(stored); c.Password != "" || c.Token != "bearer-1" || out["token_set"] != true || len(out["project_ids"].([]any)) != 0 {
		t.Errorf("switch to a token: %+v %v", c, out)
	}

	// HTTP headers: names only in the output, an empty value keeps the old one.
	h := e.expect(owner, "POST", "/api/log-drains", map[string]any{"name": "Ingest", "type": "http", "url": "https://logs.corp/in", "headers": map[string]string{"X-Api-Key": "k-123"}}, 200)
	if names := h["header_names"].([]any); len(names) != 1 || names[0] != "X-Api-Key" || strings.Contains(fmt.Sprint(h), "k-123") {
		t.Errorf("headers output: %v", h)
	}
	hid := int64(h["id"].(float64))
	e.expect(owner, "PATCH", fmt.Sprintf("/api/log-drains/%d", hid), map[string]any{"headers": map[string]string{"X-Api-Key": "", "X-Team": "ops"}}, 200)
	hs, _ := store.ByID[store.LogDrain](t.Context(), e.db, "log_drains", hid)
	if c, _ := e.api.drains.ConfigOf(hs); c.Headers["X-Api-Key"] != "k-123" || c.Headers["X-Team"] != "ops" {
		t.Errorf("headers after update: %v", c.Headers)
	}

	list := e.expectList(owner, "GET", "/api/log-drains")
	if len(list) != 2 {
		t.Fatalf("list: %v", list)
	}
	targets, err := e.api.drains.Targets(t.Context())
	if err != nil || len(targets) != 2 {
		t.Fatalf("targets: %v %v", targets, err)
	}
	e.expect(owner, "PATCH", fmt.Sprintf("/api/log-drains/%d", hid), map[string]any{"enabled": false}, 200)
	if targets, _ = e.api.drains.Targets(t.Context()); len(targets) != 1 {
		t.Errorf("a disabled destination is shipped to: %v", targets)
	}
	e.expect(owner, "DELETE", fmt.Sprintf("/api/log-drains/%d", hid), nil, 200)
	if len(e.expectList(owner, "GET", "/api/log-drains")) != 1 {
		t.Error("delete")
	}

	// The OpenTelemetry headers are a secret setting.
	e.expect(owner, "PATCH", "/api/settings", map[string]any{"otel_endpoint": "http://collector:4318", "otel_headers": "api-key=xyz"}, 200)
	st := e.expect(owner, "GET", "/api/settings", nil, 200)
	if st["otel_endpoint"] != "http://collector:4318" || st["otel_headers_set"] != true || st["otel_headers"] != nil {
		t.Errorf("settings: %v", st)
	}
}
