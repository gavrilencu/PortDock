package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// The UI iterates over list fields without null checks, so a nil Go slice that
// reaches the browser as null crashes the page. This walks the read endpoints
// of a fresh instance and fails on any null value.
func TestReadEndpointsHaveNoNulls(t *testing.T) {
	paths := []string{
		"/api/activity", "/api/alert-rules", "/api/alerts", "/api/applications", "/api/auth/2fa", "/api/auth/me",
		"/api/auth/providers", "/api/auth/sessions", "/api/auth/status", "/api/certificates", "/api/dashboard",
		"/api/database-engines", "/api/databases", "/api/deployments", "/api/github/apps", "/api/ldap/directories",
		"/api/notifications", "/api/projects", "/api/registries", "/api/servers", "/api/service-templates",
		"/api/services", "/api/settings", "/api/settings/audit", "/api/settings/certificates", "/api/ssh-keys",
		"/api/sso/providers", "/api/system/update", "/api/tokens", "/api/users", "/api/image-catalog",
	}
	{
		e := newTestEnv(t)
		e.user("owner@example.com", "correct-horse", "owner")
		c := e.login("owner@example.com")
		for _, p := range paths {
			req, _ := http.NewRequest("GET", e.srv.URL+p, nil)
			res, err := c.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != 200 {
				continue // endpoints needing Docker or GitHub are covered elsewhere
			}
			var v any
			if json.Unmarshal(raw, &v) != nil {
				continue
			}
			for _, n := range findNulls(v, "") {
				if allowedNulls[p+" "+n] {
					continue
				}
				t.Errorf("GET %s: %s is null", p, n)
			}
		}
	}
}

// Nulls the UI handles explicitly.
var allowedNulls = map[string]bool{
	"/api/system/update .latest":      true, // no release found yet
	"/api/system/update .state.steps": true, // no update has run
}

func findNulls(v any, path string) []string {
	switch x := v.(type) {
	case nil:
		return []string{path}
	case map[string]any:
		var out []string
		for k, val := range x {
			out = append(out, findNulls(val, path+"."+k)...)
		}
		return out
	case []any:
		var out []string
		for i, val := range x {
			out = append(out, findNulls(val, fmt.Sprintf("%s[%d]", path, i))...)
		}
		return out
	}
	return nil
}
