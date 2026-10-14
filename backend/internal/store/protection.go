package store

import (
	"context"
	"encoding/json"
	"errors"
)

// AppProtection restricts who reaches an application through the proxy:
// allowed addresses, a rate limit and a sign-in (a password or PortDock's
// single sign-on). It applies to every domain of the application, previews
// included.
type AppProtection struct {
	ApplicationID int64 `db:"application_id" json:"application_id"`
	// Auth is "" (none), "password" (basic auth) or "sso" (PortDock sign-in).
	Auth string `db:"auth" json:"auth"`
	// SSOAccess: "project" (people with access to the project) or "any" (every PortDock user).
	SSOAccess string `db:"sso_access" json:"sso_access"`
	// SSOEmails further limits SSO to these addresses or @domains (newline separated).
	SSOEmails   string `db:"sso_emails" json:"sso_emails"`
	BasicUsers  string `db:"basic_users" json:"-"` // JSON []BasicUser
	IPAllow     string `db:"ip_allow" json:"ip_allow"`
	IPDepth     int    `db:"ip_depth" json:"ip_depth"`
	RateAverage int    `db:"rate_average" json:"rate_average"`
	RateBurst   int    `db:"rate_burst" json:"rate_burst"`
	UpdatedAt   string `db:"updated_at" json:"updated_at"`
}

// BasicUser is a user of the password prompt; Hash is bcrypt.
type BasicUser struct {
	User string `json:"user"`
	Hash string `json:"hash"`
}

func (p AppProtection) Users() []BasicUser {
	var us []BasicUser
	_ = json.Unmarshal([]byte(p.BasicUsers), &us)
	return us
}

// Protection loads the protection of an application (the zero value when none).
func (db *DB) Protection(ctx context.Context, appID int64) (AppProtection, error) {
	p, err := Get[AppProtection](ctx, db, "SELECT * FROM app_protection WHERE application_id = ?", appID)
	if errors.Is(err, ErrNotFound) {
		return AppProtection{ApplicationID: appID, SSOAccess: "project"}, nil
	}
	return p, err
}

// SaveProtection stores p, replacing the previous settings.
func (db *DB) SaveProtection(ctx context.Context, p AppProtection) error {
	p.UpdatedAt = Now()
	_, err := db.ExecContext(ctx, `INSERT INTO app_protection(application_id, auth, sso_access, sso_emails, basic_users, ip_allow, ip_depth, rate_average, rate_burst, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(application_id) DO UPDATE SET auth = excluded.auth, sso_access = excluded.sso_access, sso_emails = excluded.sso_emails,
		basic_users = excluded.basic_users, ip_allow = excluded.ip_allow, ip_depth = excluded.ip_depth,
		rate_average = excluded.rate_average, rate_burst = excluded.rate_burst, updated_at = excluded.updated_at`,
		p.ApplicationID, p.Auth, p.SSOAccess, p.SSOEmails, p.BasicUsers, p.IPAllow, p.IPDepth, p.RateAverage, p.RateBurst, p.UpdatedAt)
	return err
}
