package api

import (
	"strings"
	"testing"
)

// The interface's CSP forbids posting forms to other origins, so the manifest
// is posted to GitHub by a dedicated page whose policy allows that host only.
func TestGitHubManifestHandOff(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	c := e.login("owner@corp.com")
	res := e.expect(c, "POST", "/api/github/apps/manifest", map[string]string{"name": "Deploys", "organization": "portdock"}, 200)
	next, _ := res["redirect_url"].(string)
	if !strings.HasPrefix(next, "/api/github/manifest/") {
		t.Fatalf("redirect_url: %v", res)
	}

	code, body, hdr := e.raw(c, next)
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/html") {
		t.Fatalf("hand-off page: %d %v", code, hdr)
	}
	csp := hdr.Get("Content-Security-Policy")
	if !strings.Contains(csp, "form-action https://github.com;") || !strings.Contains(csp, "script-src 'nonce-") {
		t.Fatalf("CSP: %q", csp)
	}
	if !strings.Contains(body, `action="https://github.com/organizations/portdock/settings/apps/new?state=`) || !strings.Contains(body, `name="manifest"`) || !strings.Contains(body, "&#34;redirect_url&#34;") {
		t.Fatalf("body: %s", body)
	}

	// Other people cannot open it, and an unknown state goes back to the settings.
	if code, _, hdr := e.raw(e.client(), next); code != 302 || !strings.HasPrefix(hdr.Get("Location"), "/login") {
		t.Fatalf("anonymous: %d %v", code, hdr)
	}
	if code, _, hdr := e.raw(c, "/api/github/manifest/nope"); code != 302 || !strings.Contains(hdr.Get("Location"), "error=") {
		t.Fatalf("unknown state: %d %v", code, hdr)
	}
}
