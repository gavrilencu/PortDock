package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/shipyard/shipyard/internal/auth"
	"github.com/shipyard/shipyard/internal/ldapauth"
	"github.com/shipyard/shipyard/internal/license"
	"github.com/shipyard/shipyard/internal/secrets"
	"github.com/shipyard/shipyard/internal/sso"
	"github.com/shipyard/shipyard/internal/store"
)

// ---- short-lived server-side state (MFA challenges, SSO logins) ----

type ttlEntry[T any] struct {
	v   T
	exp time.Time
}

type ttlStore[T any] struct {
	mu sync.Mutex
	m  map[string]*ttlEntry[T]
}

func newTTLStore[T any]() *ttlStore[T] { return &ttlStore[T]{m: map[string]*ttlEntry[T]{}} }

func (t *ttlStore[T]) put(v T, ttl time.Duration) string {
	key := secrets.RandomToken(24)
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for k, e := range t.m {
		if now.After(e.exp) {
			delete(t.m, k)
		}
	}
	t.m[key] = &ttlEntry[T]{v: v, exp: now.Add(ttl)}
	return key
}

// update applies fn to the live value under the lock and returns a copy.
func (t *ttlStore[T]) update(key string, fn func(*T)) (T, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.m[key]
	if e == nil || time.Now().After(e.exp) {
		delete(t.m, key)
		var zero T
		return zero, false
	}
	fn(&e.v)
	return e.v, true
}

func (t *ttlStore[T]) take(key string) (T, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.m[key]
	delete(t.m, key)
	if e == nil || time.Now().After(e.exp) {
		var zero T
		return zero, false
	}
	return e.v, true
}

type mfaChallenge struct {
	UserID   int64
	Attempts int
	Base     string // first factor: password | ldap:<slug>
}

type ssoLogin struct {
	ProviderID int64
	Nonce      string
	Verifier   string
	Next       string
}

type security struct {
	limiter    *auth.Limiter // failed password / second-factor attempts
	challenges *ttlStore[mfaChallenge]
	logins     *ttlStore[ssoLogin]
}

func newSecurity() *security {
	return &security{
		limiter:    auth.NewLimiter(10, 15*time.Minute),
		challenges: newTTLStore[mfaChallenge](),
		logins:     newTTLStore[ssoLogin](),
	}
}

// ---- policy ----

// userOut adds derived fields to the user JSON.
type userOut struct {
	store.User
	HasPassword bool `json:"has_password"`
}

func toUserOut(u store.User) userOut { return userOut{User: u, HasPassword: u.HasPassword()} }

// mfaRequired reports whether the organization policy requires 2FA for u.
func (s *Server) mfaRequired(ctx context.Context, u store.User) bool {
	switch s.db.Setting(ctx, "require_2fa") {
	case "all":
		return true
	case "admins":
		return u.IsAdmin()
	}
	return false
}

// needsMFASetup is true when a password session belongs to a user who must enroll
// before using the API. SSO sessions rely on the identity provider's MFA.
func (s *Server) needsMFASetup(ctx context.Context, u store.User, sess store.Session) bool {
	if u.TOTPEnabled || sess.Method == "token" || strings.HasPrefix(sess.Method, "sso:") {
		return false
	}
	return s.mfaRequired(ctx, u)
}

func (s *Server) passwordLoginAllowed(ctx context.Context, u store.User) bool {
	return s.db.Setting(ctx, "password_login") != "false" || u.Role == "owner"
}

func retryIn(d time.Duration) string {
	m := int(d.Minutes()) + 1
	if m == 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", m)
}

// ---- login ----

func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	var c credentials
	if err := readJSON(r, &c); err != nil {
		return err
	}
	ctx := r.Context()
	email := strings.ToLower(strings.TrimSpace(c.Email))
	ipKey, emailKey := "ip:"+auth.ClientIP(r), "email:"+email
	for _, k := range []string{ipKey, emailKey} {
		if left, blocked := s.sec.limiter.Blocked(k); blocked {
			return errStatus(429, "too many failed sign-in attempts; try again in %s", retryIn(left))
		}
	}
	base := "password"
	u, err := store.Get[store.User](ctx, s.db, "SELECT * FROM users WHERE email = ?", email)
	if err != nil || !auth.CheckPassword(u.PasswordHash, c.Password) {
		// Not a local password: try LDAP / Active Directory (username, UPN or email).
		du, dir, derr := s.ldapLogin(ctx, strings.TrimSpace(c.Email), c.Password)
		switch {
		case derr == nil:
			u, err, base = du, nil, "ldap:"+dir.Slug
		case errors.Is(derr, errNoDirectoryUser) || errors.Is(derr, ldapauth.ErrInvalidCredentials):
			s.sec.limiter.Fail(ipKey)
			s.sec.limiter.Fail(emailKey)
			s.db.LogActivity(ctx, u.ID, "login.failed", email+" from "+auth.ClientIP(r))
			if err == nil && !u.HasPassword() && !strings.HasPrefix(u.AuthSource, "ldap:") {
				return errStatus(401, "this account signs in with single sign-on")
			}
			return errStatus(401, "invalid username or password")
		default:
			s.sec.limiter.Fail(ipKey)
			s.db.LogActivity(ctx, 0, "login.failed", email+" via "+dir.Name+": "+derr.Error())
			return errStatus(401, "%s: %v", dir.Name, derr)
		}
	}
	if u.Disabled {
		return errStatus(403, "this account has been disabled; contact an administrator")
	}
	if base == "password" && !s.passwordLoginAllowed(ctx, u) {
		return errStatus(403, "password sign-in is disabled for your organization; use single sign-on")
	}
	s.sec.limiter.Reset(emailKey)
	if u.TOTPEnabled {
		token := s.sec.challenges.put(mfaChallenge{UserID: u.ID, Base: base}, 5*time.Minute)
		return ok(w, map[string]any{"mfa_required": true, "mfa_token": token})
	}
	if err := s.auth.CreateSession(ctx, w, r, u.ID, base); err != nil {
		return err
	}
	if base == "password" {
		s.db.LogActivity(ctx, u.ID, "login", u.Email)
	} else {
		s.db.LogActivity(ctx, u.ID, "login", u.Email+" via "+strings.TrimPrefix(base, "ldap:")+" (LDAP)")
	}
	return ok(w, toUserOut(u))
}

// loginMFA completes a password login with a TOTP or recovery code.
func (s *Server) loginMFA(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Token string `json:"mfa_token"`
		Code  string `json:"code"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	ch, found := s.sec.challenges.update(in.Token, func(c *mfaChallenge) { c.Attempts++ })
	if !found {
		return errStatus(401, "the sign-in session expired; enter your password again")
	}
	if ch.Attempts > 5 {
		s.sec.challenges.take(in.Token)
		return errStatus(429, "too many incorrect codes; enter your password again")
	}
	u, err := store.ByID[store.User](ctx, s.db, "users", ch.UserID)
	if err != nil || u.Disabled {
		return errStatus(401, "the sign-in session expired; enter your password again")
	}
	method, valid := s.verifySecondFactor(ctx, &u, in.Code)
	if !valid {
		s.sec.limiter.Fail("ip:" + auth.ClientIP(r))
		s.db.LogActivity(ctx, u.ID, "login.mfa_failed", u.Email)
		return errStatus(401, "invalid authentication code")
	}
	s.sec.challenges.take(in.Token)
	first := ch.Base
	if first == "" {
		first = "password"
	}
	if err := s.auth.CreateSession(ctx, w, r, u.ID, first+"+"+method); err != nil {
		return err
	}
	s.db.LogActivity(ctx, u.ID, "login", u.Email+" (2FA: "+method+")")
	if method == "recovery" {
		s.db.LogActivity(ctx, u.ID, "2fa.recovery_code_used", fmt.Sprintf("%d left", auth.RecoveryCodesLeft(u.RecoveryCodes)))
	}
	return ok(w, toUserOut(u))
}

// verifySecondFactor accepts a TOTP code (rejecting replays) or consumes a
// recovery code, persisting the change. It returns "totp" or "recovery".
func (s *Server) verifySecondFactor(ctx context.Context, u *store.User, code string) (string, bool) {
	if !u.TOTPEnabled {
		return "", false
	}
	secret, err := s.box.Decrypt(u.TOTPSecret)
	if err != nil {
		return "", false
	}
	if step, ok := auth.VerifyTOTP(secret, code, time.Now()); ok {
		if step <= u.TOTPLastStep {
			return "", false // code already used
		}
		u.TOTPLastStep = step
		_, _ = s.db.ExecContext(ctx, "UPDATE users SET totp_last_step = ? WHERE id = ?", step, u.ID)
		return "totp", true
	}
	if rest, ok := auth.UseRecoveryCode(u.RecoveryCodes, code); ok {
		u.RecoveryCodes = rest
		_, _ = s.db.ExecContext(ctx, "UPDATE users SET recovery_codes = ? WHERE id = ?", rest, u.ID)
		return "recovery", true
	}
	return "", false
}

// ---- 2FA management (current user) ----

func (s *Server) twoFactorStatus(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFrom(r.Context())
	return ok(w, map[string]any{
		"enabled":             u.TOTPEnabled,
		"required":            s.mfaRequired(r.Context(), u),
		"recovery_codes_left": auth.RecoveryCodesLeft(u.RecoveryCodes),
	})
}

func (s *Server) twoFactorSetup(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	u := auth.UserFrom(r.Context())
	if u.TOTPEnabled {
		return badRequest("two-factor authentication is already enabled")
	}
	if u.HasPassword() && !auth.CheckPassword(u.PasswordHash, in.Password) {
		return badRequest("current password is incorrect")
	}
	secret := auth.NewTOTPSecret()
	if _, err := s.db.ExecContext(r.Context(), "UPDATE users SET totp_secret = ? WHERE id = ?", s.box.Encrypt(secret), u.ID); err != nil {
		return err
	}
	issuer := s.db.Setting(r.Context(), "instance_name")
	if issuer == "" {
		issuer = "Shipyard"
	}
	return ok(w, map[string]string{"secret": secret, "otpauth_url": auth.TOTPURL(issuer, u.Email, secret)})
}

func (s *Server) twoFactorEnable(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	u := auth.UserFrom(r.Context())
	if u.TOTPEnabled {
		return badRequest("two-factor authentication is already enabled")
	}
	secret, err := s.box.Decrypt(u.TOTPSecret)
	if err != nil || secret == "" {
		return badRequest("start the setup again")
	}
	step, valid := auth.VerifyTOTP(secret, in.Code, time.Now())
	if !valid {
		return badRequest("the code is incorrect — check that your device's clock is correct")
	}
	codes, stored := auth.NewRecoveryCodes(10)
	if _, err := s.db.ExecContext(r.Context(), "UPDATE users SET totp_enabled = 1, totp_last_step = ?, recovery_codes = ? WHERE id = ?", step, stored, u.ID); err != nil {
		return err
	}
	// Other browsers were signed in with the password alone.
	_ = s.auth.RevokeSessions(r.Context(), u.ID, auth.SessionFrom(r.Context()).TokenHash)
	s.activity(r, "2fa.enable", u.Email)
	return ok(w, map[string]any{"recovery_codes": codes})
}

func (s *Server) twoFactorDisable(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	u := auth.UserFrom(ctx)
	if !u.TOTPEnabled {
		return badRequest("two-factor authentication is not enabled")
	}
	if s.mfaRequired(ctx, u) {
		return badRequest("your organization requires two-factor authentication")
	}
	if u.HasPassword() && !auth.CheckPassword(u.PasswordHash, in.Password) {
		return badRequest("current password is incorrect")
	}
	if _, valid := s.verifySecondFactor(ctx, &u, in.Code); !valid {
		return badRequest("invalid authentication code")
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE users SET totp_enabled = 0, totp_secret = '', recovery_codes = '', totp_last_step = 0 WHERE id = ?", u.ID); err != nil {
		return err
	}
	s.activity(r, "2fa.disable", u.Email)
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) regenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	u := auth.UserFrom(ctx)
	if method, valid := s.verifySecondFactor(ctx, &u, in.Code); !valid || method != "totp" {
		return badRequest("enter a current code from your authenticator app")
	}
	codes, stored := auth.NewRecoveryCodes(10)
	if _, err := s.db.ExecContext(ctx, "UPDATE users SET recovery_codes = ? WHERE id = ?", stored, u.ID); err != nil {
		return err
	}
	s.activity(r, "2fa.recovery_codes_regenerated", u.Email)
	return ok(w, map[string]any{"recovery_codes": codes})
}

// resetUserTwoFactor lets an admin remove a user's 2FA (lost device).
func (s *Server) resetUserTwoFactor(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	u, err := store.ByID[store.User](r.Context(), s.db, "users", id)
	if err != nil {
		return err
	}
	if u.Role == "owner" && auth.UserFrom(r.Context()).Role != "owner" {
		return errStatus(403, "only the owner can reset the owner's two-factor authentication")
	}
	if _, err := s.db.ExecContext(r.Context(), "UPDATE users SET totp_enabled = 0, totp_secret = '', recovery_codes = '', totp_last_step = 0 WHERE id = ?", id); err != nil {
		return err
	}
	_ = s.auth.RevokeSessions(r.Context(), id, "")
	s.activity(r, "2fa.reset", u.Email)
	return ok(w, map[string]bool{"ok": true})
}

// ---- sessions ----

type sessionOut struct {
	ID         string `json:"id"`
	Current    bool   `json:"current"`
	IP         string `json:"ip"`
	UserAgent  string `json:"user_agent"`
	Method     string `json:"method"`
	CreatedAt  string `json:"created_at"`
	LastSeenAt string `json:"last_seen_at"`
	ExpiresAt  string `json:"expires_at"`
}

var sessionIDRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	cur := auth.SessionFrom(ctx).TokenHash
	rows, err := store.Select[store.Session](ctx, s.db, "SELECT * FROM sessions WHERE user_id = ? AND expires_at > ? ORDER BY last_seen_at DESC", auth.UserFrom(ctx).ID, store.Now())
	if err != nil {
		return err
	}
	out := make([]sessionOut, 0, len(rows))
	for _, x := range rows {
		out = append(out, sessionOut{ID: x.TokenHash[:16], Current: x.TokenHash == cur, IP: x.IP, UserAgent: x.UserAgent, Method: x.Method,
			CreatedAt: x.CreatedAt, LastSeenAt: x.LastSeenAt, ExpiresAt: x.ExpiresAt})
	}
	return ok(w, out)
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) error {
	sid := r.PathValue("sid")
	if !sessionIDRe.MatchString(sid) {
		return badRequest("invalid session id")
	}
	if _, err := s.db.ExecContext(r.Context(), "DELETE FROM sessions WHERE user_id = ? AND substr(token_hash, 1, 16) = ?", auth.UserFrom(r.Context()).ID, sid); err != nil {
		return err
	}
	s.activity(r, "session.revoke", sid)
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) revokeOtherSessions(w http.ResponseWriter, r *http.Request) error {
	if err := s.auth.RevokeSessions(r.Context(), auth.UserFrom(r.Context()).ID, auth.SessionFrom(r.Context()).TokenHash); err != nil {
		return err
	}
	s.activity(r, "session.revoke_others", "")
	return ok(w, map[string]bool{"ok": true})
}

// ---- SSO: login flow ----

func (s *Server) listLoginProviders(w http.ResponseWriter, r *http.Request) error {
	ps, err := store.Select[store.SSOProvider](r.Context(), s.db, "SELECT * FROM sso_providers WHERE enabled = 1 ORDER BY id")
	if err != nil {
		return err
	}
	type item struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	out := make([]item, 0, len(ps))
	for _, p := range ps {
		out = append(out, item{p.Slug, p.Name, p.Kind})
	}
	dirs, _ := store.Select[store.LDAPDirectory](r.Context(), s.db, "SELECT * FROM ldap_directories WHERE enabled = 1 ORDER BY id")
	ldapList := make([]item, 0, len(dirs))
	for _, d := range dirs {
		ldapList = append(ldapList, item{d.Slug, d.Name, d.Kind})
	}
	return ok(w, map[string]any{"password_login": s.db.Setting(r.Context(), "password_login") != "false", "providers": out, "ldap": ldapList})
}

func (s *Server) ssoRedirectURI(ctx context.Context, slug string) string {
	return strings.TrimRight(s.publicURL(ctx), "/") + "/api/auth/sso/" + slug + "/callback"
}

const ssoCookie = "shipyard_sso"

// safeNext keeps post-login redirects on this site.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}

func (s *Server) ssoFail(w http.ResponseWriter, r *http.Request, msg string) error {
	http.Redirect(w, r, "/login?sso_error="+url.QueryEscape(msg), http.StatusFound)
	return nil
}

func (s *Server) ssoStart(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p, err := store.Get[store.SSOProvider](ctx, s.db, "SELECT * FROM sso_providers WHERE slug = ? AND enabled = 1", r.PathValue("slug"))
	if err != nil {
		return s.ssoFail(w, r, "This sign-in method is not available.")
	}
	meta, err := sso.Discover(ctx, p.Issuer)
	if err != nil {
		return s.ssoFail(w, r, p.Name+" is unreachable: "+err.Error())
	}
	l := ssoLogin{ProviderID: p.ID, Nonce: secrets.RandomToken(16), Verifier: secrets.RandomToken(32), Next: safeNext(r.URL.Query().Get("next"))}
	state := s.sec.logins.put(l, 10*time.Minute)
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: state, Path: "/api/auth/sso/", MaxAge: 600,
		HttpOnly: true, Secure: s.auth.SecureCookies(), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, sso.AuthURL(meta, p.ClientID, s.ssoRedirectURI(ctx, p.Slug), p.Scopes, state, l.Nonce, l.Verifier), http.StatusFound)
	return nil
}

func splitCSV(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func (s *Server) ssoCallback(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	q := r.URL.Query()
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: "", Path: "/api/auth/sso/", MaxAge: -1, HttpOnly: true})
	if e := q.Get("error"); e != "" {
		msg := q.Get("error_description")
		if msg == "" {
			msg = e
		}
		return s.ssoFail(w, r, "The identity provider refused the sign-in: "+msg)
	}
	state := q.Get("state")
	c, err := r.Cookie(ssoCookie)
	if err != nil || state == "" || c.Value != state {
		return s.ssoFail(w, r, "The sign-in session expired or was started in another browser. Please try again.")
	}
	l, found := s.sec.logins.take(state)
	if !found {
		return s.ssoFail(w, r, "The sign-in session expired. Please try again.")
	}
	p, err := store.ByID[store.SSOProvider](ctx, s.db, "sso_providers", l.ProviderID)
	if err != nil || !p.Enabled || p.Slug != r.PathValue("slug") {
		return s.ssoFail(w, r, "This sign-in method is not available.")
	}
	meta, err := sso.Discover(ctx, p.Issuer)
	if err != nil {
		return s.ssoFail(w, r, err.Error())
	}
	claims, err := sso.Exchange(ctx, meta, p.ClientID, s.box.MustDecrypt(p.ClientSecret), s.ssoRedirectURI(ctx, p.Slug), q.Get("code"), l.Verifier, l.Nonce, p.GroupsClaim)
	if err != nil {
		s.db.LogActivity(ctx, 0, "sso.failed", p.Name+": "+err.Error())
		return s.ssoFail(w, r, err.Error())
	}
	u, err := s.ssoResolveUser(ctx, p, claims)
	if err != nil {
		s.db.LogActivity(ctx, 0, "sso.denied", p.Name+": "+claims.Email+": "+err.Error())
		return s.ssoFail(w, r, err.Error())
	}
	if err := s.auth.CreateSession(ctx, w, r, u.ID, "sso:"+p.Slug); err != nil {
		return s.ssoFail(w, r, err.Error())
	}
	s.db.LogActivity(ctx, u.ID, "login", u.Email+" via "+p.Name)
	http.Redirect(w, r, l.Next, http.StatusFound)
	return nil
}

func emailDomainAllowed(p store.SSOProvider, email string) bool {
	allowed := splitCSV(strings.ToLower(p.AllowedDomains))
	if len(allowed) == 0 {
		return true
	}
	at := strings.LastIndex(email, "@")
	return at > 0 && slices.Contains(allowed, strings.TrimPrefix(email[at+1:], "@"))
}

// ssoResolveUser maps claims to a user: an existing identity link, an existing
// account with the same verified email, or a new account (just-in-time).
func (s *Server) ssoResolveUser(ctx context.Context, p store.SSOProvider, c sso.Claims) (store.User, error) {
	if c.Email != "" && !emailDomainAllowed(p, c.Email) {
		return store.User{}, fmt.Errorf("accounts from %s are not allowed to sign in", c.Email[strings.LastIndex(c.Email, "@")+1:])
	}
	if c.Email == "" && p.AllowedDomains != "" {
		return store.User{}, fmt.Errorf("the identity provider did not return an email address")
	}
	var u store.User
	id, err := store.Get[store.UserIdentity](ctx, s.db, "SELECT * FROM user_identities WHERE provider_id = ? AND subject = ?", p.ID, c.Subject)
	if err == nil {
		u, err = store.ByID[store.User](ctx, s.db, "users", id.UserID)
	}
	if err != nil {
		if c.Email == "" {
			return store.User{}, fmt.Errorf("the identity provider did not return an email address — add the 'email' scope")
		}
		verified := p.TrustEmail
		if c.EmailVerified != nil {
			verified = *c.EmailVerified
		}
		existing, gerr := store.Get[store.User](ctx, s.db, "SELECT * FROM users WHERE email = ?", c.Email)
		switch {
		case gerr == nil && !verified:
			return store.User{}, fmt.Errorf("%s is not verified by the identity provider, so it cannot be linked to the existing account", c.Email)
		case gerr == nil:
			u = existing
		case !p.AutoRegister:
			return store.User{}, fmt.Errorf("there is no account for %s — ask an administrator to invite you", c.Email)
		default:
			if err := s.checkUserLimit(ctx); err != nil {
				return store.User{}, fmt.Errorf("cannot create an account for %s: the user limit of the license is reached", c.Email)
			}
			name := c.Name
			if name == "" {
				name = strings.Split(c.Email, "@")[0]
			}
			u = store.User{Email: c.Email, Name: name, Role: p.DefaultRole, AuthSource: p.Slug, CreatedAt: store.Now()}
			if u.Role != "admin" {
				u.Role = "member"
			}
			if _, err := store.Insert(ctx, s.db, "users", &u); err != nil {
				return store.User{}, err
			}
			s.db.LogActivity(ctx, u.ID, "user.provisioned", u.Email+" via "+p.Name)
		}
		link := store.UserIdentity{UserID: u.ID, ProviderID: p.ID, Subject: c.Subject, Email: c.Email, CreatedAt: store.Now()}
		if _, err := store.Insert(ctx, s.db, "user_identities", &link); err != nil {
			return store.User{}, err
		}
	}
	if u.Disabled {
		return store.User{}, fmt.Errorf("this account has been disabled; contact an administrator")
	}
	if admins := splitCSV(p.AdminGroups); len(admins) > 0 && u.Role != "owner" {
		role := "member"
		if p.DefaultRole == "admin" {
			role = "admin"
		}
		for _, g := range c.Groups {
			if slices.Contains(admins, g) {
				role = "admin"
			}
		}
		if role != u.Role {
			u.Role = role
			_, _ = s.db.ExecContext(ctx, "UPDATE users SET role = ? WHERE id = ?", role, u.ID)
			s.db.LogActivity(ctx, u.ID, "user.role_synced", u.Email+" → "+role)
		}
	}
	if c.Name != "" && u.Name == "" {
		_, _ = s.db.ExecContext(ctx, "UPDATE users SET name = ? WHERE id = ?", c.Name, u.ID)
	}
	return u, nil
}

// ---- SSO: provider administration ----

type ssoProviderOut struct {
	store.SSOProvider
	ClientSecretSet bool   `json:"client_secret_set"`
	RedirectURI     string `json:"redirect_uri"`
	Users           int    `json:"users"`
}

func (s *Server) ssoOut(ctx context.Context, p store.SSOProvider) ssoProviderOut {
	var n int
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_identities WHERE provider_id = ?", p.ID).Scan(&n)
	return ssoProviderOut{SSOProvider: p, ClientSecretSet: p.ClientSecret != "", RedirectURI: s.ssoRedirectURI(ctx, p.Slug), Users: n}
}

func (s *Server) listSSOProviders(w http.ResponseWriter, r *http.Request) error {
	ps, err := store.Select[store.SSOProvider](r.Context(), s.db, "SELECT * FROM sso_providers ORDER BY id")
	if err != nil {
		return err
	}
	out := make([]ssoProviderOut, 0, len(ps))
	for _, p := range ps {
		out = append(out, s.ssoOut(r.Context(), p))
	}
	return ok(w, map[string]any{"providers": out, "presets": sso.Presets, "redirect_base": s.ssoRedirectURI(r.Context(), "{slug}")})
}

type ssoProviderIn struct {
	Name           *string `json:"name"`
	Slug           *string `json:"slug"`
	Kind           *string `json:"kind"`
	Issuer         *string `json:"issuer"`
	ClientID       *string `json:"client_id"`
	ClientSecret   *string `json:"client_secret"`
	Scopes         *string `json:"scopes"`
	Enabled        *bool   `json:"enabled"`
	AutoRegister   *bool   `json:"auto_register"`
	DefaultRole    *string `json:"default_role"`
	AllowedDomains *string `json:"allowed_domains"`
	GroupsClaim    *string `json:"groups_claim"`
	AdminGroups    *string `json:"admin_groups"`
	TrustEmail     *bool   `json:"trust_email"`
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

func slugify(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}

func (s *Server) applySSOProvider(p *store.SSOProvider, in ssoProviderIn) error {
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	set(&p.Name, in.Name)
	set(&p.Slug, in.Slug)
	set(&p.Kind, in.Kind)
	set(&p.Issuer, in.Issuer)
	set(&p.ClientID, in.ClientID)
	set(&p.Scopes, in.Scopes)
	set(&p.DefaultRole, in.DefaultRole)
	set(&p.AllowedDomains, in.AllowedDomains)
	set(&p.GroupsClaim, in.GroupsClaim)
	set(&p.AdminGroups, in.AdminGroups)
	if in.ClientSecret != nil && *in.ClientSecret != "" {
		p.ClientSecret = s.box.Encrypt(strings.TrimSpace(*in.ClientSecret))
	}
	for dst, v := range map[*bool]*bool{&p.Enabled: in.Enabled, &p.AutoRegister: in.AutoRegister, &p.TrustEmail: in.TrustEmail} {
		if v != nil {
			*dst = *v
		}
	}
	if p.Name == "" {
		return badRequest("name is required")
	}
	if p.Slug == "" {
		p.Slug = slugify(p.Name)
	}
	if !slugRe.MatchString(p.Slug) {
		return badRequest("slug may contain lowercase letters, digits and dashes")
	}
	if p.Kind == "" {
		p.Kind = "oidc"
	}
	iu, err := url.Parse(p.Issuer)
	if err != nil || (iu.Scheme != "https" && iu.Scheme != "http") || iu.Host == "" || strings.ContainsAny(p.Issuer, "{}") {
		return badRequest("issuer must be a URL such as https://login.microsoftonline.com/<tenant-id>/v2.0")
	}
	p.Issuer = strings.TrimRight(p.Issuer, "/")
	if p.ClientID == "" {
		return badRequest("client ID is required")
	}
	if !slices.Contains(strings.Fields(p.Scopes), "openid") {
		p.Scopes = strings.TrimSpace("openid " + p.Scopes)
	}
	if p.DefaultRole != "admin" {
		p.DefaultRole = "member"
	}
	if p.GroupsClaim == "" {
		p.GroupsClaim = "groups"
	}
	p.AllowedDomains = strings.ToLower(strings.Join(splitCSV(p.AllowedDomains), ","))
	p.AdminGroups = strings.Join(splitCSV(p.AdminGroups), ",")
	return nil
}

func (s *Server) createSSOProvider(w http.ResponseWriter, r *http.Request) error {
	var in ssoProviderIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.requireFeature(r.Context(), license.FeatureSSO); err != nil {
		return err
	}
	p := store.SSOProvider{Enabled: true, TrustEmail: true, Scopes: "openid email profile", GroupsClaim: "groups", DefaultRole: "member", CreatedAt: store.Now()}
	if err := s.applySSOProvider(&p, in); err != nil {
		return err
	}
	if _, err := store.Insert(r.Context(), s.db, "sso_providers", &p); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return badRequest("a provider with the slug %q already exists", p.Slug)
		}
		return err
	}
	s.activity(r, "sso.provider.create", p.Name)
	return ok(w, s.ssoOut(r.Context(), p))
}

func (s *Server) updateSSOProvider(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	p, err := store.ByID[store.SSOProvider](r.Context(), s.db, "sso_providers", id)
	if err != nil {
		return err
	}
	var in ssoProviderIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.Enabled != nil && *in.Enabled && !p.Enabled {
		if err := s.requireFeature(r.Context(), license.FeatureSSO); err != nil {
			return err
		}
	}
	if err := s.applySSOProvider(&p, in); err != nil {
		return err
	}
	if err := store.Update(r.Context(), s.db, "sso_providers", &p); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return badRequest("a provider with the slug %q already exists", p.Slug)
		}
		return err
	}
	s.activity(r, "sso.provider.update", p.Name)
	return ok(w, s.ssoOut(r.Context(), p))
}

func (s *Server) deleteSSOProvider(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	p, err := store.ByID[store.SSOProvider](r.Context(), s.db, "sso_providers", id)
	if err != nil {
		return err
	}
	if err := store.Delete(r.Context(), s.db, "sso_providers", id); err != nil {
		return err
	}
	s.activity(r, "sso.provider.delete", p.Name)
	return ok(w, map[string]bool{"ok": true})
}

// discoverSSO validates an issuer before saving.
func (s *Server) discoverSSO(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Issuer string `json:"issuer"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	m, err := sso.Discover(ctx, in.Issuer)
	if err != nil {
		return badRequest("%v", err)
	}
	return ok(w, m)
}

func (s *Server) revokeUserSessions(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	u, err := store.ByID[store.User](r.Context(), s.db, "users", id)
	if err != nil {
		return err
	}
	if err := s.auth.RevokeSessions(r.Context(), id, auth.SessionFrom(r.Context()).TokenHash); err != nil {
		return err
	}
	s.activity(r, "session.revoke_all", u.Email)
	return ok(w, map[string]bool{"ok": true})
}
