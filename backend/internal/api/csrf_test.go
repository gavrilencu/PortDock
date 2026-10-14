package api

import (
	"bytes"
	"net/http"
	"testing"
)

// Browsers sending a request from another site (an application on a sibling
// subdomain, say) cannot use the administrator's session cookie.
func TestCrossSiteRequestsRefused(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	c := e.login("owner@corp.com")
	post := func(path string, headers map[string]string) int {
		req, _ := http.NewRequest("POST", e.srv.URL+path, bytes.NewReader([]byte(`{"name":"x"}`)))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for _, h := range []map[string]string{
		{"Sec-Fetch-Site": "cross-site"},
		{"Sec-Fetch-Site": "same-site", "Origin": "https://app.example.com"},
		{"Origin": "https://evil.example.com"},
		{"Origin": "null"},
	} {
		if code := post("/api/projects", h); code != 403 {
			t.Errorf("%v: got %d, want 403", h, code)
		}
	}
	for _, h := range []map[string]string{
		{"Sec-Fetch-Site": "same-origin", "Origin": e.srv.URL},
		{"Origin": e.srv.URL},
		{}, // not a browser
	} {
		if code := post("/api/projects", h); code != 200 {
			t.Errorf("%v: got %d, want 200", h, code)
		}
	}
	// Webhooks come from other servers.
	if code := post("/api/webhooks/deploy/unknown", map[string]string{"Sec-Fetch-Site": "cross-site"}); code == 403 {
		t.Error("webhook refused as cross-site")
	}
}
