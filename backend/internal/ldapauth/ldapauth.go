// Package ldapauth authenticates users against LDAP directories such as
// Microsoft Active Directory, OpenLDAP and FreeIPA: a service account searches
// for the user, then the user's own DN is bound with the password they typed.
package ldapauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// Config is a directory with its bind password decrypted.
type Config struct {
	Kind         string // ad | openldap | freeipa | generic
	Host         string
	Port         int
	Security     string // ldaps | starttls | none
	SkipVerify   bool
	CACert       string
	BindDN       string
	BindPassword string
	BaseDN       string
	UserFilter   string // contains {username}
	EmailAttr    string
	NameAttr     string
	GroupsAttr   string
	NestedGroups bool
}

// Identity is an authenticated directory user.
type Identity struct {
	DN       string   `json:"dn"`
	Username string   `json:"username"`
	Email    string   `json:"email"`
	Name     string   `json:"name"`
	Groups   []string `json:"groups"` // group DNs
}

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUserNotFound       = errors.New("user not found in the directory")
	ErrAccountDisabled    = errors.New("the directory account is disabled")
)

// Preset holds sensible defaults per directory type.
type Preset struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Port       int    `json:"port"`
	Security   string `json:"security"`
	UserFilter string `json:"user_filter"`
	EmailAttr  string `json:"email_attr"`
	NameAttr   string `json:"name_attr"`
	GroupsAttr string `json:"groups_attr"`
	Nested     bool   `json:"nested_groups"`
	BindHint   string `json:"bind_hint"`
	BaseHint   string `json:"base_hint"`
}

var Presets = []Preset{
	{Kind: "ad", Name: "Microsoft Active Directory", Port: 636, Security: "ldaps",
		UserFilter: "(&(objectClass=user)(objectCategory=person)(|(sAMAccountName={username})(userPrincipalName={username})(mail={username})))",
		EmailAttr:  "mail", NameAttr: "displayName", GroupsAttr: "memberOf", Nested: true,
		BindHint: "CN=svc-portdock,OU=Service Accounts,DC=corp,DC=example or svc-portdock@corp.example", BaseHint: "DC=corp,DC=example"},
	{Kind: "openldap", Name: "OpenLDAP", Port: 389, Security: "starttls",
		UserFilter: "(&(objectClass=inetOrgPerson)(|(uid={username})(mail={username})))",
		EmailAttr:  "mail", NameAttr: "cn", GroupsAttr: "memberOf",
		BindHint: "cn=readonly,dc=example,dc=org", BaseHint: "ou=people,dc=example,dc=org"},
	{Kind: "freeipa", Name: "FreeIPA / Red Hat IdM", Port: 636, Security: "ldaps",
		UserFilter: "(&(objectClass=person)(|(uid={username})(mail={username})))",
		EmailAttr:  "mail", NameAttr: "cn", GroupsAttr: "memberOf",
		BindHint: "uid=svc-portdock,cn=sysaccounts,cn=etc,dc=example,dc=org", BaseHint: "cn=accounts,dc=example,dc=org"},
	{Kind: "generic", Name: "Other LDAP directory", Port: 389, Security: "starttls",
		UserFilter: "(|(uid={username})(mail={username}))", EmailAttr: "mail", NameAttr: "cn", GroupsAttr: "memberOf"},
}

func (c Config) tlsConfig() (*tls.Config, error) {
	tc := &tls.Config{ServerName: c.Host, InsecureSkipVerify: c.SkipVerify, MinVersion: tls.VersionTLS12} //nolint:gosec // opt-in for lab directories
	if strings.TrimSpace(c.CACert) != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(c.CACert)) {
			return nil, errors.New("the CA certificate is not valid PEM")
		}
		tc.RootCAs = pool
	}
	return tc, nil
}

// Dial connects (and secures) a connection to the directory.
func Dial(ctx context.Context, c Config) (*ldap.Conn, error) {
	port := c.Port
	if port == 0 {
		port = 389
		if c.Security == "ldaps" {
			port = 636
		}
	}
	addr := net.JoinHostPort(c.Host, strconv.Itoa(port))
	tc, err := c.tlsConfig()
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var conn *ldap.Conn
	switch c.Security {
	case "ldaps":
		conn, err = ldap.DialURL("ldaps://"+addr, ldap.DialWithDialer(dialer), ldap.DialWithTLSConfig(tc))
	default:
		conn, err = ldap.DialURL("ldap://"+addr, ldap.DialWithDialer(dialer))
		if err == nil && c.Security == "starttls" {
			if err = conn.StartTLS(tc); err != nil {
				conn.Close()
				return nil, fmt.Errorf("StartTLS failed: %w", err)
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("cannot connect to %s: %w", addr, err)
	}
	conn.SetTimeout(15 * time.Second)
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	return conn, nil
}

func (c Config) serviceBind(conn *ldap.Conn) error {
	if c.BindDN == "" {
		return nil // anonymous search
	}
	if err := conn.Bind(c.BindDN, c.BindPassword); err != nil {
		return fmt.Errorf("service account bind failed: %w", friendly(err))
	}
	return nil
}

// friendly turns LDAP result codes into readable errors (AD sub-codes included).
func friendly(err error) error {
	var le *ldap.Error
	if errors.As(err, &le) {
		switch le.ResultCode {
		case ldap.LDAPResultInvalidCredentials:
			msg := le.Err.Error()
			switch {
			case strings.Contains(msg, "data 532"):
				return errors.New("password expired")
			case strings.Contains(msg, "data 533"):
				return ErrAccountDisabled
			case strings.Contains(msg, "data 701"):
				return errors.New("account expired")
			case strings.Contains(msg, "data 775"):
				return errors.New("account locked out")
			}
			return ErrInvalidCredentials
		case ldap.LDAPResultNoSuchObject:
			return errors.New("the base DN does not exist")
		case ldap.LDAPResultConfidentialityRequired, ldap.LDAPResultStrongAuthRequired:
			return errors.New("the directory requires an encrypted connection (use LDAPS or StartTLS)")
		}
	}
	return err
}

func (c Config) attrs() []string {
	set := map[string]bool{"dn": true}
	var out []string
	for _, a := range []string{c.EmailAttr, c.NameAttr, c.GroupsAttr, "mail", "userPrincipalName", "sAMAccountName", "uid", "cn", "displayName", "userAccountControl"} {
		if a != "" && !set[strings.ToLower(a)] {
			set[strings.ToLower(a)] = true
			out = append(out, a)
		}
	}
	return out
}

// Authenticate verifies username/password and returns the directory identity.
func Authenticate(ctx context.Context, c Config, username, password string) (Identity, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		// An empty password would be an "unauthenticated bind", which many servers accept.
		return Identity{}, ErrInvalidCredentials
	}
	conn, err := Dial(ctx, c)
	if err != nil {
		return Identity{}, err
	}
	defer conn.Close()
	if err := c.serviceBind(conn); err != nil {
		return Identity{}, err
	}
	entry, err := c.findUser(conn, username)
	if err != nil {
		return Identity{}, err
	}
	if c.Kind == "ad" {
		if uac, _ := strconv.Atoi(entry.GetAttributeValue("userAccountControl")); uac&0x2 != 0 {
			return Identity{}, ErrAccountDisabled
		}
	}
	if err := conn.Bind(entry.DN, password); err != nil {
		return Identity{}, friendly(err)
	}
	// Group lookups use the service account (users often can't read groups).
	_ = c.serviceBind(conn)
	id := c.identity(entry, username)
	id.Groups = c.groups(conn, entry, id.Username)
	if id.Email == "" {
		return id, fmt.Errorf("the directory account has no %s attribute; set an email address or change the email attribute", c.EmailAttr)
	}
	return id, nil
}

// Lookup resolves a user without their password (used by the admin "test user" tool).
func Lookup(ctx context.Context, c Config, username string) (Identity, error) {
	conn, err := Dial(ctx, c)
	if err != nil {
		return Identity{}, err
	}
	defer conn.Close()
	if err := c.serviceBind(conn); err != nil {
		return Identity{}, err
	}
	entry, err := c.findUser(conn, strings.TrimSpace(username))
	if err != nil {
		return Identity{}, err
	}
	id := c.identity(entry, username)
	id.Groups = c.groups(conn, entry, id.Username)
	return id, nil
}

func (c Config) findUser(conn *ldap.Conn, username string) (*ldap.Entry, error) {
	filter := strings.ReplaceAll(c.UserFilter, "{username}", ldap.EscapeFilter(username))
	res, err := conn.Search(ldap.NewSearchRequest(c.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 15, false, filter, c.attrs(), nil))
	if err != nil {
		var le *ldap.Error
		if errors.As(err, &le) && le.ResultCode == ldap.LDAPResultSizeLimitExceeded {
			return nil, fmt.Errorf("the user filter matches several accounts for %q; make it more specific", username)
		}
		return nil, fmt.Errorf("user search failed: %w", friendly(err))
	}
	switch len(res.Entries) {
	case 0:
		return nil, ErrUserNotFound
	case 1:
		return res.Entries[0], nil
	}
	return nil, fmt.Errorf("the user filter matches several accounts for %q; make it more specific", username)
}

func (c Config) identity(e *ldap.Entry, typed string) Identity {
	get := func(names ...string) string {
		for _, n := range names {
			if v := strings.TrimSpace(e.GetAttributeValue(n)); v != "" {
				return v
			}
		}
		return ""
	}
	id := Identity{DN: e.DN}
	id.Username = get("sAMAccountName", "uid", "cn")
	if id.Username == "" {
		id.Username = typed
	}
	id.Email = strings.ToLower(get(c.EmailAttr, "mail"))
	if id.Email == "" && c.Kind == "ad" {
		id.Email = strings.ToLower(get("userPrincipalName"))
	}
	id.Name = get(c.NameAttr, "displayName", "cn")
	return id
}

// groups collects the group DNs of a user: the memberOf-style attribute, AD's
// transitive membership rule for nested groups, and for directories without
// memberOf a search over groupOfNames / groupOfUniqueNames / posixGroup.
func (c Config) groups(conn *ldap.Conn, e *ldap.Entry, username string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(dn string) {
		key := strings.ToLower(dn)
		if dn != "" && !seen[key] {
			seen[key] = true
			out = append(out, dn)
		}
	}
	if c.GroupsAttr != "" {
		for _, g := range e.GetAttributeValues(c.GroupsAttr) {
			add(g)
		}
	}
	search := func(filter string) {
		res, err := conn.Search(ldap.NewSearchRequest(c.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 500, 15, false, filter, []string{"dn"}, nil))
		if err == nil {
			for _, g := range res.Entries {
				add(g.DN)
			}
		}
	}
	dn := ldap.EscapeFilter(e.DN)
	if c.Kind == "ad" && c.NestedGroups {
		search("(&(objectClass=group)(member:1.2.840.113556.1.4.1941:=" + dn + "))")
	}
	if len(out) == 0 && c.Kind != "ad" {
		search("(|(&(objectClass=groupOfNames)(member=" + dn + "))(&(objectClass=groupOfUniqueNames)(uniqueMember=" + dn + "))(&(objectClass=posixGroup)(memberUid=" + ldap.EscapeFilter(username) + ")))")
	}
	return out
}

// Test connects, binds the service account and checks the base DN.
func Test(ctx context.Context, c Config) (string, error) {
	start := time.Now()
	conn, err := Dial(ctx, c)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if err := c.serviceBind(conn); err != nil {
		return "", err
	}
	res, err := conn.Search(ldap.NewSearchRequest(c.BaseDN, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 10, false, "(objectClass=*)", []string{"dn"}, nil))
	if err != nil {
		return "", fmt.Errorf("base DN check failed: %w", friendly(err))
	}
	if len(res.Entries) == 0 {
		return "", errors.New("the base DN does not exist")
	}
	sec := map[string]string{"ldaps": "LDAPS", "starttls": "StartTLS", "none": "unencrypted"}[c.Security]
	who := "anonymously"
	if c.BindDN != "" {
		who = "as " + c.BindDN
	}
	return fmt.Sprintf("Connected over %s and bound %s in %s; base DN %s found.", sec, who, time.Since(start).Round(time.Millisecond), res.Entries[0].DN), nil
}

// GroupMatches reports whether any of the user's group DNs matches one of the
// configured groups, given either as full DN or as common name.
func GroupMatches(userGroups []string, wanted []string) bool {
	for _, w := range wanted {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" {
			continue
		}
		for _, g := range userGroups {
			gl := strings.ToLower(g)
			if gl == w || commonName(gl) == w {
				return true
			}
		}
	}
	return false
}

func commonName(dn string) string {
	first, _, _ := strings.Cut(dn, ",")
	if _, v, ok := strings.Cut(first, "="); ok {
		return strings.TrimSpace(v)
	}
	return dn
}
