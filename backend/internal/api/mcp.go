package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/store"
)

// MCP server (Model Context Protocol, Streamable HTTP transport): AI agents
// such as Claude or Cursor list resources, deploy, read logs and roll back
// through POST /api/mcp with an API token. Every tool goes through the API
// with the caller's credentials, so roles, token scopes and deployment
// approvals apply exactly as in the interface.

const mcpProtocol = "2025-06-18"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// mcpTool describes a tool and runs it.
type mcpTool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations"`
	run         func(s *Server, r *http.Request, args map[string]any) (any, error)
}

func schema(required []string, props map[string]any) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func readOnly() map[string]any { return map[string]any{"readOnlyHint": true, "openWorldHint": false} }

func acts(destructive bool) map[string]any {
	return map[string]any{"readOnlyHint": false, "destructiveHint": destructive, "openWorldHint": false}
}

var mcpTools = []mcpTool{
	{Name: "list_projects", Title: "List projects", Description: "Projects and their environments the token can see.",
		InputSchema: schema(nil, map[string]any{}), Annotations: readOnly(),
		run: func(s *Server, r *http.Request, _ map[string]any) (any, error) {
			var out []map[string]any
			return out, s.call(r, "GET", "/api/projects", nil, &out)
		}},
	{Name: "list_applications", Title: "List applications", Description: "Applications with their id, project, environment, status, source and image.",
		InputSchema: schema(nil, map[string]any{"project": str("Optional project name to filter on.")}), Annotations: readOnly(),
		run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
			apps, err := s.mcpApps(r)
			if err != nil {
				return nil, err
			}
			out := []map[string]any{}
			for _, a := range apps {
				if p, _ := args["project"].(string); p != "" && !strings.EqualFold(p, a.project) {
					continue
				}
				out = append(out, map[string]any{"id": a.ID, "name": a.Name, "project": a.project, "environment": a.environment,
					"status": a.Status, "source": a.SourceType, "repository": a.GitRepository, "branch": a.GitBranch, "image": a.DockerImage, "current_image": a.CurrentImage})
			}
			return out, nil
		}},
	{Name: "get_application", Title: "Get an application", Description: "Settings, status, domains and the last deployments of an application.",
		InputSchema: schema([]string{"app"}, map[string]any{"app": str("Application id, name, project/name or project/environment/name.")}), Annotations: readOnly(),
		run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
			id, err := s.mcpApp(r, args)
			if err != nil {
				return nil, err
			}
			var app map[string]any
			if err := s.call(r, "GET", fmt.Sprintf("/api/applications/%d", id), nil, &app); err != nil {
				return nil, err
			}
			var deps []map[string]any
			_ = s.call(r, "GET", fmt.Sprintf("/api/applications/%d/deployments", id), nil, &deps)
			if len(deps) > 5 {
				deps = deps[:5]
			}
			keep := map[string]any{"deployments": deps}
			for _, k := range []string{"id", "name", "status", "source_type", "git_repository", "git_branch", "docker_image", "current_image", "build_pack", "port", "replicas", "domains", "health_check_path", "cpu_limit", "memory_limit", "canary_percent", "environment", "project"} {
				if v, ok := app[k]; ok {
					keep[k] = v
				}
			}
			return keep, nil
		}},
	{Name: "deploy_application", Title: "Deploy an application", Description: "Starts a deployment of the application's branch (or of commit), like the Deploy button. In a protected environment it may wait for a maintainer's approval. With wait, returns when it finishes (up to 10 minutes).",
		InputSchema: schema([]string{"app"}, map[string]any{"app": str("Application id or name."), "commit": str("Optional commit SHA."),
			"wait": map[string]any{"type": "boolean", "description": "Wait for the deployment to finish."}}), Annotations: acts(false),
		run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
			id, err := s.mcpApp(r, args)
			if err != nil {
				return nil, err
			}
			body := map[string]any{}
			if c, _ := args["commit"].(string); c != "" {
				body["commit_sha"] = c
			}
			var d map[string]any
			if err := s.call(r, "POST", fmt.Sprintf("/api/applications/%d/deploy", id), body, &d); err != nil {
				return nil, err
			}
			if w, _ := args["wait"].(bool); w {
				return s.mcpWaitDeployment(r, int64(num(d["id"])))
			}
			return map[string]any{"deployment_id": d["id"], "status": d["status"]}, nil
		}},
	{Name: "get_deployment", Title: "Get a deployment", Description: "Status of a deployment and the end of its build and rollout log.",
		InputSchema: schema([]string{"deployment_id"}, map[string]any{"deployment_id": map[string]any{"type": "integer"},
			"lines": map[string]any{"type": "integer", "description": "Log lines to return (default 80)."}}), Annotations: readOnly(),
		run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
			return s.mcpDeployment(r, int64(num(args["deployment_id"])), int(num(args["lines"])))
		}},
	{Name: "list_deployments", Title: "List deployments", Description: "The last deployments of an application, newest first.",
		InputSchema: schema([]string{"app"}, map[string]any{"app": str("Application id or name.")}), Annotations: readOnly(),
		run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
			id, err := s.mcpApp(r, args)
			if err != nil {
				return nil, err
			}
			var deps []map[string]any
			if err := s.call(r, "GET", fmt.Sprintf("/api/applications/%d/deployments", id), nil, &deps); err != nil {
				return nil, err
			}
			out := []map[string]any{}
			for i, d := range deps {
				if i == 20 {
					break
				}
				out = append(out, map[string]any{"id": d["id"], "status": d["status"], "trigger": d["trigger"], "commit": d["commit_sha"], "message": d["commit_message"], "created_at": d["created_at"], "finished_at": d["finished_at"]})
			}
			return out, nil
		}},
	{Name: "rollback_deployment", Title: "Roll back", Description: "Redeploys the image of an earlier successful deployment, without rebuilding.",
		InputSchema: schema([]string{"deployment_id"}, map[string]any{"deployment_id": map[string]any{"type": "integer", "description": "The deployment to go back to."}}), Annotations: acts(false),
		run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
			var d map[string]any
			if err := s.call(r, "POST", fmt.Sprintf("/api/deployments/%d/rollback", int64(num(args["deployment_id"]))), map[string]any{}, &d); err != nil {
				return nil, err
			}
			return map[string]any{"deployment_id": d["id"], "status": d["status"]}, nil
		}},
	{Name: "application_logs", Title: "Application logs", Description: "The last lines written by the application's running container.",
		InputSchema: schema([]string{"app"}, map[string]any{"app": str("Application id or name."), "lines": map[string]any{"type": "integer", "description": "Lines (default 100, at most 1000)."}}), Annotations: readOnly(),
		run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
			id, err := s.mcpApp(r, args)
			if err != nil {
				return nil, err
			}
			return s.mcpLogs(r, id, int(num(args["lines"])))
		}},
	{Name: "restart_application", Title: "Restart an application", Description: "Restarts the application's containers (no new deployment).",
		InputSchema: schema([]string{"app"}, map[string]any{"app": str("Application id or name.")}), Annotations: acts(false),
		run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
			id, err := s.mcpApp(r, args)
			if err != nil {
				return nil, err
			}
			return map[string]any{"restarted": true}, s.call(r, "POST", fmt.Sprintf("/api/applications/%d/restart", id), map[string]any{}, nil)
		}},
	{Name: "list_databases", Title: "List databases", Description: "Databases with their engine, status and project.",
		InputSchema: schema(nil, map[string]any{}), Annotations: readOnly(),
		run: func(s *Server, r *http.Request, _ map[string]any) (any, error) {
			var dbs []map[string]any
			if err := s.call(r, "GET", "/api/databases", nil, &dbs); err != nil {
				return nil, err
			}
			out := []map[string]any{}
			for _, d := range dbs {
				out = append(out, map[string]any{"id": d["id"], "name": d["name"], "engine": d["engine"], "status": d["status"], "environment_id": d["environment_id"]})
			}
			return out, nil
		}},
	{Name: "backup_database", Title: "Back up a database", Description: "Takes a backup of the database now.",
		InputSchema: schema([]string{"database_id"}, map[string]any{"database_id": map[string]any{"type": "integer"}}), Annotations: acts(false),
		run: func(s *Server, r *http.Request, args map[string]any) (any, error) {
			var run map[string]any
			if err := s.call(r, "POST", fmt.Sprintf("/api/databases/%d/backup-now", int64(num(args["database_id"]))), map[string]any{}, &run); err != nil {
				return nil, err
			}
			return map[string]any{"backup_run_id": run["id"], "status": run["status"]}, nil
		}},
	{Name: "list_servers", Title: "List servers", Description: "Servers with their status and Docker version.",
		InputSchema: schema(nil, map[string]any{}), Annotations: readOnly(),
		run: func(s *Server, r *http.Request, _ map[string]any) (any, error) {
			var srvs []map[string]any
			if err := s.call(r, "GET", "/api/servers", nil, &srvs); err != nil {
				return nil, err
			}
			out := []map[string]any{}
			for _, x := range srvs {
				out = append(out, map[string]any{"id": x["id"], "name": x["name"], "host": x["host"], "status": x["status"], "docker_version": x["docker_version"]})
			}
			return out, nil
		}},
	{Name: "list_alerts", Title: "List alerts", Description: "Alerts of the alert rules, firing ones first.",
		InputSchema: schema(nil, map[string]any{}), Annotations: readOnly(),
		run: func(s *Server, r *http.Request, _ map[string]any) (any, error) {
			var out any
			return out, s.call(r, "GET", "/api/alerts", nil, &out)
		}},
}

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	}
	return 0
}

type mcpAppRef struct {
	store.Application
	project, environment string
}

// mcpApps lists the applications the caller can see.
func (s *Server) mcpApps(r *http.Request) ([]mcpAppRef, error) {
	var apps []store.Application
	if err := s.call(r, "GET", "/api/applications", nil, &apps); err != nil {
		return nil, err
	}
	envs, _ := store.Select[store.Environment](r.Context(), s.db, "SELECT * FROM environments")
	projects, _ := store.Select[store.Project](r.Context(), s.db, "SELECT * FROM projects")
	pname := map[int64]string{}
	for _, p := range projects {
		pname[p.ID] = p.Name
	}
	eref := map[int64][2]string{}
	for _, e := range envs {
		eref[e.ID] = [2]string{pname[e.ProjectID], e.Name}
	}
	out := make([]mcpAppRef, 0, len(apps))
	for _, a := range apps {
		ref := eref[a.EnvironmentID]
		out = append(out, mcpAppRef{Application: a, project: ref[0], environment: ref[1]})
	}
	return out, nil
}

// mcpApp resolves the "app" argument: an id, a name, project/name or project/environment/name.
func (s *Server) mcpApp(r *http.Request, args map[string]any) (int64, error) {
	ref := strings.TrimSpace(fmt.Sprint(args["app"]))
	if ref == "" || ref == "<nil>" {
		return 0, errors.New("app is required")
	}
	if n, err := strconv.ParseInt(ref, 10, 64); err == nil {
		return n, nil
	}
	if f, ok := args["app"].(float64); ok {
		return int64(f), nil
	}
	apps, err := s.mcpApps(r)
	if err != nil {
		return 0, err
	}
	parts := strings.Split(ref, "/")
	var found []mcpAppRef
	for _, a := range apps {
		match := false
		switch len(parts) {
		case 1:
			match = strings.EqualFold(a.Name, parts[0])
		case 2:
			match = strings.EqualFold(a.project, parts[0]) && strings.EqualFold(a.Name, parts[1])
		case 3:
			match = strings.EqualFold(a.project, parts[0]) && strings.EqualFold(a.environment, parts[1]) && strings.EqualFold(a.Name, parts[2])
		}
		if match {
			found = append(found, a)
		}
	}
	switch len(found) {
	case 0:
		return 0, fmt.Errorf("no application %q", ref)
	case 1:
		return found[0].ID, nil
	}
	var names []string
	for _, a := range found {
		names = append(names, a.project+"/"+a.environment+"/"+a.Name)
	}
	return 0, fmt.Errorf("%q matches several applications: %s", ref, strings.Join(names, ", "))
}

func (s *Server) mcpDeployment(r *http.Request, id int64, lines int) (any, error) {
	var d map[string]any
	if err := s.call(r, "GET", fmt.Sprintf("/api/deployments/%d", id), nil, &d); err != nil {
		return nil, err
	}
	if lines <= 0 {
		lines = 80
	}
	logs, _ := d["logs"].(string)
	ls := strings.Split(strings.TrimRight(logs, "\n"), "\n")
	if len(ls) > lines {
		ls = ls[len(ls)-lines:]
	}
	return map[string]any{"id": d["id"], "status": d["status"], "trigger": d["trigger"], "commit": d["commit_sha"], "image": d["image"],
		"created_at": d["created_at"], "finished_at": d["finished_at"], "canary_status": d["canary_status"], "log_tail": strings.Join(ls, "\n")}, nil
}

func (s *Server) mcpWaitDeployment(r *http.Request, id int64) (any, error) {
	deadline := time.Now().Add(10 * time.Minute)
	for {
		res, err := s.mcpDeployment(r, id, 60)
		if err != nil {
			return nil, err
		}
		st, _ := res.(map[string]any)["status"].(string)
		if st != "queued" && st != "running" || time.Now().After(deadline) {
			return res, nil
		}
		select {
		case <-r.Context().Done():
			return res, nil
		case <-time.After(3 * time.Second):
		}
	}
}

// mcpLogs reads the end of the logs of the application's running container,
// after the API confirmed the caller may read the application.
func (s *Server) mcpLogs(r *http.Request, id int64, lines int) (any, error) {
	if err := s.call(r, "GET", fmt.Sprintf("/api/applications/%d", id), nil, nil); err != nil {
		return nil, err
	}
	if lines <= 0 {
		lines = 100
	}
	lines = min(lines, 1000)
	a, err := store.ByID[store.Application](r.Context(), s.db, "applications", id)
	if err != nil {
		return nil, err
	}
	cs, err := s.engine.AppContainers(r.Context(), a)
	if err != nil {
		return nil, err
	}
	var pick *docker.Container
	for i, c := range cs {
		if c.State == "running" && (pick == nil || c.Labels[docker.LabelPR] == "0") {
			pick = &cs[i]
		}
	}
	if pick == nil {
		return nil, errors.New("no running container: deploy the application first")
	}
	serverID := pick.ServerID
	if serverID == 0 {
		serverID = a.ServerID
	}
	x, _, err := s.pool.ForID(r.Context(), serverID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var buf bytes.Buffer
	if err := docker.Logs(ctx, x, pick.Name, lines, false, true, &buf); err != nil && buf.Len() == 0 {
		return nil, err
	}
	return map[string]any{"container": pick.Name, "logs": buf.String()}, nil
}

// mcp answers the JSON-RPC messages of an MCP client.
func (s *Server) mcp(w http.ResponseWriter, r *http.Request) error {
	if auth.TokenFrom(r.Context()) == nil {
		// Only API tokens: a browser session must not be usable by a page
		// that makes the browser post here.
		return errStatus(401, "the MCP endpoint needs an API token (Authorization: Bearer pd_…)")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	var batch []rpcRequest
	single := !bytes.HasPrefix(bytes.TrimSpace(body), []byte("["))
	if single {
		var one rpcRequest
		if err := json.Unmarshal(body, &one); err != nil {
			return writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
		}
		batch = []rpcRequest{one}
	} else if err := json.Unmarshal(body, &batch); err != nil {
		return writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
	}
	var out []rpcResponse
	for _, m := range batch {
		if len(m.ID) == 0 { // a notification: nothing to answer
			continue
		}
		out = append(out, s.mcpHandle(r, m))
	}
	if len(out) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return nil
	}
	if single {
		return writeRPC(w, out[0])
	}
	return writeRPC(w, out)
}

func writeRPC(w http.ResponseWriter, v any) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(v)
}

func (s *Server) mcpHandle(r *http.Request, m rpcRequest) rpcResponse {
	res := rpcResponse{JSONRPC: "2.0", ID: m.ID}
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(m.Params, &p)
		version := mcpProtocol
		if p.ProtocolVersion == "2025-03-26" || p.ProtocolVersion == "2024-11-05" {
			version = p.ProtocolVersion
		}
		res.Result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "portdock", "title": "PortDock", "version": Version},
			"instructions": "PortDock deploys applications, databases and services on the organization's servers. " +
				"Use list_applications to find an application, deploy_application to deploy it (with wait to follow it), get_deployment for its log, " +
				"application_logs for runtime logs and rollback_deployment to go back. Actions run with the permissions of the API token.",
		}
	case "ping":
		res.Result = map[string]any{}
	case "tools/list":
		res.Result = map[string]any{"tools": mcpTools}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			res.Error = &rpcError{-32602, "invalid params"}
			return res
		}
		var tool *mcpTool
		for i := range mcpTools {
			if mcpTools[i].Name == p.Name {
				tool = &mcpTools[i]
			}
		}
		if tool == nil {
			res.Error = &rpcError{-32602, "unknown tool " + p.Name}
			return res
		}
		if p.Arguments == nil {
			p.Arguments = map[string]any{}
		}
		out, err := tool.run(s, r, p.Arguments)
		if err != nil {
			res.Result = map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": err.Error()}}}
			return res
		}
		text, _ := json.MarshalIndent(out, "", "  ")
		res.Result = map[string]any{"content": []map[string]any{{"type": "text", "text": string(text)}}, "isError": false}
		if obj, isObj := out.(map[string]any); isObj {
			res.Result.(map[string]any)["structuredContent"] = obj
		}
		if ro, _ := tool.Annotations["readOnlyHint"].(bool); !ro {
			s.activity(r, "mcp.tool", p.Name)
		}
	default:
		res.Error = &rpcError{-32601, "method not found: " + m.Method}
	}
	return res
}
