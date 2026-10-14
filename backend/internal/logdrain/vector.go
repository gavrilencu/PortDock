package logdrain

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Resource is an application, database or service whose containers log.
type Resource struct {
	UUID, Kind, Name, Project, Environment string
	ProjectID                              int64
}

// Files is the configuration of the agent: vector.yaml and the table of the
// resources it reads to label each line.
type Files struct {
	Vector    []byte
	TableName string // resources-<hash>.csv: a new name makes Vector reload it
	Table     []byte
}

// Fingerprint identifies the files.
func (f Files) Fingerprint() string {
	h := sha256.Sum256(append(append([]byte{}, f.Vector...), f.Table...))
	return hex.EncodeToString(h[:8])
}

const (
	confDir = "/etc/vector"
	// MetricsAddress is where the agent reports what it sent (inside its container).
	MetricsAddress = "127.0.0.1:9598"
	// bufferBytes is the disk buffer of each destination: lines wait there
	// while it is unreachable, then the newest are dropped.
	bufferBytes = 256<<20 + 32 // the smallest disk buffer Vector accepts
)

// enrichVRL turns a Docker log event into a PortDock one.
const enrichVRL = `uuid = to_string(.label."portdock.resource") ?? ""
kind = to_string(.label."portdock.kind") ?? ""
r = {}
if uuid != "" {
  r = get_enrichment_table_record("portdock_resources", {"uuid": uuid}) ?? {}
}
.kind = kind
.resource_uuid = uuid
.app = to_string(r.name) ?? ""
.project = to_string(r.project) ?? ""
.project_id = to_string(r.project_id) ?? ""
.environment = to_string(r.environment) ?? ""
.deployment = to_string(.label."portdock.deployment") ?? ""
.server = get_env_var("PORTDOCK_SERVER") ?? ""
.host = .server
.container = to_string(.container_name) ?? ""
if kind == "proxy" { .app = "proxy" }
if kind == "test" { .app = "portdock-test" }
del(.label)
del(.container_name)
del(.container_created_at)
del(.source_type)
`

// gelfVRL keeps only flat values, as GELF expects, and sets the level.
const gelfVRL = `.level = if .stream == "stderr" { 3 } else { 6 }
`

// syslogVRL renders RFC 5424 lines: the application is the APP-NAME, the
// kind of resource the MSGID, and the project, environment and server go in
// structured data.
const syslogVRL = `pri = if .stream == "stderr" { "<11>" } else { "<14>" }
ts = format_timestamp(.timestamp, format: "%Y-%m-%dT%H:%M:%S%.6fZ", timezone: "UTC") ?? "-"
host = replace(to_string(.server) ?? "", r'[^!-~]', "_")
if host == "" { host = "-" }
app = truncate(replace(to_string(.app) ?? "", r'[^!-~]', "_"), 48)
if app == "" { app = "-" }
msgid = truncate(replace(to_string(.kind) ?? "", r'[^!-~]', "_"), 32)
if msgid == "" { msgid = "-" }
project = replace(replace(replace(to_string(.project) ?? "", "\\", "\\\\"), "\"", "\\\""), "]", "\\]")
environment = replace(replace(replace(to_string(.environment) ?? "", "\\", "\\\\"), "\"", "\\\""), "]", "\\]")
server = replace(replace(replace(to_string(.server) ?? "", "\\", "\\\\"), "\"", "\\\""), "]", "\\]")
sd = "[portdock@32473 project=\"" + project + "\" environment=\"" + environment + "\" server=\"" + server + "\"]"
.message = pri + "1 " + ts + " " + host + " " + app + " - " + msgid + " " + sd + " " + (to_string(.message) ?? "")
`

// Render builds the agent's configuration for these destinations.
func Render(targets []Target, resources []Resource) (Files, error) {
	var table bytes.Buffer
	w := csv.NewWriter(&table)
	_ = w.Write([]string{"uuid", "kind", "name", "project", "environment", "project_id"})
	sorted := slices.Clone(resources)
	slices.SortFunc(sorted, func(a, b Resource) int { return strings.Compare(a.UUID, b.UUID) })
	for _, r := range sorted {
		_ = w.Write([]string{r.UUID, r.Kind, clean(r.Name), clean(r.Project), clean(r.Environment), strconv.FormatInt(r.ProjectID, 10)})
	}
	w.Flush()
	sum := sha256.Sum256(table.Bytes())
	tableName := "resources-" + hex.EncodeToString(sum[:6]) + ".csv"

	sources := map[string]any{
		"portdock_containers": map[string]any{
			"type":               "docker_logs",
			"include_labels":     []string{"portdock.managed=true"},
			"exclude_containers": []string{ContainerName},
		},
		"portdock_internal": map[string]any{"type": "internal_metrics", "scrape_interval_secs": 15},
	}
	transforms := map[string]any{
		"portdock_enrich": map[string]any{"type": "remap", "inputs": []string{"portdock_containers"}, "source": enrichVRL},
	}
	sinks := map[string]any{
		"portdock_metrics": map[string]any{"type": "prometheus_exporter", "inputs": []string{"portdock_internal"}, "address": MetricsAddress},
	}
	for _, t := range targets {
		id := ComponentID(t.ID)
		input := "portdock_enrich"
		var cond []string
		if !t.IncludeProxy {
			cond = append(cond, `.kind != "proxy"`)
		}
		if len(t.Projects) > 0 {
			ids := make([]string, len(t.Projects))
			for i, p := range t.Projects {
				ids[i] = strconv.Quote(strconv.FormatInt(p, 10))
			}
			cond = append(cond, `(.kind == "test" || includes([`+strings.Join(ids, ", ")+`], .project_id))`)
		}
		if len(cond) > 0 {
			transforms[id+"_filter"] = map[string]any{"type": "filter", "inputs": []string{input}, "condition": strings.Join(cond, " && ")}
			input = id + "_filter"
		}
		switch t.Type {
		case "gelf":
			transforms[id+"_format"] = map[string]any{"type": "remap", "inputs": []string{input}, "source": gelfVRL}
			input = id + "_format"
		case "syslog":
			transforms[id+"_format"] = map[string]any{"type": "remap", "inputs": []string{input}, "source": syslogVRL}
			input = id + "_format"
		}
		sink, err := sinkFor(t)
		if err != nil {
			return Files{}, fmt.Errorf("destination %d: %w", t.ID, err)
		}
		sink["inputs"] = []string{input}
		sink["buffer"] = map[string]any{"type": "disk", "max_size": bufferBytes, "when_full": "drop_newest"}
		sinks[id] = sink
	}
	doc := map[string]any{
		"data_dir": "/var/lib/vector",
		"enrichment_tables": map[string]any{
			"portdock_resources": map[string]any{
				"type": "file",
				"file": map[string]any{"path": confDir + "/" + tableName, "encoding": map[string]any{"type": "csv"}},
			},
		},
		"sources":    sources,
		"transforms": transforms,
		"sinks":      sinks,
	}
	var out bytes.Buffer
	out.WriteString("# Generated by PortDock: changes are overwritten.\n")
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return Files{}, err
	}
	return Files{Vector: out.Bytes(), TableName: tableName, Table: table.Bytes()}, nil
}

// ComponentID names the Vector sink of a destination.
func ComponentID(id int64) string { return "drain_" + strconv.FormatInt(id, 10) }

// clean drops control characters from names shown in the logs.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

func sinkFor(t Target) (map[string]any, error) {
	c := t.Config
	tls := func(s map[string]any) {
		if c.SkipVerify {
			s["tls"] = map[string]any{"verify_certificate": false, "verify_hostname": false}
		}
	}
	basicOrBearer := func(s map[string]any) {
		switch {
		case c.Username != "":
			s["auth"] = map[string]any{"strategy": "basic", "user": c.Username, "password": c.Password}
		case c.Token != "":
			s["auth"] = map[string]any{"strategy": "bearer", "token": c.Token}
		}
	}
	switch t.Type {
	case "loki":
		s := map[string]any{
			"type":     "loki",
			"endpoint": strings.TrimRight(t.URL, "/"),
			"encoding": map[string]any{"codec": "json"},
			"labels": map[string]string{
				"source": "portdock", "project": "{{ project }}", "environment": "{{ environment }}",
				"app": "{{ app }}", "kind": "{{ kind }}", "server": "{{ server }}", "stream": "{{ stream }}",
			},
			"out_of_order_action": "accept",
		}
		if c.Tenant != "" {
			s["tenant_id"] = c.Tenant
		}
		basicOrBearer(s)
		tls(s)
		return s, nil
	case "elasticsearch":
		index := c.Index
		if index == "" {
			index = "portdock-%Y.%m.%d"
		}
		s := map[string]any{
			"type":        "elasticsearch",
			"endpoints":   []string{strings.TrimRight(t.URL, "/")},
			"mode":        "bulk",
			"bulk":        map[string]any{"index": index},
			"api_version": "auto",
		}
		switch {
		case c.Username != "":
			s["auth"] = map[string]any{"strategy": "basic", "user": c.Username, "password": c.Password}
		case c.Token != "":
			s["request"] = map[string]any{"headers": map[string]string{"Authorization": "ApiKey " + c.Token}}
		}
		tls(s)
		return s, nil
	case "splunk":
		s := map[string]any{
			"type":          "splunk_hec_logs",
			"endpoint":      strings.TrimRight(t.URL, "/"),
			"default_token": c.Token,
			"encoding":      map[string]any{"codec": "json"},
		}
		if c.Index != "" {
			s["index"] = c.Index
		}
		s["sourcetype"] = "portdock"
		if c.SourceType != "" {
			s["sourcetype"] = c.SourceType
		}
		tls(s)
		return s, nil
	case "http":
		s := map[string]any{
			"type":     "http",
			"uri":      t.URL,
			"method":   "post",
			"encoding": map[string]any{"codec": "json"},
			"framing":  map[string]any{"method": "newline_delimited"},
		}
		if len(c.Headers) > 0 {
			s["request"] = map[string]any{"headers": c.Headers}
		}
		basicOrBearer(s)
		tls(s)
		return s, nil
	case "gelf", "syslog":
		mode, addr, useTLS, err := socketAddress(t.Type, t.URL)
		if err != nil {
			return nil, err
		}
		codec := "gelf"
		if t.Type == "syslog" {
			codec = "text"
		}
		s := map[string]any{"type": "socket", "mode": mode, "address": addr, "encoding": map[string]any{"codec": codec}}
		if mode == "tcp" {
			if t.Type == "gelf" {
				s["framing"] = map[string]any{"method": "character_delimited", "character_delimited": map[string]any{"delimiter": "\x00"}}
			} else {
				s["framing"] = map[string]any{"method": "newline_delimited"}
			}
			if useTLS {
				tlsCfg := map[string]any{"enabled": true}
				if c.SkipVerify {
					tlsCfg["verify_certificate"], tlsCfg["verify_hostname"] = false, false
				}
				s["tls"] = tlsCfg
			}
		}
		return s, nil
	}
	return nil, fmt.Errorf("unknown type %q", t.Type)
}
