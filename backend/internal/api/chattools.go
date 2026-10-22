package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/assistant"
	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/monitor"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
)

// The tools of the AI assistant.
//
// Every tool goes through the API with the user's own session (s.call), so
// project roles, visibility and token limits apply exactly as in the
// interface. Read tools run at once. Action tools (deploy, roll back, restart
// or start something, back up a database, run a job) are only registered as
// pending: the browser shows a confirmation card and the action runs when the
// user presses Confirm (POST /api/chat/actions/{id}/confirm). There is no tool
// that deletes, removes, stops, kills or pauses anything, or that changes
// settings, variables, domains, users or tokens; chatCallAllowed refuses such
// requests even if a tool ever tried.

const (
	chatToolRounds    = 8  // model ↔ tools round trips per reply
	chatToolCalls     = 16 // tool calls per reply
	chatToolTimeout   = 60 * time.Second
	chatToolResultMax = 24_000 // characters of a tool result sent to the model
	chatActionTTL     = 15 * time.Minute
	chatLogLinesMax   = 2000
)

// ---- the guard: what a request made for the assistant may do ----

type chatGuardKey struct{}

func withChatGuard(ctx context.Context) context.Context {
	return context.WithValue(ctx, chatGuardKey{}, true)
}

func chatGuarded(ctx context.Context) bool {
	v, _ := ctx.Value(chatGuardKey{}).(bool)
	return v
}

// chatActionPaths are the only state-changing requests the assistant may
// make, and only after the user confirmed the action.
var chatActionPaths = []*regexp.Regexp{
	regexp.MustCompile(`^/api/applications/\d+/deploy$`),
	regexp.MustCompile(`^/api/applications/\d+/(start|restart)$`),
	regexp.MustCompile(`^/api/deployments/\d+/rollback$`),
	regexp.MustCompile(`^/api/databases/\d+/(backup-now|start|restart)$`),
	regexp.MustCompile(`^/api/services/\d+/(start|restart)$`),
	regexp.MustCompile(`^/api/servers/\d+/containers/[A-Za-z0-9_.-]+/(start|restart)$`),
	regexp.MustCompile(`^/api/jobs/\d+/run$`),
}

var errChatForbidden = errors.New("refused: the assistant can only read the platform and run the few actions the user confirms (deploy, roll back, restart, start, back up, run a job); it can never delete, stop, kill or remove anything, or change settings")

// chatCallAllowed decides whether the assistant may make an API request.
func chatCallAllowed(method, path string) error {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	switch method {
	case http.MethodGet, http.MethodHead:
		return nil
	case http.MethodPost:
		for _, re := range chatActionPaths {
			if re.MatchString(path) {
				return nil
			}
		}
	}
	return errChatForbidden
}

// ---- what a reply did: steps shown in the chat and kept with the message ----

type chatStep struct {
	ID       string         `json:"id"`
	Tool     string         `json:"tool"`
	Title    string         `json:"title"`
	Args     map[string]any `json:"args,omitempty"`
	Status   string         `json:"status"` // running | done | error | pending | confirmed | failed | cancelled
	Detail   string         `json:"detail,omitempty"`
	ActionID string         `json:"action_id,omitempty"`
	At       string         `json:"at,omitempty"`
}

type chatMeta struct {
	Steps  []chatStep `json:"steps,omitempty"`
	Notice string     `json:"notice,omitempty"`
}

func parseChatMeta(s string) *chatMeta {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var m chatMeta
	if json.Unmarshal([]byte(s), &m) != nil || (len(m.Steps) == 0 && m.Notice == "") {
		return nil
	}
	return &m
}

func (m *chatMeta) encode() string {
	if m == nil || (len(m.Steps) == 0 && m.Notice == "") {
		return ""
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// chatMessageOut is a stored message with its steps decoded for the browser.
type chatMessageOut struct {
	store.ChatMessage
	Meta *chatMeta `json:"meta,omitempty"`
}

func chatOut(m store.ChatMessage) chatMessageOut {
	return chatMessageOut{ChatMessage: m, Meta: parseChatMeta(m.Meta)}
}

// chatStepNote summarizes the tools of an earlier reply for the model.
func chatStepNote(meta *chatMeta) string {
	if meta == nil || len(meta.Steps) == 0 {
		return ""
	}
	var parts []string
	for _, st := range meta.Steps {
		p := firstNonEmpty(st.Title, st.Tool)
		switch st.Status {
		case "confirmed":
			p += " — confirmed by the user and run"
			if st.Detail != "" {
				p += ": " + clipStr(st.Detail, 200)
			}
		case "failed":
			p += " — confirmed by the user but failed: " + clipStr(st.Detail, 200)
		case "pending":
			p += " — still waiting for the user's confirmation (not run)"
		case "cancelled":
			p += " — cancelled by the user (not run)"
		case "error":
			p += " — failed: " + clipStr(st.Detail, 200)
		default:
			p += " — done"
		}
		parts = append(parts, p)
	}
	return clipStr("(Tools used for the previous reply: "+strings.Join(parts, "; ")+")", 1500)
}

// ---- actions waiting for the user's confirmation ----

type pendingAction struct {
	ID        string
	UserID    int64
	ConvID    int64
	MessageID int64 // set once the reply that proposed it is saved
	Tool      string
	Args      map[string]any
	Title     string
	CreatedAt time.Time
}

type chatActions struct {
	mu sync.Mutex
	m  map[string]*pendingAction
}

func newChatActions() *chatActions { return &chatActions{m: map[string]*pendingAction{}} }

func (a *chatActions) add(p *pendingAction) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, x := range a.m {
		if time.Since(x.CreatedAt) > chatActionTTL {
			delete(a.m, id)
		}
	}
	a.m[p.ID] = p
}

// take removes and returns the action if it belongs to the user and is not expired.
func (a *chatActions) take(id string, userID int64) (*pendingAction, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.m[id]
	if p == nil || p.UserID != userID {
		return nil, false
	}
	delete(a.m, id)
	if time.Since(p.CreatedAt) > chatActionTTL {
		return nil, false
	}
	return p, true
}

// attach records the saved message of the actions proposed in a reply.
func (a *chatActions) attach(convID, msgID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range a.m {
		if p.ConvID == convID && p.MessageID == 0 {
			p.MessageID = msgID
		}
	}
}

// ---- the tool registry ----

type chatTool struct {
	Name        string
	Description string
	Params      map[string]any
	Action      bool // needs the user's confirmation
	Title       func(args map[string]any) string
	Run         func(s *Server, r *http.Request, args map[string]any) (any, error)
}

func (t chatTool) def() assistant.Tool {
	params := t.Params
	if params == nil {
		params = schema(nil, map[string]any{})
	}
	return assistant.Tool{Type: "function", Function: assistant.ToolFunction{Name: t.Name, Description: t.Description, Parameters: params}}
}

var chatToolList []chatTool

func init() { chatToolList = buildChatTools() }

func chatToolByName(name string) *chatTool {
	for i := range chatToolList {
		if chatToolList[i].Name == name {
			return &chatToolList[i]
		}
	}
	return nil
}

// chatToolDefs lists the tools offered to the model.
func chatToolDefs(actions bool) []assistant.Tool {
	out := make([]assistant.Tool, 0, len(chatToolList))
	for _, t := range chatToolList {
		if t.Action && !actions {
			continue
		}
		out = append(out, t.def())
	}
	return out
}

func argStr(args map[string]any, key string) string {
	switch v := args[key].(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return strings.TrimSpace(fmt.Sprint(args[key]))
}

func argInt(args map[string]any, key string, def int) int {
	if v, ok := args[key]; ok && v != nil {
		if n := int(num(v)); n > 0 {
			return n
		}
	}
	return def
}

func argBool(args map[string]any, key string) bool {
	switch v := args[key].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "yes" || v == "1"
	}
	return false
}

func intParam(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func boolParam(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

func titled(format string, keys ...string) func(map[string]any) string {
	return func(args map[string]any) string {
		vals := make([]any, len(keys))
		for i, k := range keys {
			vals[i] = argStr(args, k)
		}
		return strings.TrimSpace(fmt.Sprintf(format, vals...))
	}
}

func fixed(title string) func(map[string]any) string {
	return func(map[string]any) string { return title }
}

func mcpRun(name string) func(*Server, *http.Request, map[string]any) (any, error) {
	for i := range mcpTools {
		if mcpTools[i].Name == name {
			return mcpTools[i].run
		}
	}
	panic("assistant: no MCP tool " + name)
}

func buildChatTools() []chatTool {
	appParam := str("The application: id, name, project/name or project/environment/name.")
	containerParams := schema([]string{"container"}, map[string]any{
		"container": str("Container name (or the first characters of its id, or the name of the application/database/service it belongs to)."),
		"server":    str("Optional server name or id when the same name exists on several servers."),
	})
	return []chatTool{
		// ---- knowledge ----
		{Name: "search_docs", Description: "Searches PortDock's documentation: features, how things work, menu paths, roles, troubleshooting playbooks (deployments, containers, exit codes, Traefik 404/502, DNS and TLS, databases, servers). Use English keywords, e.g. \"canary rollback\", \"exit code 137\", \"traefik 502\", \"point-in-time recovery\". Call it before explaining how PortDock behaves.",
			Params: schema([]string{"query"}, map[string]any{"query": str("English keywords or a short question.")}),
			Title:  titled("Search the documentation for “%s”", "query"),
			Run: func(_ *Server, _ *http.Request, args map[string]any) (any, error) {
				q := argStr(args, "query")
				if q == "" {
					return nil, errors.New("query is required")
				}
				res := assistant.SearchDocs(q, 4)
				if len(res) == 0 {
					return map[string]any{"query": q, "sections": []any{}, "note": "nothing matched; try other English keywords"}, nil
				}
				return map[string]any{"query": q, "sections": res}, nil
			}},
		{Name: "instance_info", Description: "This PortDock instance: version, address, whether an update is available, and counts of what the user can see (projects, applications, databases, services, servers, deployments today).",
			Title: fixed("Read the instance overview"),
			Run: func(s *Server, r *http.Request, _ map[string]any) (any, error) {
				out := map[string]any{"version": Version, "url": s.engine.PublicURL(r.Context()), "documentation": "search_docs"}
				var dash map[string]any
				if err := s.call(r, "GET", "/api/dashboard", nil, &dash); err == nil {
					if counts, ok := dash["counts"]; ok {
						out["counts"] = counts
					}
				}
				var upd map[string]any
				if err := s.call(r, "GET", "/api/system/update", nil, &upd); err == nil {
					for k, v := range upd {
						lk := strings.ToLower(k)
						if strings.Contains(lk, "token") || strings.Contains(lk, "secret") {
							continue
						}
						out["update_"+k] = v
					}
				}
				return out, nil
			}},

		// ---- reading the platform (same tools as the MCP server) ----
		{Name: "list_projects", Description: "Projects and their environments the user can see.", Title: fixed("List the projects"), Run: mcpRun("list_projects")},
		{Name: "list_applications", Description: "Applications with their id, project, environment, status, source and image.",
			Params: schema(nil, map[string]any{"project": str("Optional project name to filter on.")}), Title: fixed("List the applications"), Run: mcpRun("list_applications")},
		{Name: "get_application", Description: "Settings, status, domains and the last deployments of an application.",
			Params: schema([]string{"app"}, map[string]any{"app": appParam}), Title: titled("Read application %s", "app"), Run: mcpRun("get_application")},
		{Name: "list_deployments", Description: "The last deployments of an application, newest first, with status, trigger and commit.",
			Params: schema([]string{"app"}, map[string]any{"app": appParam}), Title: titled("List the deployments of %s", "app"), Run: mcpRun("list_deployments")},
		{Name: "get_deployment", Description: "Status of a deployment and the end of its build and rollout log (the place to look when a deployment failed).",
			Params: schema([]string{"deployment_id"}, map[string]any{"deployment_id": intParam("The deployment id."), "lines": intParam("Log lines to return (default 80, at most 400).")}),
			Title:  titled("Read deployment #%s", "deployment_id"),
			Run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
				args["lines"] = float64(min(argInt(args, "lines", 80), 400))
				return mcpRun("get_deployment")(s, r, args)
			}},
		{Name: "application_logs", Description: "The last lines written by the application's running container (for a specific container, or when the application has several, use container_logs).",
			Params: schema([]string{"app"}, map[string]any{"app": appParam, "lines": intParam("Lines (default 100, at most 1000).")}),
			Title:  titled("Read the logs of application %s", "app"), Run: mcpRun("application_logs")},
		{Name: "list_versions", Description: "The saved versions of an application (v1, v2…): which one is live, their commit and age. Rolling back uses rollback_deployment with the deployment id of a version.",
			Params: schema([]string{"app"}, map[string]any{"app": appParam}), Title: titled("List the versions of %s", "app"),
			Run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
				id, err := s.mcpApp(r, args)
				if err != nil {
					return nil, err
				}
				var vs []map[string]any
				if err := s.call(r, "GET", fmt.Sprintf("/api/applications/%d/versions", id), nil, &vs); err != nil {
					return nil, err
				}
				return keepKeys(vs, 30, "id", "tag", "number", "live", "status", "commit_sha", "commit_message", "branch", "deployment_id", "created_at", "expires_at"), nil
			}},
		{Name: "list_domains", Description: "Domains of an application with their DNS status, certificate status and errors, HTTPS and redirects.",
			Params: schema([]string{"app"}, map[string]any{"app": appParam}), Title: titled("List the domains of %s", "app"),
			Run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
				id, err := s.mcpApp(r, args)
				if err != nil {
					return nil, err
				}
				var ds []map[string]any
				if err := s.call(r, "GET", fmt.Sprintf("/api/applications/%d/domains", id), nil, &ds); err != nil {
					return nil, err
				}
				return keepKeys(ds, 50, "id", "host", "path", "port", "service", "https", "www_redirect", "indexing", "dns_status", "dns_detail", "dns_checked_at", "cert_mode", "cert_status", "cert_issuer", "cert_expires_at", "cert_error"), nil
			}},
		{Name: "list_jobs", Description: "Scheduled jobs (cron) of an application with their schedule, command and last result.",
			Params: schema([]string{"app"}, map[string]any{"app": appParam}), Title: titled("List the jobs of %s", "app"),
			Run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
				id, err := s.mcpApp(r, args)
				if err != nil {
					return nil, err
				}
				var js []map[string]any
				if err := s.call(r, "GET", fmt.Sprintf("/api/applications/%d/jobs", id), nil, &js); err != nil {
					return nil, err
				}
				return keepKeys(js, 50), nil
			}},
		{Name: "list_databases", Description: "Databases with their engine, status and project.", Title: fixed("List the databases"), Run: mcpRun("list_databases")},
		{Name: "get_database", Description: "A database: engine, status, server, backup settings and its last backups (never its password or connection URL).",
			Params: schema([]string{"database"}, map[string]any{"database": str("Database id, name or project/name.")}), Title: titled("Read database %s", "database"),
			Run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
				db, err := s.chatResolveNamed(r, "/api/databases", argStr(args, "database"), "database")
				if err != nil {
					return nil, err
				}
				var detail map[string]any
				if err := s.call(r, "GET", fmt.Sprintf("/api/databases/%d", db.ID), nil, &detail); err != nil {
					return nil, err
				}
				out := map[string]any{"project": db.Project, "environment": db.Environment}
				for k, v := range detail {
					lk := strings.ToLower(k)
					if strings.Contains(lk, "password") || strings.Contains(lk, "url") || strings.Contains(lk, "secret") || k == "access" {
						continue
					}
					out[k] = v
				}
				var runs any
				if err := s.call(r, "GET", fmt.Sprintf("/api/databases/%d/backup-runs", db.ID), nil, &runs); err == nil {
					list, _ := runs.([]any)
					if m, ok := runs.(map[string]any); ok {
						list, _ = m["runs"].([]any)
					}
					var recent []map[string]any
					for i, it := range list {
						if i == 5 {
							break
						}
						if im, ok := it.(map[string]any); ok {
							recent = append(recent, pickKeys(im, "id", "status", "kind", "size", "message", "created_at", "finished_at", "verify_status", "verify_message", "restore_status"))
						}
					}
					out["recent_backups"] = recent
				}
				return out, nil
			}},
		{Name: "list_services", Description: "One-click services (Keycloak, n8n, Grafana…) and custom compose services with their status, domain and project.", Title: fixed("List the services"),
			Run: func(s *Server, r *http.Request, _ map[string]any) (any, error) {
				items, err := s.chatNamedList(r, "/api/services")
				if err != nil {
					return nil, err
				}
				out := []map[string]any{}
				for _, it := range items {
					out = append(out, map[string]any{"id": it.ID, "name": it.Name, "project": it.Project, "environment": it.Environment, "template": it.Raw["template"], "status": it.Raw["status"], "domain": it.Raw["domain"], "server_id": it.Raw["server_id"]})
				}
				return out, nil
			}},
		{Name: "list_servers", Description: "Servers with their status and Docker version.", Title: fixed("List the servers"), Run: mcpRun("list_servers")},
		{Name: "server_status", Description: "Health of the servers: status, load, memory, disk, containers, uptime (one server or all, up to 10).",
			Params: schema(nil, map[string]any{"server": str("Optional server name or id.")}), Title: titled("Read the status of the servers %s", "server"),
			Run: func(s *Server, r *http.Request, args map[string]any) (any, error) { return s.chatServerStatus(r, args) }},
		{Name: "list_containers", Description: "Containers of the servers (all or one), with state, image, server and the PortDock resource each belongs to. Use it to find the container the user talks about.",
			Params: schema(nil, map[string]any{"server": str("Optional server name or id."), "query": str("Optional text to filter on (container name, image, application name)."), "state": str("Optional state filter: running, exited, restarting, paused, created.")}),
			Title:  titled("List the containers %s %s", "server", "query"),
			Run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
				fleet, err := s.chatFleet(r, argStr(args, "server"), false)
				if err != nil {
					return nil, err
				}
				q, state := strings.ToLower(argStr(args, "query")), strings.ToLower(argStr(args, "state"))
				out := []map[string]any{}
				for _, c := range fleet.Containers {
					if state != "" && !strings.EqualFold(c.State, state) {
						continue
					}
					if q != "" && !strings.Contains(strings.ToLower(c.Name+" "+c.Image+" "+c.resourceLabel()), q) {
						continue
					}
					out = append(out, c.brief())
					if len(out) == 200 {
						break
					}
				}
				res := map[string]any{"containers": out, "count": len(out)}
				var unreachable []string
				for _, srv := range fleet.Servers {
					if srv.Error != "" {
						unreachable = append(unreachable, srv.Name+": "+srv.Error)
					}
				}
				if len(unreachable) > 0 {
					res["unreachable_servers"] = unreachable
				}
				return res, nil
			}},
		{Name: "container_logs", Description: "The last lines of a container's logs (with timestamps), optionally only the lines that look like errors or that contain a text; also the most repeated lines. The main tool to analyze a failing container.",
			Params: schema([]string{"container"}, map[string]any{
				"container":   containerParams["properties"].(map[string]any)["container"],
				"server":      containerParams["properties"].(map[string]any)["server"],
				"lines":       intParam("Lines to read from the end (default 200, at most 2000)."),
				"errors_only": boolParam("Keep only lines that look like errors, exceptions, failures, timeouts or 5xx."),
				"filter":      str("Keep only lines containing this text (case-insensitive)."),
			}),
			Title: titled("Read the logs of container %s", "container"),
			Run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
				return s.chatContainerLogs(r, args)
			}},
		{Name: "container_inspect", Description: "Details of a container: state, exit code, OOMKilled, restart count, health check and its last outputs, image, command, limits, mounts, networks, ports, labels (environment variable names only, never values).",
			Params: containerParams, Title: titled("Inspect container %s", "container"),
			Run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
				return s.chatContainerInspect(r, args)
			}},
		{Name: "container_stats", Description: "Live CPU, memory, network and process counts of the running containers (all, or those matching a name).",
			Params: schema(nil, map[string]any{"container": str("Optional container name (or part of it)."), "server": str("Optional server name or id.")}),
			Title:  titled("Read the resource usage of containers %s", "container"),
			Run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
				fleet, err := s.chatFleet(r, argStr(args, "server"), true)
				if err != nil {
					return nil, err
				}
				q := strings.ToLower(argStr(args, "container"))
				type row struct {
					brief map[string]any
					cpu   float64
				}
				var rows []row
				for _, c := range fleet.Containers {
					if c.State != "running" || (q != "" && !strings.Contains(strings.ToLower(c.Name), q)) {
						continue
					}
					var cpu float64
					if c.Stats != nil {
						cpu, _ = strconv.ParseFloat(strings.TrimSuffix(c.Stats.CPUPerc, "%"), 64)
					}
					rows = append(rows, row{c.brief(), cpu})
				}
				sort.SliceStable(rows, func(i, j int) bool { return rows[i].cpu > rows[j].cpu })
				out := []map[string]any{}
				for i, x := range rows {
					if i == 100 {
						break
					}
					out = append(out, x.brief)
				}
				return map[string]any{"containers": out, "count": len(out)}, nil
			}},
		{Name: "resource_metrics", Description: "Metrics history of a server, application, database or service (CPU, memory, disk, network, restarts, availability) over a range, summarized and sampled.",
			Params: schema([]string{"kind", "id"}, map[string]any{
				"kind":  str("server, application, database or service."),
				"id":    intParam("The id (from the list tools)."),
				"range": str("1h, 6h, 24h (default), 7d or 30d."),
			}),
			Title: titled("Read the metrics of %s #%s", "kind", "id"),
			Run:   func(s *Server, r *http.Request, args map[string]any) (any, error) { return s.chatMetrics(r, args) }},
		{Name: "list_alerts", Description: "Alerts of the alert rules, firing ones first.", Title: fixed("List the alerts"), Run: mcpRun("list_alerts")},
		{Name: "list_monitors", Description: "Uptime monitors with their status, last response time and last error.", Title: fixed("List the uptime monitors"),
			Run: func(s *Server, r *http.Request, _ map[string]any) (any, error) {
				var ms []map[string]any
				if err := s.call(r, "GET", "/api/monitors", nil, &ms); err != nil {
					return nil, err
				}
				return keepKeys(ms, 100, "id", "name", "url", "application_id", "enabled", "public", "status", "interval_seconds", "consecutive_failures", "last_checked_at", "last_change_at", "last_response_ms", "last_status_code", "last_error", "ssl_expires_at"), nil
			}},
		{Name: "recent_activity", Description: "The last entries of the audit log the user may see (who did what, when): deployments, restarts, settings changes.",
			Params: schema(nil, map[string]any{"limit": intParam("Entries (default 30, at most 100).")}), Title: fixed("Read the recent activity"),
			Run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
				var out struct {
					Entries []map[string]any `json:"entries"`
				}
				if err := s.call(r, "GET", fmt.Sprintf("/api/activity?limit=%d", min(argInt(args, "limit", 30), 100)), nil, &out); err != nil {
					return nil, err
				}
				return keepKeys(out.Entries, 100, "created_at", "user_name", "action", "target"), nil
			}},

		// ---- actions: proposed to the user, run only after Confirm ----
		{Name: "deploy_application", Action: true, Description: "Deploys the application's branch (or a commit), like the Deploy button; in a protected environment it becomes an approval request. Needs the user's confirmation.",
			Params: schema([]string{"app"}, map[string]any{"app": appParam, "commit": str("Optional commit SHA.")}), Title: titled("Deploy application %s %s", "app", "commit"),
			Run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
				copy := map[string]any{"app": args["app"], "commit": args["commit"]}
				return mcpRun("deploy_application")(s, r, copy)
			}},
		{Name: "rollback_deployment", Action: true, Description: "Redeploys the image of an earlier successful deployment, without rebuilding (list_deployments or list_versions give the id). Needs the user's confirmation.",
			Params: schema([]string{"deployment_id"}, map[string]any{"deployment_id": intParam("The deployment to go back to.")}), Title: titled("Roll back to deployment #%s", "deployment_id"), Run: mcpRun("rollback_deployment")},
		{Name: "restart_application", Action: true, Description: "Restarts the application's containers (no new deployment). Needs the user's confirmation.",
			Params: schema([]string{"app"}, map[string]any{"app": appParam}), Title: titled("Restart application %s", "app"), Run: mcpRun("restart_application")},
		{Name: "start_application", Action: true, Description: "Starts a stopped application. Needs the user's confirmation.",
			Params: schema([]string{"app"}, map[string]any{"app": appParam}), Title: titled("Start application %s", "app"),
			Run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
				id, err := s.mcpApp(r, args)
				if err != nil {
					return nil, err
				}
				return map[string]any{"started": true}, s.call(r, "POST", fmt.Sprintf("/api/applications/%d/start", id), map[string]any{}, nil)
			}},
		{Name: "restart_container", Action: true, Description: "Restarts one container (docker restart). Needs the user's confirmation.",
			Params: containerParams, Title: titled("Restart container %s", "container"),
			Run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
				return s.chatContainerAction(r, args, "restart")
			}},
		{Name: "start_container", Action: true, Description: "Starts a stopped container (docker start). Needs the user's confirmation.",
			Params: containerParams, Title: titled("Start container %s", "container"),
			Run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
				return s.chatContainerAction(r, args, "start")
			}},
		{Name: "restart_database", Action: true, Description: "Restarts a database's container. Needs the user's confirmation.",
			Params: schema([]string{"database"}, map[string]any{"database": str("Database id, name or project/name.")}), Title: titled("Restart database %s", "database"),
			Run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
				db, err := s.chatResolveNamed(r, "/api/databases", argStr(args, "database"), "database")
				if err != nil {
					return nil, err
				}
				return map[string]any{"restarted": db.Name}, s.call(r, "POST", fmt.Sprintf("/api/databases/%d/restart", db.ID), map[string]any{}, nil)
			}},
		{Name: "restart_service", Action: true, Description: "Restarts a one-click or compose service. Needs the user's confirmation.",
			Params: schema([]string{"service"}, map[string]any{"service": str("Service id, name or project/name.")}), Title: titled("Restart service %s", "service"),
			Run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
				svc, err := s.chatResolveNamed(r, "/api/services", argStr(args, "service"), "service")
				if err != nil {
					return nil, err
				}
				return map[string]any{"restarted": svc.Name}, s.call(r, "POST", fmt.Sprintf("/api/services/%d/restart", svc.ID), map[string]any{}, nil)
			}},
		{Name: "backup_database", Action: true, Description: "Takes a backup of a database now. Needs the user's confirmation.",
			Params: schema([]string{"database_id"}, map[string]any{"database_id": intParam("The database id (list_databases).")}), Title: titled("Back up database #%s", "database_id"), Run: mcpRun("backup_database")},
		{Name: "run_job", Action: true, Description: "Runs a scheduled job of an application now. Needs the user's confirmation.",
			Params: schema([]string{"app", "job"}, map[string]any{"app": appParam, "job": str("Job name or id (list_jobs).")}), Title: titled("Run job %s of %s", "job", "app"),
			Run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
				id, err := s.mcpApp(r, args)
				if err != nil {
					return nil, err
				}
				var js []map[string]any
				if err := s.call(r, "GET", fmt.Sprintf("/api/applications/%d/jobs", id), nil, &js); err != nil {
					return nil, err
				}
				ref := argStr(args, "job")
				var jobID int64
				for _, j := range js {
					if strings.EqualFold(toStr(j["name"]), ref) || toStr(j["id"]) == ref {
						jobID = int64(num(j["id"]))
					}
				}
				if jobID == 0 {
					return nil, fmt.Errorf("no job %q in this application", ref)
				}
				var run map[string]any
				if err := s.call(r, "POST", fmt.Sprintf("/api/jobs/%d/run", jobID), map[string]any{}, &run); err != nil {
					return nil, err
				}
				return pickKeys(run, "id", "status", "started_at"), nil
			}},
	}
}

// ---- helpers shared by the tools ----

func toStr(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func pickKeys(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			out[k] = v
		}
	}
	return out
}

// keepKeys keeps the given keys (all of them when none is given) of at most n items.
func keepKeys(items []map[string]any, n int, keys ...string) []map[string]any {
	out := make([]map[string]any, 0, min(len(items), n))
	for i, it := range items {
		if i == n {
			break
		}
		if len(keys) == 0 {
			out = append(out, it)
			continue
		}
		out = append(out, pickKeys(it, keys...))
	}
	return out
}

func clipStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

var (
	ansiRe    = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	logErrRe  = regexp.MustCompile(`(?i)\b(error|exception|fatal|panic|traceback|critical|crit|refused|denied|unreachable|timeout|timed out|failed|failure|cannot|could not|unhandled|segfault|oom|out of memory|killed|emerg)\b|\s5\d\d\s|\[(error|fatal|crit)\]|\bERR\b`)
	secretKV  = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|authorization|bearer|private[_-]?key)\b(\s*["']?\s*[:=]\s*["']?)([^\s"'&,;]{3,})`)
	secretURL = regexp.MustCompile(`(://[^/\s:@]+:)([^@\s/]+)@`)
	bearerRe  = regexp.MustCompile(`(?i)\b(bearer|basic)\s+([A-Za-z0-9._~+/=-]{3,})`)
	tsPrefix  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T[0-9:.]+Z?\s*`)
	digitsRe  = regexp.MustCompile(`\b[0-9a-f]{8,}\b|\d+`)
)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// maskSecrets hides values that look like credentials in text read from
// containers, so they never reach the model.
func maskSecrets(s string) string {
	s = secretURL.ReplaceAllString(s, "${1}***@")
	s = bearerRe.ReplaceAllString(s, "${1} ***")
	return secretKV.ReplaceAllString(s, "${1}${2}***")
}

// frequentLines returns the most repeated log lines (numbers and ids folded).
func frequentLines(lines []string, n int) []string {
	counts := map[string]int{}
	sample := map[string]string{}
	for _, l := range lines {
		key := digitsRe.ReplaceAllString(tsPrefix.ReplaceAllString(l, ""), "#")
		if len(strings.TrimSpace(key)) < 8 {
			continue
		}
		counts[key]++
		if _, ok := sample[key]; !ok {
			sample[key] = tsPrefix.ReplaceAllString(l, "")
		}
	}
	type kv struct {
		k string
		n int
	}
	var all []kv
	for k, c := range counts {
		if c >= 2 {
			all = append(all, kv{k, c})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].n > all[j].n || (all[i].n == all[j].n && all[i].k < all[j].k) })
	var out []string
	for i, x := range all {
		if i == n {
			break
		}
		out = append(out, fmt.Sprintf("%d× %s", x.n, clipStr(sample[x.k], 200)))
	}
	return out
}

// ---- resolving what the user names ----

type chatNamed struct {
	ID                         int64
	Name, Project, Environment string
	Raw                        map[string]any
}

// chatEnvNames maps environment ids to their project and environment names.
func (s *Server) chatEnvNames(ctx context.Context) map[int64][2]string {
	envs, _ := store.Select[store.Environment](ctx, s.db, "SELECT * FROM environments")
	projects, _ := store.Select[store.Project](ctx, s.db, "SELECT * FROM projects")
	pname := map[int64]string{}
	for _, p := range projects {
		pname[p.ID] = p.Name
	}
	out := map[int64][2]string{}
	for _, e := range envs {
		out[e.ID] = [2]string{pname[e.ProjectID], e.Name}
	}
	return out
}

// chatNamedList lists databases or services the user can see, with names.
func (s *Server) chatNamedList(r *http.Request, path string) ([]chatNamed, error) {
	var items []map[string]any
	if err := s.call(r, "GET", path, nil, &items); err != nil {
		return nil, err
	}
	names := s.chatEnvNames(r.Context())
	out := make([]chatNamed, 0, len(items))
	for _, it := range items {
		env := names[int64(num(it["environment_id"]))]
		out = append(out, chatNamed{ID: int64(num(it["id"])), Name: toStr(it["name"]), Project: env[0], Environment: env[1], Raw: it})
	}
	return out, nil
}

// chatResolveNamed finds one database or service by id, name, project/name or project/environment/name.
func (s *Server) chatResolveNamed(r *http.Request, path, ref, what string) (chatNamed, error) {
	if ref == "" {
		return chatNamed{}, fmt.Errorf("%s is required: its id, name or project/name", what)
	}
	items, err := s.chatNamedList(r, path)
	if err != nil {
		return chatNamed{}, err
	}
	if n, err := strconv.ParseInt(ref, 10, 64); err == nil {
		for _, it := range items {
			if it.ID == n {
				return it, nil
			}
		}
		return chatNamed{}, fmt.Errorf("no %s with id %d that you can see", what, n)
	}
	parts := strings.Split(ref, "/")
	var found []chatNamed
	for _, it := range items {
		match := false
		switch len(parts) {
		case 1:
			match = strings.EqualFold(it.Name, parts[0])
		case 2:
			match = strings.EqualFold(it.Project, parts[0]) && strings.EqualFold(it.Name, parts[1])
		case 3:
			match = strings.EqualFold(it.Project, parts[0]) && strings.EqualFold(it.Environment, parts[1]) && strings.EqualFold(it.Name, parts[2])
		}
		if match {
			found = append(found, it)
		}
	}
	switch len(found) {
	case 0:
		return chatNamed{}, fmt.Errorf("no %s %q that you can see", what, ref)
	case 1:
		return found[0], nil
	}
	var names []string
	for _, it := range found {
		names = append(names, it.Project+"/"+it.Environment+"/"+it.Name)
	}
	return chatNamed{}, fmt.Errorf("%q matches several %ss: %s", ref, what, strings.Join(names, ", "))
}

// chatServerID resolves a server by id, name or host.
func (s *Server) chatServerID(r *http.Request, ref string) (int64, error) {
	var servers []map[string]any
	if err := s.call(r, "GET", "/api/servers", nil, &servers); err != nil {
		return 0, err
	}
	if n, err := strconv.ParseInt(ref, 10, 64); err == nil {
		for _, x := range servers {
			if int64(num(x["id"])) == n {
				return n, nil
			}
		}
	}
	var names []string
	for _, x := range servers {
		if strings.EqualFold(toStr(x["name"]), ref) || strings.EqualFold(toStr(x["host"]), ref) {
			return int64(num(x["id"])), nil
		}
		names = append(names, toStr(x["name"]))
	}
	for _, x := range servers {
		if strings.Contains(strings.ToLower(toStr(x["name"])), strings.ToLower(ref)) {
			return int64(num(x["id"])), nil
		}
	}
	return 0, fmt.Errorf("no server %q (servers: %s)", ref, strings.Join(names, ", "))
}

// ---- containers ----

type chatContainer struct {
	docker.Container
	Resource *fleetResource `json:"resource"`
	Stats    *docker.Stat   `json:"stats,omitempty"`
}

func (c chatContainer) resourceLabel() string {
	if c.Resource == nil {
		return ""
	}
	return fmt.Sprintf("%s %s/%s/%s", c.Resource.Kind, c.Resource.Project, c.Resource.Environment, c.Resource.Name)
}

func (c chatContainer) brief() map[string]any {
	out := map[string]any{"name": c.Name, "id": shortID(c.ID), "state": c.State, "status": c.Status, "image": c.Image, "server": c.ServerName}
	if rl := c.resourceLabel(); rl != "" {
		out["resource"] = rl
	}
	if c.Ports != "" {
		out["ports"] = c.Ports
	}
	if c.Stats != nil {
		out["cpu"] = c.Stats.CPUPerc
		out["memory"] = c.Stats.MemUsage
		out["memory_percent"] = c.Stats.MemPerc
		out["net_io"] = c.Stats.NetIO
		out["pids"] = c.Stats.PIDs
	}
	return out
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

type chatFleet struct {
	Servers    []fleetServer   `json:"servers"`
	Containers []chatContainer `json:"containers"`
}

// chatFleet lists the containers the user may see, of one server or all.
func (s *Server) chatFleet(r *http.Request, server string, stats bool) (chatFleet, error) {
	q := url.Values{}
	if stats {
		q.Set("stats", "1")
	}
	if server != "" {
		id, err := s.chatServerID(r, server)
		if err != nil {
			return chatFleet{}, err
		}
		q.Set("server", strconv.FormatInt(id, 10))
	}
	path := "/api/fleet/containers"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out chatFleet
	err := s.call(r, "GET", path, nil, &out)
	return out, err
}

// chatFindContainer resolves the container named in args: exact name, id
// prefix, part of the name, or the resource it belongs to. When several match
// and exactly one runs, that one is meant.
func (s *Server) chatFindContainer(r *http.Request, args map[string]any) (chatContainer, error) {
	ref := strings.TrimPrefix(argStr(args, "container"), "/")
	if ref == "" {
		return chatContainer{}, errors.New("container is required: its name or id (list_containers shows them)")
	}
	fleet, err := s.chatFleet(r, argStr(args, "server"), false)
	if err != nil {
		return chatContainer{}, err
	}
	low := strings.ToLower(ref)
	var exact, prefix, partial, byResource []chatContainer
	for _, c := range fleet.Containers {
		name := strings.ToLower(strings.TrimPrefix(c.Name, "/"))
		switch {
		case name == low:
			exact = append(exact, c)
		case len(ref) >= 4 && strings.HasPrefix(strings.ToLower(c.ID), low):
			prefix = append(prefix, c)
		case strings.Contains(name, low):
			partial = append(partial, c)
		case c.Resource != nil && (strings.EqualFold(c.Resource.Name, ref) || strings.EqualFold(c.Resource.Project+"/"+c.Resource.Name, ref) ||
			strings.EqualFold(c.Resource.Project+"/"+c.Resource.Environment+"/"+c.Resource.Name, ref)):
			byResource = append(byResource, c)
		}
	}
	for _, group := range [][]chatContainer{exact, prefix, partial, byResource} {
		switch len(group) {
		case 0:
			continue
		case 1:
			return group[0], nil
		}
		var running []chatContainer
		for _, c := range group {
			if c.State == "running" {
				running = append(running, c)
			}
		}
		if len(running) == 1 {
			return running[0], nil
		}
		var names []string
		for i, c := range group {
			if i == 10 {
				names = append(names, "…")
				break
			}
			names = append(names, fmt.Sprintf("%s (%s, %s)", c.Name, c.ServerName, c.State))
		}
		return chatContainer{}, fmt.Errorf("%q matches several containers: %s — give the full name and the server", ref, strings.Join(names, "; "))
	}
	msg := fmt.Sprintf("no container matching %q among the %d containers you can see; list_containers shows them", ref, len(fleet.Containers))
	var unreachable []string
	for _, srv := range fleet.Servers {
		if srv.Error != "" {
			unreachable = append(unreachable, srv.Name+": "+srv.Error)
		}
	}
	if len(unreachable) > 0 {
		msg += " (unreachable servers: " + strings.Join(unreachable, "; ") + ")"
	}
	return chatContainer{}, errors.New(msg)
}

// chatContainerLogs reads the end of a container's logs after the API
// confirmed the user may read that container.
func (s *Server) chatContainerLogs(r *http.Request, args map[string]any) (any, error) {
	c, err := s.chatFindContainer(r, args)
	if err != nil {
		return nil, err
	}
	if err := s.call(r, "GET", fmt.Sprintf("/api/servers/%d/containers/%s/inspect", c.ServerID, url.PathEscape(c.ID)), nil, nil); err != nil {
		return nil, err
	}
	lines := min(argInt(args, "lines", 200), chatLogLinesMax)
	x, _, err := s.pool.ForID(r.Context(), c.ServerID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var buf bytes.Buffer
	if err := docker.Logs(ctx, x, c.ID, lines, false, true, &buf); err != nil && buf.Len() == 0 {
		return nil, err
	}
	all := strings.Split(strings.TrimRight(stripANSI(buf.String()), "\n"), "\n")
	if len(all) == 1 && all[0] == "" {
		all = nil
	}
	filter := strings.ToLower(argStr(args, "filter"))
	errorsOnly := argBool(args, "errors_only")
	var kept []string
	errorLines := 0
	for _, l := range all {
		if logErrRe.MatchString(l) {
			errorLines++
		}
		if filter != "" && !strings.Contains(strings.ToLower(l), filter) {
			continue
		}
		if errorsOnly && !logErrRe.MatchString(l) {
			continue
		}
		kept = append(kept, l)
	}
	text := maskSecrets(strings.Join(kept, "\n"))
	truncated := false
	if limit := chatToolResultMax - 3000; len(text) > limit {
		text = text[len(text)-limit:]
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
		truncated = true
	}
	out := map[string]any{"container": c.Name, "server": c.ServerName, "image": c.Image, "state": c.State, "status": c.Status,
		"lines_read": len(all), "lines_returned": len(kept), "error_like_lines": errorLines, "logs": text, "timestamps": "UTC"}
	if rl := c.resourceLabel(); rl != "" {
		out["resource"] = rl
	}
	if filter != "" {
		out["filter"] = filter
	}
	if errorsOnly {
		out["errors_only"] = true
	}
	if truncated {
		out["note"] = "older lines were cut to fit; ask for fewer lines or use filter / errors_only"
	}
	if freq := frequentLines(kept, 5); len(freq) > 0 {
		out["frequent_lines"] = freq
	}
	return out, nil
}

// chatContainerInspect summarizes docker inspect without any secret: the API
// already strips the environment for readers; values are never passed on.
func (s *Server) chatContainerInspect(r *http.Request, args map[string]any) (any, error) {
	c, err := s.chatFindContainer(r, args)
	if err != nil {
		return nil, err
	}
	var list []map[string]any
	if err := s.call(r, "GET", fmt.Sprintf("/api/servers/%d/containers/%s/inspect", c.ServerID, url.PathEscape(c.ID)), nil, &list); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, errors.New("the container was not found (it may have been removed meanwhile)")
	}
	out := summarizeInspect(list[0])
	out["server"] = c.ServerName
	if rl := c.resourceLabel(); rl != "" {
		out["resource"] = rl
	}
	return out, nil
}

func summarizeInspect(c map[string]any) map[string]any {
	cfg, st, hc, ns := asMap(c["Config"]), asMap(c["State"]), asMap(c["HostConfig"]), asMap(c["NetworkSettings"])
	out := map[string]any{
		"name": strings.TrimPrefix(toStr(c["Name"]), "/"), "id": shortID(toStr(c["Id"])), "image": cfg["Image"], "created": c["Created"],
		"restart_count": c["RestartCount"], "platform": c["Platform"],
		"state":          pickKeys(st, "Status", "Running", "Paused", "Restarting", "OOMKilled", "Dead", "ExitCode", "Error", "StartedAt", "FinishedAt"),
		"restart_policy": hc["RestartPolicy"], "memory_limit_bytes": hc["Memory"], "nano_cpus": hc["NanoCpus"],
		"working_dir": cfg["WorkingDir"], "user": cfg["User"],
	}
	if h := asMap(st["Health"]); h != nil {
		hs := map[string]any{"status": h["Status"], "failing_streak": h["FailingStreak"]}
		if log, ok := h["Log"].([]any); ok {
			if n := len(log); n > 3 {
				log = log[n-3:]
			}
			var checks []map[string]any
			for _, e := range log {
				em := asMap(e)
				checks = append(checks, map[string]any{"start": em["Start"], "exit_code": em["ExitCode"], "output": clipStr(maskSecrets(toStr(em["Output"])), 400)})
			}
			hs["last_checks"] = checks
		}
		out["health"] = hs
	}
	if env, ok := cfg["Env"].([]any); ok {
		keys := []string{}
		for _, e := range env {
			k, _, _ := strings.Cut(toStr(e), "=")
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out["env_keys"] = keys
	}
	out["cmd"], out["entrypoint"] = maskList(cfg["Cmd"]), maskList(cfg["Entrypoint"])
	if hcfg := asMap(cfg["Healthcheck"]); hcfg != nil {
		out["healthcheck"] = map[string]any{"test": maskList(hcfg["Test"]), "interval_ns": hcfg["Interval"], "timeout_ns": hcfg["Timeout"], "retries": hcfg["Retries"], "start_period_ns": hcfg["StartPeriod"]}
	}
	if labels := asMap(cfg["Labels"]); labels != nil {
		keep := map[string]any{}
		for k, v := range labels {
			if strings.HasPrefix(k, "portdock.") || strings.HasPrefix(k, "shipyard.") || strings.HasPrefix(k, "traefik.") || strings.HasPrefix(k, "com.docker.compose.") {
				keep[k] = v
			}
		}
		out["labels"] = keep
	}
	if mounts, ok := c["Mounts"].([]any); ok {
		var ms []map[string]any
		for _, m := range mounts {
			mm := asMap(m)
			ms = append(ms, map[string]any{"type": mm["Type"], "source": firstNonEmpty(toStr(mm["Name"]), toStr(mm["Source"])), "destination": mm["Destination"], "rw": mm["RW"]})
		}
		out["mounts"] = ms
	}
	if nets := asMap(ns["Networks"]); nets != nil {
		nn := map[string]any{}
		for name, v := range nets {
			vm := asMap(v)
			nn[name] = map[string]any{"ip": vm["IPAddress"], "aliases": vm["Aliases"]}
		}
		out["networks"] = nn
	}
	if ports := asMap(ns["Ports"]); len(ports) > 0 {
		out["ports"] = ports
	}
	if lc := asMap(hc["LogConfig"]); lc != nil {
		out["log_driver"] = lc["Type"]
	}
	return out
}

func maskList(v any) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}
	out := make([]string, 0, len(list))
	for _, x := range list {
		out = append(out, maskSecrets(toStr(x)))
	}
	return out
}

// chatContainerAction starts or restarts a container through the API (which
// checks the user's rights and audits it).
func (s *Server) chatContainerAction(r *http.Request, args map[string]any, action string) (any, error) {
	if action != "start" && action != "restart" {
		return nil, errChatForbidden
	}
	c, err := s.chatFindContainer(r, args)
	if err != nil {
		return nil, err
	}
	if err := s.call(r, "POST", fmt.Sprintf("/api/servers/%d/containers/%s/%s", c.ServerID, url.PathEscape(c.ID), action), map[string]any{}, nil); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "action": action, "container": c.Name, "server": c.ServerName}, nil
}

// ---- servers and metrics ----

func (s *Server) chatServerStatus(r *http.Request, args map[string]any) (any, error) {
	var servers []map[string]any
	if err := s.call(r, "GET", "/api/servers", nil, &servers); err != nil {
		return nil, err
	}
	ref := argStr(args, "server")
	out := []map[string]any{}
	for _, x := range servers {
		id, name := int64(num(x["id"])), toStr(x["name"])
		if ref != "" && !strings.EqualFold(name, ref) && strconv.FormatInt(id, 10) != ref && !strings.EqualFold(toStr(x["host"]), ref) {
			continue
		}
		item := map[string]any{"id": id, "name": name, "status": x["status"], "status_message": x["status_message"], "docker_version": x["docker_version"],
			"proxy_status": x["proxy_status"], "last_check_at": x["last_check_at"], "is_local": x["is_local"]}
		if len(out) < 10 && toStr(x["status"]) != "unreachable" {
			var m docker.Metrics
			ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			err := s.call(r.WithContext(ctx), "GET", fmt.Sprintf("/api/servers/%d/metrics", id), nil, &m)
			cancel()
			if err != nil {
				item["metrics_error"] = err.Error()
			} else {
				item["cpus"] = m.CPUs
				item["load"] = []float64{m.Load1, m.Load5, m.Load15}
				item["memory"] = fmt.Sprintf("%s of %s used (%.0f%%)", humanBytes(m.MemUsed), humanBytes(m.MemTotal), pct(m.MemUsed, m.MemTotal))
				item["disk"] = fmt.Sprintf("%s of %s used (%.0f%%)", humanBytes(m.DiskUsed), humanBytes(m.DiskTotal), pct(m.DiskUsed, m.DiskTotal))
				item["containers_running"], item["containers_total"] = m.RunningCont, m.Containers
				item["uptime"] = (time.Duration(m.Uptime) * time.Second).Round(time.Minute).String()
				item["os"], item["kernel"] = m.OS, m.Kernel
			}
		}
		out = append(out, item)
	}
	if ref != "" && len(out) == 0 {
		return nil, fmt.Errorf("no server %q", ref)
	}
	return out, nil
}

func pct(used, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return 100 * float64(used) / float64(total)
}

func (s *Server) chatMetrics(r *http.Request, args map[string]any) (any, error) {
	kind := strings.ToLower(argStr(args, "kind"))
	if _, ok := scopeTables[kind]; !ok {
		return nil, errors.New("kind must be server, application, database or service")
	}
	id := int64(num(args["id"]))
	if id <= 0 {
		return nil, errors.New("id is required")
	}
	rg := firstNonEmpty(argStr(args, "range"), "24h")
	var res struct {
		Range  string          `json:"range"`
		Bucket int64           `json:"bucket"`
		Points []monitor.Point `json:"points"`
	}
	if err := s.call(r, "GET", fmt.Sprintf("/api/metrics/%s/%d?range=%s", kind, id, url.QueryEscape(rg)), nil, &res); err != nil {
		return nil, err
	}
	pts := res.Points
	out := map[string]any{"kind": kind, "id": id, "range": res.Range, "points": len(pts)}
	if len(pts) == 0 {
		out["note"] = "no metrics yet for this range"
		return out, nil
	}
	var cpuSum, cpuMax, memSum, memMax, upSum float64
	restarts := 0
	for _, p := range pts {
		cpuSum += p.CPU
		cpuMax = max(cpuMax, p.CPU, p.CPUMax)
		memSum += p.Mem
		memMax = max(memMax, p.Mem)
		upSum += p.Up
		restarts += p.Restarts
	}
	n := float64(len(pts))
	last := pts[len(pts)-1]
	out["summary"] = map[string]any{
		"cpu_percent_avg": round1(cpuSum / n), "cpu_percent_max": round1(cpuMax),
		"memory_avg": humanBytes(int64(memSum / n)), "memory_max": humanBytes(int64(memMax)), "memory_total": humanBytes(int64(last.MemTotal)),
		"restarts": restarts, "availability_percent": round1(100 * upSum / n),
		"last": map[string]any{"at": time.Unix(last.T, 0).UTC().Format(time.RFC3339), "cpu_percent": round1(last.CPU), "memory": humanBytes(int64(last.Mem)), "disk_used": humanBytes(int64(last.Disk)), "disk_total": humanBytes(int64(last.DiskTotal)), "load": last.Load},
	}
	stride := max(1, (len(pts)+23)/24)
	var sampled []map[string]any
	for i := 0; i < len(pts); i += stride {
		p := pts[i]
		sampled = append(sampled, map[string]any{"t": time.Unix(p.T, 0).UTC().Format("2006-01-02T15:04Z"), "cpu": round1(p.CPU), "mem": humanBytes(int64(p.Mem)), "restarts": p.Restarts, "up": round1(p.Up)})
	}
	out["sampled"] = sampled
	return out, nil
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

// ---- the page the user is looking at ----

var chatPathRe = regexp.MustCompile(`^/(applications|databases|services|servers|projects)/(\d+)(?:/deployments/(\d+))?`)

// chatPageContext describes the page open in the browser, so "this
// application" or "this server" mean something to the model.
func (s *Server) chatPageContext(r *http.Request, path string) string {
	path = strings.TrimSpace(path)
	if path == "" || len(path) > 200 {
		return ""
	}
	pages := map[string]string{"/": "the dashboard", "/projects": "the Projects page", "/deployments": "the Deployments list", "/servers": "the Servers page",
		"/containers": "the Containers page (containers of every server)", "/topology": "the Topology page", "/alerts": "the Alerts page", "/uptime": "the Uptime page", "/settings": "the Settings page"}
	if p, ok := pages[strings.TrimRight(path, "/")]; ok {
		return p
	}
	if p, ok := pages["/"+strings.Trim(path, "/")]; ok {
		return p
	}
	m := chatPathRe.FindStringSubmatch(path)
	if m == nil {
		return ""
	}
	kind, id := m[1], m[2]
	gr := r.WithContext(withChatGuard(r.Context()))
	var item map[string]any
	if err := s.call(gr, "GET", "/api/"+kind+"/"+id, nil, &item); err != nil {
		return ""
	}
	name := toStr(item["name"])
	switch kind {
	case "servers":
		return fmt.Sprintf("the page of server #%s %q (status %s)", id, name, toStr(item["status"]))
	case "projects":
		return fmt.Sprintf("the page of project #%s %q", id, name)
	}
	names := s.chatEnvNames(r.Context())[int64(num(item["environment_id"]))]
	desc := fmt.Sprintf("the page of %s #%s %q (project %s, environment %s, status %s)", strings.TrimSuffix(kind, "s"), id, name, names[0], names[1], toStr(item["status"]))
	if m[3] != "" {
		desc += ", looking at deployment #" + m[3]
	}
	return desc
}

// ---- running a tool call of the model ----

// chatRunTool executes one tool call and returns what the model reads;
// the browser is told about the step as it goes.
func (s *Server) chatRunTool(r *http.Request, c chatConfig, u store.User, conv store.ChatConversation, call assistant.ToolCall, out *sse) (string, chatStep) {
	step := chatStep{ID: call.ID, Tool: call.Function.Name, Title: call.Function.Name, Status: "running", At: store.Now()}
	args := map[string]any{}
	if a := strings.TrimSpace(call.Function.Arguments); a != "" && a != "null" {
		if err := json.Unmarshal([]byte(a), &args); err != nil {
			step.Status, step.Detail = "error", "invalid arguments"
			_ = out.send("tool", step)
			return toolJSON(map[string]any{"error": "the arguments are not a JSON object: " + err.Error()}), step
		}
	}
	step.Args = args
	tool := chatToolByName(call.Function.Name)
	if tool == nil {
		step.Status, step.Detail = "error", "unknown tool"
		_ = out.send("tool", step)
		return toolJSON(map[string]any{"error": fmt.Sprintf("there is no tool %q: the assistant can only read the platform and propose the few actions in its tool list; anything else is done in the interface", call.Function.Name)}), step
	}
	step.Title = tool.Title(args)
	if tool.Action {
		if !c.Actions {
			step.Status, step.Detail = "error", "actions are disabled"
			_ = out.send("tool", step)
			return toolJSON(map[string]any{"error": "actions of the assistant are disabled by the administrator (Settings → AI assistant); tell the user how to do it in the interface"}), step
		}
		p := &pendingAction{ID: secrets.RandomToken(12), UserID: u.ID, ConvID: conv.ID, Tool: tool.Name, Args: args, Title: step.Title, CreatedAt: time.Now()}
		s.chatActs.add(p)
		step.Status, step.ActionID = "pending", p.ID
		_ = out.send("action", step)
		return toolJSON(map[string]any{"status": "awaiting_confirmation", "action": step.Title,
			"instructions": "A Confirm button is shown to the user. The action has NOT run: say in one sentence what it will do and that it waits for their confirmation. Never claim it ran."}), step
	}
	_ = out.send("tool", step)
	ctx, cancel := context.WithTimeout(r.Context(), chatToolTimeout)
	defer cancel()
	res, err := tool.Run(s, r.WithContext(withChatGuard(ctx)), args)
	if err != nil {
		step.Status, step.Detail = "error", clipStr(err.Error(), 300)
		_ = out.send("tool", step)
		return toolJSON(map[string]any{"error": err.Error()}), step
	}
	step.Status = "done"
	_ = out.send("tool", step)
	return toolJSON(res), step
}

func toolJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"the result could not be encoded"}`
	}
	if len(b) > chatToolResultMax {
		return string(b[:chatToolResultMax]) + fmt.Sprintf(" …[truncated: %d more characters; ask for less, e.g. fewer lines or a filter]", len(b)-chatToolResultMax)
	}
	return string(b)
}

// chatUpdateStep records the outcome of a confirmed or cancelled action on
// the message that proposed it.
func (s *Server) chatUpdateStep(ctx context.Context, p *pendingAction, status, detail string) chatStep {
	step := chatStep{Tool: p.Tool, Title: p.Title, Args: p.Args, Status: status, Detail: detail, ActionID: p.ID, At: store.Now()}
	if p.MessageID == 0 {
		return step
	}
	m, err := store.ByID[store.ChatMessage](ctx, s.db, "chat_messages", p.MessageID)
	if err != nil {
		return step
	}
	meta := parseChatMeta(m.Meta)
	if meta == nil {
		return step
	}
	for i := range meta.Steps {
		if meta.Steps[i].ActionID == p.ID {
			meta.Steps[i].Status, meta.Steps[i].Detail, meta.Steps[i].At = status, detail, step.At
			step = meta.Steps[i]
		}
	}
	_, _ = s.db.ExecContext(ctx, "UPDATE chat_messages SET meta = ? WHERE id = ?", meta.encode(), m.ID)
	return step
}

// confirmChatAction runs an action the assistant proposed, with the user's
// own rights, and records the outcome: POST /api/chat/actions/{id}/confirm.
func (s *Server) confirmChatAction(w http.ResponseWriter, r *http.Request) error {
	u, c, err := s.chatUser(r)
	if err != nil {
		return err
	}
	if !c.Tools || !c.Actions {
		return errStatus(403, "actions of the assistant are disabled")
	}
	p, found := s.chatActs.take(r.PathValue("id"), u.ID)
	if !found {
		return errStatus(410, "this action has expired or was already handled; ask the assistant again")
	}
	tool := chatToolByName(p.Tool)
	if tool == nil || !tool.Action {
		return errStatus(400, "unknown action")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	res, runErr := tool.Run(s, r.WithContext(withChatGuard(ctx)), p.Args)
	status, detail := "confirmed", ""
	if runErr != nil {
		status, detail = "failed", clipStr(runErr.Error(), 600)
	} else if res != nil {
		b, _ := json.Marshal(res)
		detail = clipStr(string(b), 1500)
	}
	s.activity(r, "assistant.action", fmt.Sprintf("%s — %s", p.Title, status))
	step := s.chatUpdateStep(context.WithoutCancel(r.Context()), p, status, detail)
	return ok(w, map[string]any{"status": status, "detail": detail, "step": step})
}

// cancelChatAction drops a proposed action: POST /api/chat/actions/{id}/cancel.
func (s *Server) cancelChatAction(w http.ResponseWriter, r *http.Request) error {
	u, _, err := s.chatUser(r)
	if err != nil {
		return err
	}
	p, found := s.chatActs.take(r.PathValue("id"), u.ID)
	if !found {
		return errStatus(410, "this action has expired or was already handled")
	}
	step := s.chatUpdateStep(context.WithoutCancel(r.Context()), p, "cancelled", "")
	return ok(w, map[string]any{"status": "cancelled", "step": step})
}
