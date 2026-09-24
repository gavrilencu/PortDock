package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/portdock/portdock/internal/ldapauth"
	"github.com/portdock/portdock/internal/store"
)

// Accounts are never taken over through an identity provider: owners and
// administrators connect SSO themselves, members are linked only with a
// verified e-mail, and directories only reach the accounts they created.
func TestSSOLinkingRules(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("admin@corp.com", "correct-horse", "admin")
	e.user("dev@corp.com", "correct-horse", "member")
	owner := e.login("owner@corp.com")
	idp := newIdP(t)
	prov := e.expect(owner, "POST", "/api/sso/providers", map[string]any{"name": "Corp SSO", "kind": "keycloak", "issuer": idp.URL, "client_id": "portdock", "client_secret": "s3cret", "auto_register": true}, 200)
	if prov["trust_email"] != false {
		t.Fatalf("trust_email should default to false: %v", prov["trust_email"])
	}

	// The identity provider asserts the owner's / an admin's e-mail: refused, even verified.
	for _, email := range []string{"owner@corp.com", "admin@corp.com"} {
		idp.claims = map[string]any{"sub": "attacker-" + email, "email": email, "email_verified": true}
		if next := e.ssoLogin(e.client(), "corp-sso"); !strings.Contains(next, "sso_error") || !strings.Contains(next, "administrator") {
			t.Fatalf("%s was linked by e-mail: %s", email, next)
		}
	}
	// A member with a verified e-mail is linked.
	idp.claims = map[string]any{"sub": "dev-sub", "email": "dev@corp.com", "email_verified": true}
	dev := e.client()
	if next := e.ssoLogin(dev, "corp-sso"); next != "/projects" {
		t.Fatalf("member link: %s", next)
	}
	if me := e.expect(dev, "GET", "/api/auth/me", nil, 200); me["email"] != "dev@corp.com" || me["role"] != "member" {
		t.Fatalf("member: %v", me)
	}
	// Without email_verified and trust_email off, nothing is linked.
	e.user("bob@corp.com", "correct-horse", "member")
	idp.claims = map[string]any{"sub": "bob-sub", "email": "bob@corp.com"}
	if next := e.ssoLogin(e.client(), "corp-sso"); !strings.Contains(next, "sso_error") {
		t.Fatalf("unverified e-mail linked: %s", next)
	}

	// The owner connects the provider from a signed-in session, then signs in with it.
	idp.claims = map[string]any{"sub": "owner-sub", "email": "owner@corp.com", "email_verified": true}
	res, err := owner.Get(e.srv.URL + "/api/auth/sso/corp-sso/start?link=1")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	loc := res.Header.Get("Location")
	for range 4 { // IdP → callback → profile
		if strings.HasPrefix(loc, "/") {
			break
		}
		r2, err := owner.Get(loc)
		if err != nil {
			t.Fatal(err)
		}
		r2.Body.Close()
		loc = r2.Header.Get("Location")
	}
	if !strings.HasPrefix(loc, "/settings?tab=profile&sso_linked=") {
		t.Fatalf("link redirect: %s", loc)
	}
	ids := e.expectList(owner, "GET", "/api/auth/identities")
	if len(ids) != 1 || ids[0]["slug"] != "corp-sso" || ids[0]["email"] != "owner@corp.com" {
		t.Fatalf("identities: %v", ids)
	}
	fresh := e.client()
	if next := e.ssoLogin(fresh, "corp-sso"); next != "/projects" {
		t.Fatalf("owner sso login after linking: %s", next)
	}
	if me := e.expect(fresh, "GET", "/api/auth/me", nil, 200); me["email"] != "owner@corp.com" || me["role"] != "owner" {
		t.Fatalf("owner via sso: %v", me)
	}
	// Linking needs a session; an anonymous ?link=1 is refused.
	res, _ = e.client().Get(e.srv.URL + "/api/auth/sso/corp-sso/start?link=1")
	res.Body.Close()
	if !strings.Contains(res.Header.Get("Location"), "sso_error") {
		t.Fatalf("anonymous link: %s", res.Header.Get("Location"))
	}
	// Disconnecting: the owner has a password, so it is allowed.
	e.expect(owner, "DELETE", fmt.Sprintf("/api/auth/identities/%v", ids[0]["id"]), nil, 200)
	if ids := e.expectList(owner, "GET", "/api/auth/identities"); len(ids) != 0 {
		t.Fatalf("identity not removed: %v", ids)
	}
}

func TestLDAPLinkingRules(t *testing.T) {
	e := newTestEnv(t)
	ownerUser := e.user("owner@corp.com", "correct-horse", "owner")
	memberUser := e.user("dev@corp.com", "correct-horse", "member")
	owner := e.login("owner@corp.com")
	e.user("admin@corp.com", "correct-horse", "admin")
	admin := e.login("admin@corp.com")
	e.expect(owner, "POST", "/api/ldap/directories", map[string]any{"name": "Corp AD", "slug": "corp", "kind": "ad", "host": "dc01.corp.example", "port": 636, "security": "ldaps",
		"bind_dn": "CN=svc,DC=corp", "bind_password": "x", "base_dn": "DC=corp", "user_filter": "(sAMAccountName={username})", "auto_register": true}, 200)
	dir, err := store.Get[store.LDAPDirectory](t.Context(), e.db, "SELECT * FROM ldap_directories WHERE slug = 'corp'")
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(email string) (store.User, error) {
		return e.api.ldapResolveUser(t.Context(), dir, ldapauth.Identity{DN: "CN=x,DC=corp", Username: "x", Email: email, Name: "X"})
	}
	// The directory's mail attribute matches the owner: refused.
	if _, err := resolve("owner@corp.com"); err == nil || !strings.Contains(err.Error(), "not linked") {
		t.Fatalf("owner reached through LDAP: %v", err)
	}
	// A local member is not linked either, until an administrator links it.
	if _, err := resolve("dev@corp.com"); err == nil {
		t.Fatal("local member reached through LDAP")
	}
	e.expect(admin, "PATCH", fmt.Sprintf("/api/users/%d", memberUser.ID), map[string]any{"auth_source": "ldap:nope"}, 400)
	e.expect(admin, "PATCH", fmt.Sprintf("/api/users/%d", memberUser.ID), map[string]any{"auth_source": "sso:x"}, 400)
	e.expect(admin, "PATCH", fmt.Sprintf("/api/users/%d", ownerUser.ID), map[string]any{"auth_source": "ldap:corp"}, 403)
	e.expect(admin, "PATCH", fmt.Sprintf("/api/users/%d", memberUser.ID), map[string]any{"auth_source": "ldap:corp"}, 200)
	if u, err := resolve("dev@corp.com"); err != nil || u.ID != memberUser.ID {
		t.Fatalf("linked member: %v %v", u.ID, err)
	}
	// Unknown e-mails are still provisioned just in time.
	if u, err := resolve("new@corp.com"); err != nil || u.AuthSource != "ldap:corp" || u.Role != "member" {
		t.Fatalf("provisioned: %+v %v", u, err)
	}
	// Back to local: the directory no longer reaches the account.
	e.expect(admin, "PATCH", fmt.Sprintf("/api/users/%d", memberUser.ID), map[string]any{"auth_source": ""}, 200)
	if _, err := resolve("dev@corp.com"); err == nil {
		t.Fatal("unlinked member still reached through LDAP")
	}
}

// expectList is expect for endpoints that answer with a JSON array.
func (e *testEnv) expectList(c *http.Client, method, path string) []map[string]any {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, nil)
	res, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		e.t.Fatalf("%s %s: %d", method, path, res.StatusCode)
	}
	var out []map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		e.t.Fatal(err)
	}
	return out
}
