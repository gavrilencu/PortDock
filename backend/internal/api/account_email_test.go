package api

import "testing"

// A member cannot pick another person's e-mail: single sign-on would link that
// person's first sign-in to the member's account.
func TestSelfEmailChange(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("dev@corp.com", "correct-horse", "member")
	dev := e.login("dev@corp.com")
	owner := e.login("owner@corp.com")

	e.expect(dev, "PATCH", "/api/auth/me", map[string]any{"email": "cto@corp.com", "current_password": "correct-horse"}, 403)
	e.expect(dev, "PATCH", "/api/auth/me", map[string]any{"email": "dev@corp.com", "name": "Dev"}, 200) // unchanged address
	e.expect(owner, "PATCH", "/api/auth/me", map[string]any{"email": "boss@corp.com"}, 400)
	e.expect(owner, "PATCH", "/api/auth/me", map[string]any{"email": "boss@corp.com", "current_password": "wrong-horse"}, 400)
	if me := e.expect(owner, "PATCH", "/api/auth/me", map[string]any{"email": "boss@corp.com", "current_password": "correct-horse"}, 200); me["email"] != "boss@corp.com" {
		t.Fatalf("owner e-mail: %v", me)
	}
}
