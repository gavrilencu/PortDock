package deploy

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/portdock/portdock/internal/config"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
)

func TestOTelEnv(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	box, _ := secrets.LoadOrCreate("", dir)
	e := New(config.Config{}, db, box, nil, nil, nil, nil)
	ctx := t.Context()
	for _, q := range []string{
		"INSERT INTO projects(id, name) VALUES (1, 'shop, eu')",
		"INSERT INTO environments(id, project_id, name) VALUES (2, 1, 'staging')",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	app := store.Application{UUID: "abc123", Name: "web", EnvironmentID: 2}
	d := &store.Deployment{CommitSHA: "0123456789abcdef0123", PRNumber: 7}
	none := func(string) bool { return false }

	if v := e.otelEnv(ctx, app, d, "", none); v != nil {
		t.Fatalf("no collector configured, got %v", v)
	}
	_ = db.SetSetting(ctx, "otel_endpoint", "http://otel-collector:4318")
	_ = db.SetSetting(ctx, "otel_headers", box.Encrypt("authorization=Bearer t0k"))
	v := e.otelEnv(ctx, app, d, "", none)
	want := map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://otel-collector:4318",
		"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
		"OTEL_EXPORTER_OTLP_HEADERS":  "authorization=Bearer t0k",
		"OTEL_SERVICE_NAME":           "web",
		"OTEL_RESOURCE_ATTRIBUTES": "service.namespace=shop%2C%20eu,deployment.environment.name=staging,deployment.environment=staging," +
			"portdock.application=abc123,service.version=0123456789ab,portdock.pull_request=7",
	}
	for k, w := range want {
		if v[k] != w {
			t.Errorf("%s = %q, want %q", k, v[k], w)
		}
	}
	if v := e.otelEnv(ctx, app, d, "worker", none); v["OTEL_SERVICE_NAME"] != "web-worker" {
		t.Errorf("compose service name: %v", v["OTEL_SERVICE_NAME"])
	}
	// The application's own values win; its own endpoint or disabling the SDK
	// turns the injection off (the organization's headers stay private).
	if v := e.otelEnv(ctx, app, d, "", func(k string) bool { return k == "OTEL_SERVICE_NAME" }); v["OTEL_SERVICE_NAME"] != "" || v["OTEL_EXPORTER_OTLP_ENDPOINT"] == "" {
		t.Errorf("own service name: %v", v)
	}
	for _, k := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_SDK_DISABLED"} {
		if v := e.otelEnv(ctx, app, d, "", func(s string) bool { return s == k }); v != nil {
			t.Errorf("%s set: got %v", k, v)
		}
	}
}

func TestComposeEnvKeys(t *testing.T) {
	var spec composeSpec
	raw := `services:
  web:
    image: nginx
    environment:
      OTEL_SERVICE_NAME: site
      PORT: 80
  worker:
    image: app
    environment:
      - OTEL_SDK_DISABLED=true
      - EMPTY
  db:
    image: postgres
`
	if err := yaml.Unmarshal([]byte(raw), &spec); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for n, node := range spec.Services {
		got[n] = strings.Join(slices.Sorted(maps.Keys(composeEnvKeys(node))), ",")
	}
	if got["web"] != "OTEL_SERVICE_NAME,PORT" || got["worker"] != "EMPTY,OTEL_SDK_DISABLED" || got["db"] != "" {
		t.Errorf("keys: %v", got)
	}
}
