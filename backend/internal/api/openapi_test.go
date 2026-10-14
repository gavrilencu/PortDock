package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
)

// The OpenAPI document lists every route, with the token scope it needs.
func TestOpenAPIDocument(t *testing.T) {
	e := newTestEnv(t)
	resp, err := http.Get(e.srv.URL + "/api/openapi.json")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("openapi: %v %v", err, resp)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if out := os.Getenv("PORTDOCK_OPENAPI_OUT"); out != "" {
		_ = os.WriteFile(out, b, 0o644) // for linting with standard tools
	}
	var doc struct {
		OpenAPI string                               `json:"openapi"`
		Paths   map[string]map[string]map[string]any `json:"paths"`
		Comp    struct {
			Schemas map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.OpenAPI != "3.1.0" || len(doc.Paths) < 200 {
		t.Fatalf("document: %s, %d paths", doc.OpenAPI, len(doc.Paths))
	}
	for path, want := range map[string]map[string]string{
		"/api/applications/{id}/deploy": {"post": "deploy"},
		"/api/applications/{id}":        {"get": "read", "patch": "write", "delete": "full"},
		"/api/users":                    {"get": "full"},
		"/api/projects":                 {"get": "read", "post": "write"},
	} {
		for method, scope := range want {
			op := doc.Paths[path][method]
			if op == nil || op["x-token-scope"] != scope {
				t.Errorf("%s %s: %v", method, path, op)
			}
		}
	}
	if op := doc.Paths["/api/health"]["get"]; op == nil || len(op["security"].([]any)) != 0 {
		t.Errorf("health must be public: %v", op)
	}
	app := doc.Comp.Schemas["Application"].Properties
	if app["name"] == nil || app["git_repository"] == nil || app["git_token"] != nil {
		t.Errorf("Application schema: %v", app)
	}
}
