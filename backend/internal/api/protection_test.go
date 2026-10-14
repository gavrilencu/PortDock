package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestAccessProtectionSettings(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	c := e.login("owner@corp.com")
	p := e.expect(c, "POST", "/api/projects", map[string]any{"name": "shop"}, 200)
	env := p["environments"].([]any)[0].(map[string]any)["id"]
	app := e.expect(c, "POST", "/api/applications", map[string]any{"name": "admin", "environment_id": env, "server_id": 1, "source_type": "image", "docker_image": "nginx"}, 200)
	base := fmt.Sprintf("/api/applications/%v/protection", app["id"])

	for _, bad := range []map[string]any{
		{"auth": "magic"},
		{"ip_allow": "10.0.0.0/33"},
		{"ip_allow": "not-an-ip"},
		{"auth": "password"}, // no user
		{"auth": "password", "users": []map[string]any{{"user": "ops"}}},                            // no password
		{"auth": "password", "users": []map[string]any{{"user": "ops", "password": "short"}}},       // too short
		{"auth": "password", "users": []map[string]any{{"user": "o p", "password": "long enough"}}}, // bad name
		{"auth": "sso", "sso_emails": "not an email"},
		{"ip_depth": 9},
	} {
		e.expect(c, "PUT", base, bad, 400)
	}
	res := e.expect(c, "PUT", base, map[string]any{"auth": "password", "ip_allow": "10.0.0.0/8\n203.0.113.7", "rate_average": 10, "users": []map[string]any{{"user": "ops", "password": "long enough"}}}, 200)
	prot := res["protection"].(map[string]any)
	if prot["auth"] != "password" || fmt.Sprint(prot["users"]) != "[ops]" || prot["basic_users"] != nil {
		t.Fatalf("saved: %v", prot)
	}
	// Keeping the user without retyping the password keeps its hash.
	e.expect(c, "PUT", base, map[string]any{"auth": "password", "users": []map[string]any{{"user": "ops"}}}, 200)
	var stored string
	_ = e.db.QueryRow("SELECT basic_users FROM app_protection WHERE application_id = ?", app["id"]).Scan(&stored)
	if !strings.Contains(stored, `"user":"ops"`) || !strings.Contains(stored, "$2a$") || strings.Contains(stored, "long enough") {
		t.Fatalf("stored users: %s", stored)
	}
}

func TestForwardAuthSingleSignOn(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	dev := e.user("dev@corp.com", "correct-horse", "member")
	e.user("outsider@other.com", "correct-horse", "member")
	if _, err := e.db.Exec("INSERT INTO servers(id, name, host, status) VALUES (1, 'local', 'localhost', 'reachable')"); err != nil {
		t.Fatal(err)
	}
	owner := e.login("owner@corp.com")
	p := e.expect(owner, "POST", "/api/projects", map[string]any{"name": "ops"}, 200)
	pid, env := p["id"], p["environments"].([]any)[0].(map[string]any)["id"]
	// Only explicit members see this project.
	e.expect(owner, "PATCH", fmt.Sprintf("/api/projects/%v", pid), map[string]any{"name": "ops", "default_role": "none"}, 200)
	e.expect(owner, "PUT", fmt.Sprintf("/api/projects/%v/members", pid), map[string]any{"members": []map[string]any{{"user_id": dev.ID, "role": "viewer"}}}, 200)
	app := e.expect(owner, "POST", "/api/applications", map[string]any{"name": "grafana", "environment_id": env, "server_id": 1, "source_type": "image", "docker_image": "grafana/grafana", "port": 3000}, 200)
	appID, uuid := app["id"], app["uuid"].(string)
	e.expect(owner, "POST", fmt.Sprintf("/api/applications/%v/domains", appID), map[string]any{"domain": "https://grafana.example.com"}, 200)
	e.expect(owner, "PUT", fmt.Sprintf("/api/applications/%v/protection", appID), map[string]any{"auth": "sso"}, 200)

	noFollow := func(c *http.Client) *http.Client {
		return &http.Client{Jar: c.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	anon := noFollow(&http.Client{})
	// The proxy asks for each request, forwarding the cookies of the application's domain.
	ask := func(uri string, cookie *http.Cookie) *http.Response {
		req, _ := http.NewRequest("GET", e.srv.URL+"/api/forward-auth/"+uuid, nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-Host", "grafana.example.com")
		req.Header.Set("X-Forwarded-Uri", uri)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := anon.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	// 1. No cookie: to PortDock's sign-in, remembering where the visitor was going.
	resp := ask("/d/home?orgId=1", nil)
	loc := resp.Header.Get("Location")
	if resp.StatusCode != 302 || !strings.Contains(loc, "/api/forward-auth/"+uuid+"/login?rd=") || !strings.Contains(loc, url.QueryEscape("https://grafana.example.com/d/home?orgId=1")) {
		t.Fatalf("unauthenticated: %d %s", resp.StatusCode, loc)
	}
	loginPath := strings.TrimPrefix(loc, e.srv.URL)

	// 2. Not signed in to PortDock: to its login page, then back here.
	resp, _ = anon.Get(e.srv.URL + loginPath)
	if resp.StatusCode != 302 || !strings.HasPrefix(resp.Header.Get("Location"), "/login?next=") {
		t.Fatalf("login without session: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	// 3. Signed in and allowed: back to the application with a one-time ticket.
	devClient := noFollow(e.login("dev@corp.com"))
	resp, _ = devClient.Get(e.srv.URL + loginPath)
	back, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != 302 || back.Host != "grafana.example.com" || back.Path != "/_portdock/auth" || back.Query().Get("rd") != "/d/home?orgId=1" {
		t.Fatalf("ticket redirect: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	// 4. The ticket becomes a cookie on the application's domain.
	resp = ask(back.RequestURI(), nil)
	var cookie *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == appAuthCookie {
			cookie = ck
		}
	}
	if resp.StatusCode != 302 || resp.Header.Get("Location") != "https://grafana.example.com/d/home?orgId=1" || cookie == nil || !cookie.HttpOnly || !cookie.Secure {
		t.Fatalf("ticket exchange: %d %s %v", resp.StatusCode, resp.Header.Get("Location"), cookie)
	}
	// A ticket works once; with the cookie, a reloaded ticket page just goes on.
	if resp := ask(back.RequestURI(), nil); len(resp.Cookies()) != 0 || !strings.Contains(resp.Header.Get("Location"), "/login?rd=") {
		t.Fatal("ticket replayed")
	}
	if resp := ask(back.RequestURI(), cookie); resp.StatusCode != 302 || resp.Header.Get("Location") != "https://grafana.example.com/d/home?orgId=1" {
		t.Fatalf("used ticket with a cookie: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	// 5. With the cookie: allowed, with the user's identity for the application.
	resp = ask("/api/dashboards", cookie)
	if resp.StatusCode != 200 || resp.Header.Get("X-Forwarded-Email") != "dev@corp.com" {
		t.Fatalf("authenticated: %d %v", resp.StatusCode, resp.Header)
	}
	// The cookie of one application opens no other.
	other := e.expect(owner, "POST", "/api/applications", map[string]any{"name": "kibana", "environment_id": env, "server_id": 1, "source_type": "image", "docker_image": "kibana"}, 200)
	e.expect(owner, "PUT", fmt.Sprintf("/api/applications/%v/protection", other["id"]), map[string]any{"auth": "sso"}, 200)
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/forward-auth/"+other["uuid"].(string), nil)
	req.Header.Set("X-Forwarded-Host", "kibana.example.com")
	req.AddCookie(cookie)
	if resp, _ := anon.Do(req); resp.StatusCode != 302 {
		t.Fatalf("cookie reused on another application: %d", resp.StatusCode)
	}

	// 6. Someone without access to the project is refused.
	outsider := noFollow(e.login("outsider@other.com"))
	if resp, _ := outsider.Get(e.srv.URL + loginPath); resp.StatusCode != 403 {
		t.Fatalf("outsider: %d", resp.StatusCode)
	}
	// ...unless every PortDock user may sign in, limited to the company's addresses.
	e.expect(owner, "PUT", fmt.Sprintf("/api/applications/%v/protection", appID), map[string]any{"auth": "sso", "sso_access": "any", "sso_emails": "@corp.com"}, 200)
	if resp, _ := outsider.Get(e.srv.URL + loginPath); resp.StatusCode != 403 {
		t.Fatalf("outsider outside the allowed domain: %d", resp.StatusCode)
	}
	if resp, _ := devClient.Get(e.srv.URL + loginPath); resp.StatusCode != 302 {
		t.Fatalf("dev in the allowed domain: %d", resp.StatusCode)
	}

	// 7. Tickets go only to the application's own domains.
	evil := "/api/forward-auth/" + uuid + "/login?rd=" + url.QueryEscape("https://evil.example/steal")
	if resp, _ := devClient.Get(e.srv.URL + evil); resp.StatusCode != 400 {
		t.Fatalf("foreign return address: %d", resp.StatusCode)
	}

	// 8. Without single sign-on the proxy lets requests through.
	e.expect(owner, "PUT", fmt.Sprintf("/api/applications/%v/protection", appID), map[string]any{"auth": ""}, 200)
	if resp := ask("/", nil); resp.StatusCode != 200 {
		t.Fatalf("unprotected: %d", resp.StatusCode)
	}
}
