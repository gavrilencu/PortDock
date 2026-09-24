package api

import (
	"fmt"
	"testing"
	"time"
)

// A member with a project opens the dashboard: this used to deadlock the
// whole process (a query inside an open cursor on the single connection).
func TestMemberDashboardDoesNotDeadlock(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("member@corp.com", "correct-horse", "member")
	c := e.login("member@corp.com")
	e.expect(c, "POST", "/api/projects", map[string]any{"name": "p1"}, 200)
	done := make(chan map[string]any, 1)
	go func() { done <- e.expect(c, "GET", "/api/dashboard", nil, 200) }()
	select {
	case d := <-done:
		if d["counts"].(map[string]any)["projects"] != float64(1) {
			t.Fatalf("dashboard counts: %v", d["counts"])
		}
	case <-time.After(10 * time.Second):
		t.Fatal("/api/dashboard did not answer a member within 10s")
	}
}

// Administrators cannot take over the owner account by setting its password.
func TestAdminCannotSetOwnerPassword(t *testing.T) {
	e := newTestEnv(t)
	owner := e.user("owner@corp.com", "correct-horse", "owner")
	admin := e.user("admin@corp.com", "correct-horse", "admin")
	oc, ac := e.login("owner@corp.com"), e.login("admin@corp.com")
	e.expect(ac, "PATCH", fmt.Sprintf("/api/users/%d", owner.ID), map[string]any{"password": "taken-over-1"}, 403)
	e.expect(ac, "PATCH", fmt.Sprintf("/api/users/%d", owner.ID), map[string]any{"name": "The Owner"}, 200)
	e.expect(ac, "PATCH", fmt.Sprintf("/api/users/%d", admin.ID), map[string]any{"password": "new-password-1"}, 200)
	e.expect(oc, "PATCH", fmt.Sprintf("/api/users/%d", owner.ID), map[string]any{"password": "owner-sets-own-1"}, 200)
	if code, _ := e.do(e.client(), "POST", "/api/auth/login", map[string]string{"email": "owner@corp.com", "password": "taken-over-1"}); code == 200 {
		t.Fatal("the admin's password took effect on the owner")
	}
	if code, _ := e.do(e.client(), "POST", "/api/auth/login", map[string]string{"email": "owner@corp.com", "password": "owner-sets-own-1"}); code != 200 {
		t.Fatalf("owner's own password change: %d", code)
	}
}
