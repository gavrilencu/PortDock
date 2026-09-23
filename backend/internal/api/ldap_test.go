package api

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/portdock/portdock/internal/ldapauth"
)

// Runs against a real directory: PORTDOCK_LDAP_TEST=127.0.0.1 with the OpenLDAP
// fixture (osixia/openldap on 1389/1636, users alice/bob/eve, groups portdock-users, platform-admins).
func ldapHost(t *testing.T) string {
	h := os.Getenv("PORTDOCK_LDAP_TEST")
	if h == "" {
		t.Skip("PORTDOCK_LDAP_TEST not set")
	}
	return h
}

func TestLDAPClient(t *testing.T) {
	host := ldapHost(t)
	ctx := context.Background()
	cfg := ldapauth.Config{Kind: "openldap", Host: host, Port: 1389, Security: "none", BindDN: "cn=admin,dc=example,dc=org", BindPassword: "adminpw",
		BaseDN: "dc=example,dc=org", UserFilter: "(&(objectClass=inetOrgPerson)(|(uid={username})(mail={username})))", EmailAttr: "mail", NameAttr: "cn", GroupsAttr: "memberOf"}

	msg, err := ldapauth.Test(ctx, cfg)
	if err != nil || !strings.Contains(msg, "base DN dc=example,dc=org found") {
		t.Fatalf("test: %q %v", msg, err)
	}
	id, err := ldapauth.Authenticate(ctx, cfg, "alice", "alice-pw")
	if err != nil || id.Email != "alice@example.org" || id.Name != "Alice Admin" || len(id.Groups) != 2 {
		t.Fatalf("alice: %+v %v", id, err)
	}
	if !ldapauth.GroupMatches(id.Groups, []string{"platform-admins"}) || !ldapauth.GroupMatches(id.Groups, []string{"cn=portdock-users,ou=groups,dc=example,dc=org"}) {
		t.Fatal("group matching by CN and DN")
	}
	if _, err := ldapauth.Authenticate(ctx, cfg, "alice@example.org", "alice-pw"); err != nil {
		t.Fatalf("login by email: %v", err)
	}
	if _, err := ldapauth.Authenticate(ctx, cfg, "alice", "wrong"); !errors.Is(err, ldapauth.ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := ldapauth.Authenticate(ctx, cfg, "alice", ""); !errors.Is(err, ldapauth.ErrInvalidCredentials) {
		t.Fatalf("empty password must never bind: %v", err)
	}
	if _, err := ldapauth.Authenticate(ctx, cfg, "nobody", "x"); !errors.Is(err, ldapauth.ErrUserNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
	// Filter injection: "*" must be escaped, not match every account.
	if _, err := ldapauth.Authenticate(ctx, cfg, "*", "alice-pw"); !errors.Is(err, ldapauth.ErrUserNotFound) {
		t.Fatalf("filter injection: %v", err)
	}
	// Groups via search when the directory has no memberOf attribute.
	noMemberOf := cfg
	noMemberOf.GroupsAttr = ""
	if id, err := ldapauth.Authenticate(ctx, noMemberOf, "bob", "bob-pw"); err != nil || len(id.Groups) != 1 {
		t.Fatalf("group search fallback: %+v %v", id, err)
	}
	bad := cfg
	bad.BindPassword = "nope"
	if _, err := ldapauth.Test(ctx, bad); err == nil || !strings.Contains(err.Error(), "service account bind failed") {
		t.Fatalf("bad service password: %v", err)
	}

	// StartTLS and LDAPS with the fixture's self-signed certificate.
	for _, sec := range []struct {
		name string
		port int
	}{{"starttls", 1389}, {"ldaps", 1636}} {
		tlsCfg := cfg
		tlsCfg.Security, tlsCfg.Port = sec.name, sec.port
		if _, err := ldapauth.Test(ctx, tlsCfg); err == nil {
			t.Fatalf("%s: untrusted certificate accepted", sec.name)
		}
		tlsCfg.SkipVerify = true
		if msg, err := ldapauth.Test(ctx, tlsCfg); err != nil {
			t.Fatalf("%s: %v", sec.name, err)
		} else {
			t.Log(msg)
		}
	}
}

func TestLDAPLoginFlow(t *testing.T) {
	host := ldapHost(t)
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	owner := e.login("owner@corp.com")

	dir := map[string]any{"name": "Example LDAP", "kind": "openldap", "host": host, "port": 1389, "security": "none",
		"bind_dn": "cn=admin,dc=example,dc=org", "bind_password": "adminpw", "base_dn": "dc=example,dc=org",
		"user_filter": "(&(objectClass=inetOrgPerson)(|(uid={username})(mail={username})))", "name_attr": "cn",
		"required_groups": "portdock-users", "admin_groups": "platform-admins", "auto_register": true}
	res := e.expect(owner, "POST", "/api/ldap/test", mergeMap(dir, map[string]any{"username": "alice", "password": "alice-pw"}), 200)
	if u := res["user"].(map[string]any); u["role"] != "admin" || u["allowed"] != true {
		t.Fatalf("test user: %v", res)
	}
	d := e.expect(owner, "POST", "/api/ldap/directories", dir, 200)
	if d["bind_password_set"] != true || d["slug"] != "example-ldap" {
		t.Fatalf("create: %v", d)
	}
	if _, prov := e.do(e.client(), "GET", "/api/auth/providers", nil); len(prov["ldap"].([]any)) != 1 {
		t.Fatalf("providers: %v", prov)
	}

	// Just-in-time account, admin via group, username login.
	alice := e.client()
	e.expect(alice, "POST", "/api/auth/login", map[string]string{"email": "alice", "password": "alice-pw"}, 200)
	me := e.expect(alice, "GET", "/api/auth/me", nil, 200)
	if me["email"] != "alice@example.org" || me["role"] != "admin" || me["auth_source"] != "ldap:example-ldap" || me["has_password"] != false {
		t.Fatalf("alice: %v", me)
	}
	bob := e.client()
	e.expect(bob, "POST", "/api/auth/login", map[string]string{"email": "bob@example.org", "password": "bob-pw"}, 200)
	if me := e.expect(bob, "GET", "/api/auth/me", nil, 200); me["role"] != "member" {
		t.Fatalf("bob: %v", me)
	}
	// Not in the required group / wrong password.
	if code, res := e.do(e.client(), "POST", "/api/auth/login", map[string]string{"email": "eve", "password": "eve-pw"}); code != 401 || !strings.Contains(res["error"].(string), "not in a group") {
		t.Fatalf("eve: %d %v", code, res)
	}
	e.expect(e.client(), "POST", "/api/auth/login", map[string]string{"email": "alice", "password": "nope"}, 401)
	// Local accounts keep working (break-glass) and LDAP works when password login is disabled.
	_ = e.db.SetSetting(t.Context(), "password_login", "false")
	e.expect(e.client(), "POST", "/api/auth/login", map[string]string{"email": "bob", "password": "bob-pw"}, 200)
	e.expect(e.client(), "POST", "/api/auth/login", map[string]string{"email": "owner@corp.com", "password": "correct-horse"}, 200)
	// Sessions record the directory.
	var method string
	_ = e.db.QueryRow("SELECT method FROM sessions s JOIN users u ON u.id = s.user_id WHERE u.email = 'alice@example.org'").Scan(&method)
	if method != "ldap:example-ldap" {
		t.Fatalf("session method %q", method)
	}
}

func mergeMap(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
