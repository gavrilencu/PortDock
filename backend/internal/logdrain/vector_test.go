package logdrain

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func sampleTargets() []Target {
	return []Target{
		{ID: 1, Type: "loki", URL: "http://loki:3100/", Config: Config{Username: "u", Password: "pa$$:word'\"", Tenant: "team-a"}},
		{ID: 2, Type: "elasticsearch", URL: "https://es.corp:9200", Config: Config{Token: "a2V5", Index: "apps-%Y.%m", SkipVerify: true}, Projects: []int64{3, 4}},
		{ID: 3, Type: "gelf", URL: "udp://graylog:12201"},
		{ID: 4, Type: "gelf", URL: "tcp://graylog"},
		{ID: 5, Type: "splunk", URL: "https://splunk:8088", Config: Config{Token: "hec", Index: "main", SourceType: "docker"}},
		{ID: 6, Type: "syslog", URL: "tls://siem.corp", IncludeProxy: true},
		{ID: 7, Type: "http", URL: "https://logs.corp/ingest", Config: Config{Token: "t", Headers: map[string]string{"X-Team": "ops"}}},
	}
}

func sampleResources() []Resource {
	return []Resource{
		{UUID: "b2", Kind: "database", Name: "db", Project: "shop", Environment: "production", ProjectID: 3},
		{UUID: "a1", Kind: "application", Name: "web, \"api\"\n", Project: "shop", Environment: "staging", ProjectID: 3},
	}
}

func TestRender(t *testing.T) {
	f, err := Render(sampleTargets(), sampleResources())
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(f.Vector, &raw); err != nil {
		t.Fatalf("yaml: %v\n%s", err, f.Vector)
	}
	doc := map[string]map[string]any{}
	for _, k := range []string{"sinks", "transforms"} {
		doc[k] = raw[k].(map[string]any)
	}
	sinks := doc["sinks"]
	for _, id := range []string{"drain_1", "drain_2", "drain_3", "drain_4", "drain_5", "drain_6", "drain_7", "portdock_metrics"} {
		if sinks[id] == nil {
			t.Errorf("missing sink %s", id)
		}
	}
	loki := sinks["drain_1"].(map[string]any)
	if loki["endpoint"] != "http://loki:3100" || loki["tenant_id"] != "team-a" || loki["auth"].(map[string]any)["password"] != "pa$$:word'\"" {
		t.Errorf("loki: %v", loki)
	}
	if loki["inputs"].([]any)[0] != "drain_1_filter" {
		t.Errorf("proxy logs are not filtered: %v", loki["inputs"])
	}
	es := sinks["drain_2"].(map[string]any)
	if es["request"].(map[string]any)["headers"].(map[string]any)["Authorization"] != "ApiKey a2V5" || es["tls"] == nil {
		t.Errorf("elasticsearch: %v", es)
	}
	cond := doc["transforms"]["drain_2_filter"].(map[string]any)["condition"].(string)
	if !strings.Contains(cond, `includes(["3", "4"], .project_id)`) {
		t.Errorf("project filter: %s", cond)
	}
	if doc["transforms"]["drain_6_filter"] != nil {
		t.Error("a destination with the proxy logs and every project needs no filter")
	}
	if g := sinks["drain_4"].(map[string]any); g["address"] != "graylog:12201" || g["mode"] != "tcp" {
		t.Errorf("gelf tcp: %v", g)
	}
	if s := sinks["drain_6"].(map[string]any); s["address"] != "siem.corp:6514" || s["tls"] == nil {
		t.Errorf("syslog tls: %v", s)
	}
	if !strings.HasPrefix(f.TableName, "resources-") || !strings.Contains(string(f.Table), "a1,application,\"web, \"\"api\"\"\",shop,staging,3\n") {
		t.Errorf("table %s:\n%s", f.TableName, f.Table)
	}
	g, _ := Render(sampleTargets(), []Resource{sampleResources()[1], sampleResources()[0]})
	if g.Fingerprint() != f.Fingerprint() {
		t.Error("the order of the resources changes the files")
	}
}

func TestValidate(t *testing.T) {
	for _, c := range []struct {
		typ, url string
		cfg      Config
		ok       bool
	}{
		{"loki", "https://loki.corp", Config{}, true},
		{"loki", "ftp://loki", Config{}, false},
		{"loki", "https://user:pw@loki", Config{}, false},
		{"gelf", "udp://graylog:12201", Config{}, true},
		{"syslog", "tls://siem:6514", Config{}, true},
		{"syslog", "https://siem", Config{}, false},
		{"syslog", "udp://siem:99999", Config{}, false},
		{"splunk", "https://splunk:8088", Config{}, false},
		{"elasticsearch", "http://es:9200", Config{Index: "Logs"}, false},
		{"http", "https://x", Config{Headers: map[string]string{"Bad Header": "v"}}, false},
		{"http", "https://x", Config{Token: "a\nb"}, false},
		{"kafka", "https://x", Config{}, false},
	} {
		if err := Validate(c.typ, c.url, c.cfg); (err == nil) != c.ok {
			t.Errorf("%s %s: %v", c.typ, c.url, err)
		}
	}
}

func TestParseStats(t *testing.T) {
	s := ParseStats(`# HELP vector_component_sent_events_total x
vector_component_sent_events_total{component_id="drain_1",component_kind="sink",host="h"} 42 1700000000000
vector_component_sent_events_total{component_id="portdock_enrich",component_kind="transform"} 99
vector_component_discarded_events_total{component_id="drain_1_filter",component_kind="transform"} 7
vector_component_errors_total{component_id="drain_1",error_type="request_failed",stage="sending"} 2
vector_buffer_discarded_events_total{component_id="drain_1",intentional="false"} 5
`)
	if got := s["drain_1"]; got.Sent != 42 || got.Errors != 2 || got.Discarded != 5 || len(s) != 1 {
		t.Errorf("stats: %+v", s)
	}
}

// TestRenderVectorValidate checks the configuration with Vector itself.
func TestRenderVectorValidate(t *testing.T) {
	if os.Getenv("PORTDOCK_DOCKER_E2E") != "1" {
		t.Skip("set PORTDOCK_DOCKER_E2E=1")
	}
	f, err := Render(sampleTargets(), sampleResources())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "vector.yaml"), f.Vector, 0o644)
	_ = os.WriteFile(filepath.Join(dir, f.TableName), f.Table, 0o644)
	out, err := exec.Command("docker", "run", "--rm", "-v", filepath.ToSlash(dir)+":/etc/vector:ro", Image,
		"validate", "--no-environment", "/etc/vector/vector.yaml").CombinedOutput()
	if err != nil {
		t.Fatalf("vector validate: %v\n%s\n%s", err, out, f.Vector)
	}
}
