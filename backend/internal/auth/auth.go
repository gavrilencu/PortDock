// Package auth handles passwords, cookie sessions, API tokens, TOTP two-factor
// authentication and login throttling.
package auth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
	"golang.org/x/crypto/bcrypt"
)

const (
	CookieName  = "portdock_session"
	sessionTTL  = 30 * 24 * time.Hour
	TokenPrefix = "pd_"
	// LegacyCookieName keeps users signed in across the rename.
	LegacyCookieName = "shipyard_session"
)

type ctxKey struct{}
type sessionKey struct{}

func HashPassword(p string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	return string(b), err
}

func CheckPassword(hash, p string) bool {
	if hash == "" {
		return false // SSO-only account
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(p)) == nil
}

type Auth struct {
	db     *store.DB
	secure bool

	mu      sync.Mutex
	touched map[string]time.Time // session hash -> last last_seen_at write
}

func New(db *store.DB, secureCookies bool) *Auth {
	return &Auth{db: db, secure: secureCookies, touched: map[string]time.Time{}}
}

// ClientIP returns the caller address, honouring X-Forwarded-For / X-Real-Ip
// set by the reverse proxy in front of PortDock.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	if ip := r.Header.Get("X-Real-Ip"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// CreateSession issues a session cookie for the user. method records how the
// user authenticated (password, password+totp, password+recovery, sso:<slug>).
func (a *Auth) CreateSession(ctx context.Context, w http.ResponseWriter, r *http.Request, userID int64, method string) error {
	token := secrets.RandomToken(32)
	exp := time.Now().Add(sessionTTL)
	now := store.Now()
	ua := r.UserAgent()
	if len(ua) > 300 {
		ua = ua[:300]
	}
	if _, err := a.db.ExecContext(ctx,
		"INSERT INTO sessions(token_hash, user_id, expires_at, created_at, last_seen_at, ip, user_agent, method) VALUES(?,?,?,?,?,?,?,?)",
		secrets.Hash(token), userID, exp.UTC().Format(time.RFC3339), now, now, ClientIP(r), ua, method); err != nil {
		return err
	}
	_, _ = a.db.ExecContext(ctx, "UPDATE users SET last_login_at = ? WHERE id = ?", now, userID)
	_, _ = a.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < ?", now)
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/", Expires: exp,
		HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (a *Auth) DestroySession(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{CookieName, LegacyCookieName} {
		if c, err := r.Cookie(name); err == nil {
			_, _ = a.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", secrets.Hash(c.Value))
		}
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	}
}

// RevokeSessions deletes all sessions of a user except keepHash (may be "").
func (a *Auth) RevokeSessions(ctx context.Context, userID int64, keepHash string) error {
	_, err := a.db.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ? AND token_hash != ?", userID, keepHash)
	return err
}

// SecureCookies reports whether cookies are marked Secure (HTTPS deployments).
func (a *Auth) SecureCookies() bool { return a.secure }

// CreateToken returns a new plaintext API token (shown once).
func (a *Auth) CreateToken(ctx context.Context, userID int64, name string) (string, store.APIToken, error) {
	plain := TokenPrefix + secrets.RandomToken(24)
	t := store.APIToken{UserID: userID, Name: name, TokenHash: secrets.Hash(plain), CreatedAt: store.Now()}
	_, err := store.Insert(ctx, a.db, "api_tokens", &t)
	return plain, t, err
}

var ErrUnauthorized = errors.New("unauthorized")

// Authenticate resolves the user from a Bearer token or session cookie.
func (a *Auth) Authenticate(r *http.Request) (store.User, error) {
	u, _, err := a.AuthenticateSession(r)
	return u, err
}

// AuthenticateSession is Authenticate that also returns the browser session
// (Method "token" for API tokens).
func (a *Auth) AuthenticateSession(r *http.Request) (store.User, store.Session, error) {
	ctx := r.Context()
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok := strings.TrimPrefix(h, "Bearer ")
		t, err := store.Get[store.APIToken](ctx, a.db, "SELECT * FROM api_tokens WHERE token_hash = ?", secrets.Hash(tok))
		if err != nil {
			return store.User{}, store.Session{}, ErrUnauthorized
		}
		u, err := store.ByID[store.User](ctx, a.db, "users", t.UserID)
		if err != nil || u.Disabled {
			return store.User{}, store.Session{}, ErrUnauthorized
		}
		_, _ = a.db.ExecContext(ctx, "UPDATE api_tokens SET last_used_at = ? WHERE id = ?", store.Now(), t.ID)
		return u, store.Session{UserID: u.ID, Method: "token"}, nil
	}
	c, err := r.Cookie(CookieName)
	if err != nil {
		c, err = r.Cookie(LegacyCookieName)
	}
	if err != nil {
		return store.User{}, store.Session{}, ErrUnauthorized
	}
	hash := secrets.Hash(c.Value)
	type row struct {
		store.User
		SMethod  string `db:"s_method"`
		SCreated string `db:"s_created"`
	}
	res, err := store.Get[row](ctx, a.db,
		"SELECT u.*, s.method AS s_method, s.created_at AS s_created FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = ? AND s.expires_at > ?",
		hash, store.Now())
	if err != nil || res.Disabled {
		return store.User{}, store.Session{}, ErrUnauthorized
	}
	a.touch(ctx, hash)
	return res.User, store.Session{TokenHash: hash, UserID: res.ID, Method: res.SMethod, CreatedAt: res.SCreated}, nil
}

// touch updates last_seen_at at most every 5 minutes per session.
func (a *Auth) touch(ctx context.Context, hash string) {
	a.mu.Lock()
	fresh := time.Since(a.touched[hash]) < 5*time.Minute
	if !fresh {
		if len(a.touched) > 10_000 {
			clear(a.touched)
		}
		a.touched[hash] = time.Now()
	}
	a.mu.Unlock()
	if !fresh {
		_, _ = a.db.ExecContext(ctx, "UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?", store.Now(), hash)
	}
}

func WithUser(ctx context.Context, u store.User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

func UserFrom(ctx context.Context) store.User {
	u, _ := ctx.Value(ctxKey{}).(store.User)
	return u
}

func WithSession(ctx context.Context, s store.Session) context.Context {
	return context.WithValue(ctx, sessionKey{}, s)
}

// SessionFrom returns the current session (Method "token" for API tokens).
func SessionFrom(ctx context.Context) store.Session {
	s, _ := ctx.Value(sessionKey{}).(store.Session)
	return s
}
