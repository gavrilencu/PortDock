package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/ldapauth"
	"github.com/portdock/portdock/internal/store"
)

func (s *Server) ldapConfig(d store.LDAPDirectory) ldapauth.Config {
	return ldapauth.Config{Kind: d.Kind, Host: d.Host, Port: d.Port, Security: d.Security, SkipVerify: d.SkipVerify, CACert: d.CACert,
		BindDN: d.BindDN, BindPassword: s.box.MustDecrypt(d.BindPassword), BaseDN: d.BaseDN, UserFilter: d.UserFilter,
		EmailAttr: d.EmailAttr, NameAttr: d.NameAttr, GroupsAttr: d.GroupsAttr, NestedGroups: d.NestedGroups}
}

var errNoDirectoryUser = errors.New("not found in any directory")

// ldapLogin authenticates against the enabled directories in order. The first
// directory that knows the username decides; a wrong password is not retried elsewhere.
func (s *Server) ldapLogin(ctx context.Context, username, password string) (store.User, store.LDAPDirectory, error) {
	dirs, err := store.Select[store.LDAPDirectory](ctx, s.db, "SELECT * FROM ldap_directories WHERE enabled = 1 ORDER BY id")
	if err != nil || len(dirs) == 0 {
		return store.User{}, store.LDAPDirectory{}, errNoDirectoryUser
	}
	for _, d := range dirs {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		id, err := ldapauth.Authenticate(cctx, s.ldapConfig(d), username, password)
		cancel()
		if errors.Is(err, ldapauth.ErrUserNotFound) {
			continue
		}
		if err != nil {
			if !errors.Is(err, ldapauth.ErrInvalidCredentials) {
				s.db.LogActivity(ctx, 0, "ldap.error", d.Name+": "+err.Error())
			}
			return store.User{}, d, err
		}
		u, err := s.ldapResolveUser(ctx, d, id)
		return u, d, err
	}
	return store.User{}, store.LDAPDirectory{}, errNoDirectoryUser
}

// ldapResolveUser maps a directory identity to a PortDock account (JIT provisioning, role sync).
func (s *Server) ldapResolveUser(ctx context.Context, d store.LDAPDirectory, id ldapauth.Identity) (store.User, error) {
	if req := splitCSV(d.RequiredGroups); len(req) > 0 && !ldapauth.GroupMatches(id.Groups, req) {
		return store.User{}, fmt.Errorf("your directory account is not in a group allowed to use PortDock")
	}
	u, err := store.Get[store.User](ctx, s.db, "SELECT * FROM users WHERE email = ?", id.Email)
	switch {
	case err == nil && u.AuthSource != "ldap:"+d.Slug:
		// A matching e-mail is not proof of identity: whoever can edit the
		// mail attribute in the directory could otherwise sign in as any
		// local account. Administrators link such accounts explicitly.
		return store.User{}, fmt.Errorf("there is already a PortDock account for %s that is not linked to %s; ask an administrator to link it", id.Email, d.Name)
	case err == nil:
	case !d.AutoRegister:
		return store.User{}, fmt.Errorf("there is no PortDock account for %s — ask an administrator to invite you", id.Email)
	default:
		name := id.Name
		if name == "" {
			name = id.Username
		}
		role := "member"
		if d.DefaultRole == "admin" {
			role = "admin"
		}
		u = store.User{Email: id.Email, Name: name, Role: role, AuthSource: "ldap:" + d.Slug, CreatedAt: store.Now()}
		if _, err := store.Insert(ctx, s.db, "users", &u); err != nil {
			return store.User{}, err
		}
		s.db.LogActivity(ctx, u.ID, "user.provisioned", u.Email+" via "+d.Name)
	}
	if u.Disabled {
		return store.User{}, fmt.Errorf("this account has been disabled; contact an administrator")
	}
	if admins := splitCSV(d.AdminGroups); len(admins) > 0 && u.Role != "owner" {
		role := "member"
		if d.DefaultRole == "admin" || ldapauth.GroupMatches(id.Groups, admins) {
			role = "admin"
		}
		if role != u.Role {
			u.Role = role
			_, _ = s.db.ExecContext(ctx, "UPDATE users SET role = ? WHERE id = ?", role, u.ID)
			s.db.LogActivity(ctx, u.ID, "user.role_synced", u.Email+" → "+role+" ("+d.Name+")")
		}
	}
	if u.Name == "" && id.Name != "" {
		_, _ = s.db.ExecContext(ctx, "UPDATE users SET name = ? WHERE id = ?", id.Name, u.ID)
	}
	return u, nil
}

// ---- administration ----

type ldapOut struct {
	store.LDAPDirectory
	BindPasswordSet bool `json:"bind_password_set"`
	Users           int  `json:"users"`
}

func (s *Server) ldapOut(ctx context.Context, d store.LDAPDirectory) ldapOut {
	var n int
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE auth_source = ?", "ldap:"+d.Slug).Scan(&n)
	return ldapOut{LDAPDirectory: d, BindPasswordSet: d.BindPassword != "", Users: n}
}

func (s *Server) listLDAP(w http.ResponseWriter, r *http.Request) error {
	dirs, err := store.Select[store.LDAPDirectory](r.Context(), s.db, "SELECT * FROM ldap_directories ORDER BY id")
	if err != nil {
		return err
	}
	out := make([]ldapOut, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, s.ldapOut(r.Context(), d))
	}
	return ok(w, map[string]any{"directories": out, "presets": ldapauth.Presets})
}

type ldapIn struct {
	ID             int64   `json:"id"` // only for tests of saved directories
	Name           *string `json:"name"`
	Slug           *string `json:"slug"`
	Kind           *string `json:"kind"`
	Host           *string `json:"host"`
	Port           *int    `json:"port"`
	Security       *string `json:"security"`
	SkipVerify     *bool   `json:"skip_verify"`
	CACert         *string `json:"ca_cert"`
	BindDN         *string `json:"bind_dn"`
	BindPassword   *string `json:"bind_password"`
	BaseDN         *string `json:"base_dn"`
	UserFilter     *string `json:"user_filter"`
	EmailAttr      *string `json:"email_attr"`
	NameAttr       *string `json:"name_attr"`
	GroupsAttr     *string `json:"groups_attr"`
	RequiredGroups *string `json:"required_groups"`
	AdminGroups    *string `json:"admin_groups"`
	NestedGroups   *bool   `json:"nested_groups"`
	AutoRegister   *bool   `json:"auto_register"`
	DefaultRole    *string `json:"default_role"`
	Enabled        *bool   `json:"enabled"`
}

func (s *Server) applyLDAP(d *store.LDAPDirectory, in ldapIn) error {
	str := map[*string]*string{&d.Name: in.Name, &d.Slug: in.Slug, &d.Kind: in.Kind, &d.Host: in.Host, &d.Security: in.Security, &d.CACert: in.CACert,
		&d.BindDN: in.BindDN, &d.BaseDN: in.BaseDN, &d.UserFilter: in.UserFilter, &d.EmailAttr: in.EmailAttr, &d.NameAttr: in.NameAttr,
		&d.GroupsAttr: in.GroupsAttr, &d.RequiredGroups: in.RequiredGroups, &d.AdminGroups: in.AdminGroups, &d.DefaultRole: in.DefaultRole}
	for dst, v := range str {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	for dst, v := range map[*bool]*bool{&d.SkipVerify: in.SkipVerify, &d.NestedGroups: in.NestedGroups, &d.AutoRegister: in.AutoRegister, &d.Enabled: in.Enabled} {
		if v != nil {
			*dst = *v
		}
	}
	if in.Port != nil {
		d.Port = *in.Port
	}
	if in.BindPassword != nil && *in.BindPassword != "" {
		d.BindPassword = s.box.Encrypt(*in.BindPassword)
	}
	if d.Name == "" {
		return badRequest("name is required")
	}
	if d.Slug == "" {
		d.Slug = slugify(d.Name)
	}
	if !slugRe.MatchString(d.Slug) {
		return badRequest("slug may contain lowercase letters, digits and dashes")
	}
	switch d.Kind {
	case "ad", "openldap", "freeipa", "generic":
	default:
		d.Kind = "generic"
	}
	d.Host = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(d.Host, "ldaps://"), "ldap://"), "//")
	if d.Host == "" || strings.ContainsAny(d.Host, " /") {
		return badRequest("host must be a hostname or IP address, e.g. dc01.corp.example")
	}
	switch d.Security {
	case "ldaps", "starttls", "none":
	default:
		return badRequest("security must be ldaps, starttls or none")
	}
	if d.Port <= 0 || d.Port > 65535 {
		d.Port = map[string]int{"ldaps": 636}[d.Security]
		if d.Port == 0 {
			d.Port = 389
		}
	}
	if d.BaseDN == "" || !strings.Contains(d.BaseDN, "=") {
		return badRequest("base DN is required, e.g. DC=corp,DC=example")
	}
	if !strings.Contains(d.UserFilter, "{username}") || !strings.HasPrefix(d.UserFilter, "(") {
		return badRequest("the user filter must be an LDAP filter containing {username}")
	}
	if d.EmailAttr == "" {
		d.EmailAttr = "mail"
	}
	if d.DefaultRole != "admin" {
		d.DefaultRole = "member"
	}
	return nil
}

func (s *Server) createLDAP(w http.ResponseWriter, r *http.Request) error {
	var in ldapIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	d := store.LDAPDirectory{Enabled: true, AutoRegister: true, NestedGroups: true, EmailAttr: "mail", NameAttr: "displayName", GroupsAttr: "memberOf", DefaultRole: "member", CreatedAt: store.Now()}
	if err := s.applyLDAP(&d, in); err != nil {
		return err
	}
	if _, err := store.Insert(r.Context(), s.db, "ldap_directories", &d); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return badRequest("a directory with the slug %q already exists", d.Slug)
		}
		return err
	}
	s.activity(r, "ldap.create", d.Name)
	return ok(w, s.ldapOut(r.Context(), d))
}

func (s *Server) loadLDAP(r *http.Request) (store.LDAPDirectory, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.LDAPDirectory{}, err
	}
	return store.ByID[store.LDAPDirectory](r.Context(), s.db, "ldap_directories", id)
}

func (s *Server) updateLDAP(w http.ResponseWriter, r *http.Request) error {
	d, err := s.loadLDAP(r)
	if err != nil {
		return err
	}
	var in ldapIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.applyLDAP(&d, in); err != nil {
		return err
	}
	if err := store.Update(r.Context(), s.db, "ldap_directories", &d); err != nil {
		return err
	}
	s.activity(r, "ldap.update", d.Name)
	return ok(w, s.ldapOut(r.Context(), d))
}

func (s *Server) deleteLDAP(w http.ResponseWriter, r *http.Request) error {
	d, err := s.loadLDAP(r)
	if err != nil {
		return err
	}
	if err := store.Delete(r.Context(), s.db, "ldap_directories", d.ID); err != nil {
		return err
	}
	s.activity(r, "ldap.delete", d.Name)
	return ok(w, map[string]bool{"ok": true})
}

// draftLDAP builds a config from the form (reusing the stored secret of a saved directory).
func (s *Server) draftLDAP(r *http.Request) (store.LDAPDirectory, ldapIn, error) {
	var in struct {
		ldapIn
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		return store.LDAPDirectory{}, ldapIn{}, err
	}
	d := store.LDAPDirectory{Name: "draft", EmailAttr: "mail", NameAttr: "displayName", GroupsAttr: "memberOf"}
	if in.ID != 0 {
		var err error
		if d, err = store.ByID[store.LDAPDirectory](r.Context(), s.db, "ldap_directories", in.ID); err != nil {
			return d, ldapIn{}, err
		}
	}
	if err := s.applyLDAP(&d, in.ldapIn); err != nil {
		return d, ldapIn{}, err
	}
	// Carry the test credentials in the otherwise unused pointer fields.
	in.ldapIn.Name, in.ldapIn.Slug = &in.Username, &in.Password
	return d, in.ldapIn, nil
}

// testLDAP checks connection, service bind and base DN; with a username it also
// resolves the account (and verifies the password when one is given).
func (s *Server) testLDAP(w http.ResponseWriter, r *http.Request) error {
	d, extra, err := s.draftLDAP(r)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	cfg := s.ldapConfig(d)
	msg, err := ldapauth.Test(ctx, cfg)
	if err != nil {
		return badRequest("%v", err)
	}
	res := map[string]any{"message": msg}
	username, password := *extra.Name, *extra.Slug
	if strings.TrimSpace(username) != "" {
		var id ldapauth.Identity
		if password != "" {
			id, err = ldapauth.Authenticate(ctx, cfg, username, password)
		} else {
			id, err = ldapauth.Lookup(ctx, cfg, username)
		}
		if err != nil {
			return badRequest("%s — user test: %v", msg, err)
		}
		role := "member"
		if d.DefaultRole == "admin" || ldapauth.GroupMatches(id.Groups, splitCSV(d.AdminGroups)) {
			role = "admin"
		}
		allowed := len(splitCSV(d.RequiredGroups)) == 0 || ldapauth.GroupMatches(id.Groups, splitCSV(d.RequiredGroups))
		res["user"] = map[string]any{"identity": id, "role": role, "allowed": allowed, "password_checked": password != ""}
	}
	return ok(w, res)
}
