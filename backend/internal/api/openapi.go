package api

import (
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/portdock/portdock/internal/store"
)

// The OpenAPI document is generated from the routes as they are registered
// (Handler records each one with its access rule), so it cannot drift from
// the code. Schemas of the main resources come from their Go types.

type routeInfo struct {
	Pattern string // "GET /api/applications/{id}" or a path without method
	Access  string // public | user | admin | project
	Perm    perm   // project permission (project routes)
}

// textBody marks an operation answering text of this media type.
type textBody string

// opSchemas maps operations to the Go types of their body and response; a
// slice type means a JSON array.
var opSchemas = map[string]struct {
	Request, Response any
	Summary           string
}{
	"GET /api/metrics":                       {nil, textBody("text/plain; version=0.0.4"), "Metrics in the Prometheus text format"},
	"GET /api/projects":                      {nil, []store.Project{}, "List projects"},
	"POST /api/projects":                     {store.Project{}, store.Project{}, "Create a project"},
	"GET /api/applications":                  {nil, []store.Application{}, "List applications"},
	"POST /api/applications":                 {store.Application{}, store.Application{}, "Create an application"},
	"GET /api/applications/{id}":             {nil, store.Application{}, "Get an application"},
	"PATCH /api/applications/{id}":           {store.Application{}, store.Application{}, "Update an application"},
	"POST /api/applications/{id}/deploy":     {deployInput{}, store.Deployment{}, "Deploy an application"},
	"GET /api/applications/{id}/deployments": {nil, []store.Deployment{}, "List the deployments of an application"},
	"GET /api/deployments/{id}":              {nil, store.Deployment{}, "Get a deployment (with its log)"},
	"GET /api/applications/{id}/envs":        {nil, []store.EnvVar{}, "List the variables of an application"},
	"POST /api/applications/{id}/envs":       {store.EnvVar{}, store.EnvVar{}, "Add a variable"},
	"POST /api/applications/{id}/domains":    {domainInput{}, domainsResponse{}, "Add a domain"},
	"GET /api/applications/{id}/domains":     {nil, domainsResponse{}, "List the domains of an application"},
	"GET /api/applications/{id}/volumes":     {nil, volumesResponse{}, "List persistent volumes"},
	"POST /api/applications/{id}/volumes":    {volumeInput{}, volumesResponse{}, "Add a persistent volume"},
	"GET /api/applications/{id}/jobs":        {nil, []jobDTO{}, "List scheduled jobs"},
	"POST /api/applications/{id}/jobs":       {jobInput{}, jobDTO{}, "Create a scheduled job"},
	"POST /api/jobs/{id}/run":                {nil, store.JobRun{}, "Run a job now"},
	"GET /api/jobs/{id}/runs":                {nil, []store.JobRun{}, "List the runs of a job"},
	"GET /api/job-runs/{id}":                 {nil, store.JobRun{}, "Get a job run with its output"},
	"GET /api/applications/{id}/protection":  {nil, protectionDTO{}, "Get the access protection of an application"},
	"GET /api/tokens":                        {nil, []store.APIToken{}, "List your API tokens"},
	"POST /api/tokens":                       {tokenInput{}, nil, "Create an API token"},
	"GET /api/service-accounts":              {nil, []serviceAccountDTO{}, "List service accounts"},
	"POST /api/service-accounts/{id}/tokens": {tokenInput{}, nil, "Create a token for a service account"},
	"GET /api/system/backup":                 {nil, instanceBackupDTO{}, "Instance backup settings and files"},
	"GET /api/system/backup/files/{name}":    {nil, nil, "Download an instance backup"},
	"POST /api/gitops/apply":                 {nil, nil, "Apply a portdock.yaml (dry_run=true returns the plan)"},
	"GET /api/applications/{id}/export":      {nil, nil, "Export an application as portdock.yaml"},
	"GET /api/databases":                     {nil, []store.Database{}, "List databases"},
	"GET /api/services":                      {nil, []store.Service{}, "List services"},
}

type deployInput struct {
	ForceRebuild bool   `json:"force_rebuild"`
	CommitSHA    string `json:"commit_sha,omitempty"`
	Branch       string `json:"branch,omitempty"`
}

var (
	openAPIOnce sync.Once
	openAPIDoc  map[string]any
)

// scopeFor is the smallest token scope that may call a route.
func scopeFor(r routeInfo, method string) string {
	switch r.Access {
	case "public":
		return ""
	case "admin":
		return "full"
	case "project":
		for name, p := range tokenScopes {
			if p == r.Perm {
				return name
			}
		}
	}
	if unsafeMethod(method) {
		return "write"
	}
	return "read"
}

var pathParamRe = regexp.MustCompile(`\{([a-z_]+)\}`)

func (s *Server) openAPI(w http.ResponseWriter, r *http.Request) error {
	openAPIOnce.Do(func() { openAPIDoc = buildOpenAPI(s.routes) })
	doc := map[string]any{}
	for k, v := range openAPIDoc {
		doc[k] = v
	}
	doc["servers"] = []map[string]string{{"url": s.publicURL(r.Context())}}
	return ok(w, doc)
}

func buildOpenAPI(routes []routeInfo) map[string]any {
	schemas := map[string]any{}
	paths := map[string]map[string]any{}
	for _, rt := range routes {
		methods := []string{}
		path := rt.Pattern
		if m, p, found := strings.Cut(rt.Pattern, " "); found {
			methods, path = []string{m}, p
		} else {
			methods = []string{"GET", "POST"} // routes answering any method
		}
		if !strings.HasPrefix(path, "/api/") {
			continue
		}
		for _, method := range methods {
			op := map[string]any{
				"summary":     summaryFor(method, path),
				"operationId": operationID(method, path),
				"tags":        []string{tagFor(path)},
				"responses":   map[string]any{"200": map[string]any{"description": "OK"}},
			}
			addErrors := func() {
				resp := op["responses"].(map[string]any)
				for code, desc := range map[string]string{"400": "Invalid request", "401": "Not signed in, or the token expired", "403": "Not allowed (role or token scope)", "404": "Not found or not visible"} {
					resp[code] = map[string]any{"description": desc, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Error"}}}}
				}
			}
			key := method + " " + path
			if sc, ok := opSchemas[key]; ok {
				if sc.Summary != "" {
					op["summary"] = sc.Summary
				}
				if sc.Request != nil {
					op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": schemaFor(reflect.TypeOf(sc.Request), schemas)}}}
				}
				if t, isText := sc.Response.(textBody); isText {
					op["responses"] = map[string]any{"200": map[string]any{"description": "OK", "content": map[string]any{string(t): map[string]any{"schema": map[string]string{"type": "string"}}}}}
				} else if sc.Response != nil {
					op["responses"] = map[string]any{"200": map[string]any{"description": "OK", "content": map[string]any{"application/json": map[string]any{"schema": schemaFor(reflect.TypeOf(sc.Response), schemas)}}}}
				}
			}
			var params []map[string]any
			for _, m := range pathParamRe.FindAllStringSubmatch(path, -1) {
				params = append(params, map[string]any{"name": m[1], "in": "path", "required": true, "schema": map[string]string{"type": "string"}})
			}
			if params != nil {
				op["parameters"] = params
			}
			if rt.Access == "public" {
				op["security"] = []any{}
			} else {
				op["x-token-scope"] = scopeFor(rt, method)
				op["x-access"] = rt.Access
			}
			addErrors()
			if paths[path] == nil {
				paths[path] = map[string]any{}
			}
			paths[path][strings.ToLower(method)] = op
		}
	}
	schemas["Error"] = map[string]any{"type": "object", "properties": map[string]any{"error": map[string]any{"type": "string"}}}
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "PortDock API",
			"version": Version,
			"license": map[string]any{"name": "Apache-2.0", "identifier": "Apache-2.0"},
			"description": "Every route of PortDock. Authenticate with `Authorization: Bearer <token>` (Settings → API tokens). " +
				"`x-token-scope` is the smallest token scope that may call an operation (read < deploy < write < full); " +
				"`x-access` is user (any signed-in account), project (project role) or admin.",
		},
		"paths": paths,
		"components": map[string]any{
			"schemas": schemas,
			"securitySchemes": map[string]any{
				"bearer":  map[string]any{"type": "http", "scheme": "bearer"},
				"session": map[string]any{"type": "apiKey", "in": "cookie", "name": "portdock_session"},
			},
		},
		"security": []any{map[string]any{"bearer": []string{}}, map[string]any{"session": []string{}}},
	}
}

// schemaFor returns the JSON schema of t, registering named structs in components.
func schemaFor(t reflect.Type, components map[string]any) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": schemaFor(t.Elem(), components)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaFor(t.Elem(), components)}
	case reflect.Struct:
		name := t.Name()
		if name != "" {
			if _, done := components[name]; !done {
				components[name] = map[string]any{} // break cycles
				components[name] = structSchema(t, components)
			}
			return map[string]any{"$ref": "#/components/schemas/" + name}
		}
		return structSchema(t, components)
	}
	return map[string]any{}
}

func structSchema(t reflect.Type, components map[string]any) map[string]any {
	props := map[string]any{}
	var collect func(t reflect.Type)
	collect = func(t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			if f.Anonymous && tag == "" {
				ft := f.Type
				for ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					collect(ft)
					continue
				}
			}
			if !f.IsExported() || tag == "-" {
				continue
			}
			name, _, _ := strings.Cut(tag, ",")
			if name == "" {
				name = f.Name
			}
			props[name] = schemaFor(f.Type, components)
		}
	}
	collect(t)
	return map[string]any{"type": "object", "properties": props}
}

func tagFor(path string) string {
	seg := strings.Split(strings.TrimPrefix(path, "/api/"), "/")[0]
	return strings.ReplaceAll(seg, "-", " ")
}

func operationID(method, path string) string {
	p := strings.NewReplacer("/api/", "", "/", "_", "{", "", "}", "", "-", "_", ".", "_").Replace(path)
	return strings.ToLower(method) + "_" + strings.Trim(p, "_")
}

func singular(s string) string {
	switch {
	case strings.HasSuffix(s, "ies"):
		return strings.TrimSuffix(s, "ies") + "y"
	case strings.HasSuffix(s, "ses"):
		return strings.TrimSuffix(s, "es")
	case strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss"):
		return strings.TrimSuffix(s, "s")
	}
	return s
}

// summaryFor describes an operation from its method and path.
func summaryFor(method, path string) string {
	segs := strings.Split(strings.TrimPrefix(path, "/api/"), "/")
	var literals []string
	for _, s := range segs {
		if !strings.HasPrefix(s, "{") {
			literals = append(literals, strings.ReplaceAll(s, "-", " "))
		}
	}
	last := segs[len(segs)-1]
	resource := literals[len(literals)-1]
	byID := strings.HasPrefix(last, "{")
	switch {
	case method == "GET" && byID:
		return "Get " + singular(resource)
	case method == "GET":
		if len(literals) > 1 {
			return strings.ToUpper(resource[:1]) + resource[1:] + " of " + singular(literals[0])
		}
		return "List " + resource
	case method == "POST" && byID:
		return strings.ToUpper(method[:1]) + strings.ToLower(method[1:]) + " " + singular(resource)
	case method == "POST" && len(literals) > 1 && !byID:
		return strings.ToUpper(resource[:1]) + resource[1:] + " (" + singular(literals[0]) + ")"
	case method == "POST":
		return "Create " + singular(resource)
	case method == "PATCH", method == "PUT":
		return "Update " + singular(resource)
	case method == "DELETE":
		return "Delete " + singular(resource)
	}
	return method + " " + path
}

// sortedPaths is used by tests to list the document deterministically.
func sortedPaths(doc map[string]any) []string {
	paths := doc["paths"].(map[string]map[string]any)
	out := make([]string, 0, len(paths))
	for p := range paths {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
