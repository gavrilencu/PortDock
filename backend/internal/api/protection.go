package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/proxy"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
)

// Access protection of an application (Domains → Access protection): allowed
// addresses, a rate limit and a sign-in, enforced by the proxy in front of it.
//
// Single sign-on uses Traefik's forwardAuth: for every request the proxy asks
// GET /api/forward-auth/<app>. Without a valid cookie on the application's
// domain, the visitor is sent to /api/forward-auth/<app>/login on PortDock,
// which signs them in (with the company's SSO when configured), checks that
// they may use the application and hands back a one-time ticket that
// /_portdock/auth on the application's domain turns into the cookie.

type protectionDTO struct {
	store.AppProtection
	Users []string `json:"users"`
}

func protectionOut(p store.AppProtection) protectionDTO {
	names := []string{}
	for _, u := range p.Users() {
		names = append(names, u.User)
	}
	return protectionDTO{AppProtection: p, Users: names}
}

func (s *Server) getProtection(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	p, err := s.db.Protection(r.Context(), app.ID)
	if err != nil {
		return err
	}
	return ok(w, protectionOut(p))
}

var basicUserRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)

func (s *Server) updateProtection(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	var in struct {
		Auth        string `json:"auth"`
		SSOAccess   string `json:"sso_access"`
		SSOEmails   string `json:"sso_emails"`
		IPAllow     string `json:"ip_allow"`
		IPDepth     int    `json:"ip_depth"`
		RateAverage int    `json:"rate_average"`
		RateBurst   int    `json:"rate_burst"`
		Users       []struct {
			User     string `json:"user"`
			Password string `json:"password"` // empty keeps the current password
		} `json:"users"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	prev, err := s.db.Protection(r.Context(), app.ID)
	if err != nil {
		return err
	}
	p := store.AppProtection{ApplicationID: app.ID, Auth: in.Auth, SSOAccess: in.SSOAccess, IPDepth: in.IPDepth, RateAverage: in.RateAverage, RateBurst: in.RateBurst}
	switch p.Auth {
	case "", "password", "sso":
	default:
		return badRequest("auth must be empty, password or sso")
	}
	if p.SSOAccess == "" {
		p.SSOAccess = "project"
	}
	if p.SSOAccess != "project" && p.SSOAccess != "any" {
		return badRequest("sso_access must be project or any")
	}
	var emails []string
	for _, e := range strings.Split(in.SSOEmails, "\n") {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if !strings.Contains(e, "@") || strings.ContainsAny(e, " ,;") {
			return badRequest("%q is not an e-mail address or @domain", e)
		}
		emails = append(emails, e)
	}
	p.SSOEmails = strings.Join(emails, "\n")
	var ranges []string
	for _, l := range strings.Split(in.IPAllow, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(l); err != nil && net.ParseIP(l) == nil {
			return badRequest("%q is not an IP address or CIDR range", l)
		}
		ranges = append(ranges, l)
	}
	p.IPAllow = strings.Join(ranges, "\n")
	if p.IPDepth < 0 || p.IPDepth > 5 {
		return badRequest("the forwarded-address depth must be between 0 and 5")
	}
	if p.RateAverage < 0 || p.RateAverage > 100000 || p.RateBurst < 0 || p.RateBurst > 1000000 {
		return badRequest("invalid rate limit")
	}
	old := map[string]string{}
	for _, u := range prev.Users() {
		old[u.User] = u.Hash
	}
	var users []store.BasicUser
	for _, u := range in.Users {
		name := strings.TrimSpace(u.User)
		if !basicUserRe.MatchString(name) {
			return badRequest("user names may contain letters, digits, '.', '_', '@' and '-'")
		}
		if slices.ContainsFunc(users, func(x store.BasicUser) bool { return x.User == name }) {
			return badRequest("user %s is listed twice", name)
		}
		hash := old[name]
		if u.Password != "" {
			if len(u.Password) < 8 {
				return badRequest("the password of %s must be at least 8 characters", name)
			}
			if hash, err = auth.HashPassword(u.Password); err != nil {
				return err
			}
		}
		if hash == "" {
			return badRequest("set a password for %s", name)
		}
		users = append(users, store.BasicUser{User: name, Hash: hash})
	}
	if p.Auth == "password" && len(users) == 0 {
		return badRequest("add at least one user for the password prompt")
	}
	b, _ := json.Marshal(users)
	p.BasicUsers = string(b)
	if err := s.db.SaveProtection(r.Context(), p); err != nil {
		return err
	}
	s.activity(r, "application.protection", fmt.Sprintf("%s: auth=%s, %d address range(s), rate %d/s", app.Name, firstNonEmpty(p.Auth, "none"), len(ranges), p.RateAverage))
	res := map[string]any{"protection": protectionOut(p)}
	if err := s.engine.ApplyRoutes(r.Context(), app); err != nil {
		res["routing_warning"] = "Saved, but routing could not be updated: " + err.Error()
	}
	return ok(w, res)
}

// ---- forward authentication ----

const appAuthCookie = "_portdock_auth"

var appAuthCookieTTL = 12 * time.Hour

type ssoClaims struct {
	User  int64  `json:"u"`
	App   string `json:"a"`
	Exp   int64  `json:"e"`
	Nonce string `json:"n,omitempty"`
}

func (s *Server) ssoKey() []byte {
	h := sha256.Sum256(append(s.box.Key(), []byte("portdock forward-auth v1")...))
	return h[:]
}

func (s *Server) signSSO(c ssoClaims) string {
	b, _ := json.Marshal(c)
	p := base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, s.ssoKey())
	mac.Write([]byte(p))
	return p + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) readSSO(v, app string) (ssoClaims, bool) {
	var c ssoClaims
	p, sig, found := strings.Cut(v, ".")
	if !found {
		return c, false
	}
	mac := hmac.New(sha256.New, s.ssoKey())
	mac.Write([]byte(p))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, want) {
		return c, false
	}
	b, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil || json.Unmarshal(b, &c) != nil {
		return c, false
	}
	return c, c.App == app && time.Now().Unix() < c.Exp
}

// usedTickets makes the one-time tickets single use.
var usedTickets = struct {
	sync.Mutex
	m map[string]time.Time
}{m: map[string]time.Time{}}

func useTicket(nonce string) bool {
	usedTickets.Lock()
	defer usedTickets.Unlock()
	now := time.Now()
	for n, exp := range usedTickets.m {
		if now.After(exp) {
			delete(usedTickets.m, n)
		}
	}
	if _, used := usedTickets.m[nonce]; used {
		return false
	}
	usedTickets.m[nonce] = now.Add(5 * time.Minute)
	return true
}

// ssoAllowed reports whether u may use app under p.
func (s *Server) ssoAllowed(ctx context.Context, u store.User, app store.Application, p store.AppProtection) bool {
	if u.Disabled || u.ServiceAccount {
		return false
	}
	if p.SSOEmails != "" {
		email := strings.ToLower(u.Email)
		match := false
		for _, e := range strings.Split(p.SSOEmails, "\n") {
			if email == e || (strings.HasPrefix(e, "@") && strings.HasSuffix(email, e)) {
				match = true
			}
		}
		if !match {
			return false
		}
	}
	if p.SSOAccess == "any" {
		return true
	}
	env, err := store.ByID[store.Environment](ctx, s.db, "environments", app.EnvironmentID)
	if err != nil {
		return false
	}
	return roleRank(s.projectRole(ctx, u, env.ProjectID)) > 0
}

// appHosts lists the host names of app (domains, www variants, previews).
func (s *Server) appHosts(ctx context.Context, app store.Application) []string {
	var hosts []string
	ds, _ := s.db.Domains(ctx, app.ID)
	for _, d := range ds {
		hosts = append(hosts, d.Host)
		if d.WWWRedirect != "none" && d.WWWRedirect != "" {
			a := strings.TrimPrefix(d.Host, "www.")
			hosts = append(hosts, a, "www."+a)
		}
	}
	srv, _ := store.ByID[store.Server](ctx, s.db, "servers", app.ServerID)
	type row struct {
		PR int `db:"pr_number"`
	}
	prs, _ := store.Select[row](ctx, s.db, "SELECT DISTINCT pr_number FROM deployments WHERE application_id = ? AND pr_number > 0", app.ID)
	for _, pr := range prs {
		for _, d := range s.engine.PreviewDomains(app, srv, pr.PR) {
			h, _ := proxy.NormalizeDomain(d)
			hosts = append(hosts, h)
		}
	}
	return hosts
}

func hostMatches(hosts []string, host string) bool {
	host = strings.ToLower(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	for _, h := range hosts {
		if h == host || (strings.HasPrefix(h, "*.") && strings.HasSuffix(host, h[1:]) && !strings.Contains(strings.TrimSuffix(host, h[1:]), ".")) {
			return true
		}
	}
	return false
}

// forwardAuth answers the proxy: 200 with the user's identity, or a redirect
// to the sign-in. It is called for every request to a protected application.
func (s *Server) forwardAuth(w http.ResponseWriter, r *http.Request) error {
	app, err := s.appByUUID(r)
	if err != nil {
		http.Error(w, "unknown application", http.StatusForbidden)
		return nil
	}
	ctx := r.Context()
	p, err := s.db.Protection(ctx, app.ID)
	if err != nil {
		http.Error(w, "protection unavailable", http.StatusServiceUnavailable)
		return nil
	}
	if p.Auth != "sso" {
		w.WriteHeader(http.StatusOK)
		return nil
	}
	proto := firstNonEmpty(r.Header.Get("X-Forwarded-Proto"), "http")
	host := r.Header.Get("X-Forwarded-Host")
	uri := firstNonEmpty(r.Header.Get("X-Forwarded-Uri"), "/")
	ru, err := url.Parse(uri)
	if err != nil || host == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return nil
	}
	secure := proto == "https"
	// Redirects are absolute: the proxy resolves a relative Location against
	// the address it asked (PortDock's), not the application's domain.
	origin := proto + "://" + host
	var user *store.User
	if ck, err := r.Cookie(appAuthCookie); err == nil {
		if c, valid := s.readSSO(ck.Value, app.UUID); valid && c.Nonce == "" {
			if u, err := store.ByID[store.User](ctx, s.db, "users", c.User); err == nil && s.ssoAllowed(ctx, u, app, p) {
				user = &u
			}
		}
	}
	switch ru.Path {
	case "/_portdock/auth":
		rd := origin + safeNext(ru.Query().Get("rd"))
		c, valid := s.readSSO(ru.Query().Get("ticket"), app.UUID)
		if valid && c.Nonce != "" && useTicket(c.Nonce) {
			http.SetCookie(w, &http.Cookie{Name: appAuthCookie, Value: s.signSSO(ssoClaims{User: c.User, App: app.UUID, Exp: time.Now().Add(appAuthCookieTTL).Unix()}),
				Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: int(appAuthCookieTTL.Seconds())})
			http.Redirect(w, r, rd, http.StatusFound)
			return nil
		}
		if user != nil { // already signed in (a reloaded or used ticket)
			http.Redirect(w, r, rd, http.StatusFound)
			return nil
		}
	case "/_portdock/logout":
		http.SetCookie(w, &http.Cookie{Name: appAuthCookie, Value: "", Path: "/", HttpOnly: true, Secure: secure, MaxAge: -1})
		http.Redirect(w, r, origin+"/", http.StatusFound)
		return nil
	}
	if user != nil {
		w.Header().Set("X-Forwarded-User", user.Email)
		w.Header().Set("X-Forwarded-Email", user.Email)
		w.Header().Set("X-Forwarded-Name", user.Name)
		w.WriteHeader(http.StatusOK)
		return nil
	}
	back := proto + "://" + host + uri
	http.Redirect(w, r, s.publicURL(ctx)+"/api/forward-auth/"+app.UUID+"/login?rd="+url.QueryEscape(back), http.StatusFound)
	return nil
}

// forwardAuthLogin runs on PortDock's own domain: it makes sure the visitor
// is signed in to PortDock and may use the application, then sends them back
// with a one-time ticket.
func (s *Server) forwardAuthLogin(w http.ResponseWriter, r *http.Request) error {
	app, err := s.appByUUID(r)
	if err != nil {
		return errStatus(404, "not found")
	}
	ctx := r.Context()
	p, err := s.db.Protection(ctx, app.ID)
	if err != nil || p.Auth != "sso" {
		return errStatus(404, "not found")
	}
	rd, err := url.Parse(r.URL.Query().Get("rd"))
	if err != nil || (rd.Scheme != "http" && rd.Scheme != "https") || !hostMatches(s.appHosts(ctx, app), rd.Host) {
		return badRequest("invalid return address")
	}
	u, sess, err := s.auth.AuthenticateSession(r)
	if err != nil || sess.Method == "token" {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return nil
	}
	if s.needsMFASetup(ctx, u, sess) {
		http.Redirect(w, r, "/", http.StatusFound)
		return nil
	}
	if !s.ssoAllowed(ctx, u, app, p) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Access denied</title><body style="font-family:system-ui;max-width:32rem;margin:15vh auto;padding:0 1rem"><h1>Access denied</h1><p>%s is signed in to PortDock but may not use <b>%s</b>. Ask an administrator of the project for access.</p><p><a href="/">Open PortDock</a></p>`,
			html.EscapeString(u.Email), html.EscapeString(app.Name))
		return nil
	}
	ticket := s.signSSO(ssoClaims{User: u.ID, App: app.UUID, Exp: time.Now().Add(time.Minute).Unix(), Nonce: secrets.RandomToken(12)})
	path := firstNonEmpty(rd.EscapedPath(), "/")
	if rd.RawQuery != "" {
		path += "?" + rd.RawQuery
	}
	target := rd.Scheme + "://" + rd.Host + "/_portdock/auth?ticket=" + url.QueryEscape(ticket) + "&rd=" + url.QueryEscape(path)
	s.db.LogActivity(ctx, u.ID, "application.sso_login", app.Name)
	http.Redirect(w, r, target, http.StatusFound)
	return nil
}
