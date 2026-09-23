package api

import "testing"

func TestUserLocale(t *testing.T) {
	e := newTestEnv(t)
	e.user("ana@example.com", "correct-horse", "member")
	c := e.login("ana@example.com")

	if me := e.expect(c, "GET", "/api/auth/me", nil, 200); me["locale"] != "" {
		t.Fatalf("default locale should be empty, got %v", me["locale"])
	}
	e.expect(c, "PATCH", "/api/auth/me", map[string]any{"locale": "ru"}, 200)
	if me := e.expect(c, "GET", "/api/auth/me", nil, 200); me["locale"] != "ru" {
		t.Fatalf("locale not saved: %v", me["locale"])
	}
	e.expect(c, "PATCH", "/api/auth/me", map[string]any{"locale": "de"}, 400)
	if me := e.expect(c, "GET", "/api/auth/me", nil, 200); me["locale"] != "ru" || me["name"] == nil {
		t.Fatalf("invalid locale must not change anything: %v", me)
	}
}
