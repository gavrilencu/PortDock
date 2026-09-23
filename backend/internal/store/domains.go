package store

import (
	"context"
	"strings"
)

// AppDomain is a host (plus optional path prefix) routed to an application.
type AppDomain struct {
	ID            int64  `db:"id" json:"id"`
	ApplicationID int64  `db:"application_id" json:"application_id"`
	Host          string `db:"host" json:"host"`
	Path          string `db:"path" json:"path"`
	Port          int    `db:"port" json:"port"`
	HTTPS         bool   `db:"https" json:"https"`
	WWWRedirect   string `db:"www_redirect" json:"www_redirect"`
	Indexing      bool   `db:"indexing" json:"indexing"`
	DNSStatus     string `db:"dns_status" json:"dns_status"`
	DNSDetail     string `db:"dns_detail" json:"dns_detail"`
	DNSCheckedAt  string `db:"dns_checked_at" json:"dns_checked_at"`
	CreatedAt     string `db:"created_at" json:"created_at"`
}

// URL renders the domain as it is reached by visitors.
func (d AppDomain) URL() string {
	scheme := "https://"
	if !d.HTTPS {
		scheme = "http://"
	}
	return scheme + d.Host + d.Path
}

func (db *DB) Domains(ctx context.Context, appID int64) ([]AppDomain, error) {
	return Select[AppDomain](ctx, db, "SELECT * FROM app_domains WHERE application_id = ? ORDER BY id", appID)
}

// SyncDomainsColumn refreshes applications.domains, a denormalized
// comma-separated copy used for display and search.
func (db *DB) SyncDomainsColumn(ctx context.Context, appID int64) error {
	ds, err := db.Domains(ctx, appID)
	if err != nil {
		return err
	}
	parts := make([]string, 0, len(ds))
	for _, d := range ds {
		parts = append(parts, d.Host+d.Path)
	}
	_, err = db.ExecContext(ctx, "UPDATE applications SET domains = ? WHERE id = ?", strings.Join(parts, ","), appID)
	return err
}

// backfillDomains converts the legacy comma-separated applications.domains
// column into app_domains rows (runs once per application).
func (db *DB) backfillDomains(ctx context.Context) error {
	type row struct {
		ID      int64  `db:"id"`
		Domains string `db:"domains"`
		HTTPS   bool   `db:"https"`
	}
	apps, err := Select[row](ctx, db, `SELECT id, domains, https FROM applications
		WHERE domains != '' AND id NOT IN (SELECT application_id FROM app_domains)`)
	if err != nil {
		return err
	}
	for _, a := range apps {
		for _, raw := range SplitList(a.Domains) {
			https := a.HTTPS
			if strings.HasPrefix(raw, "http://") {
				https = false
			}
			raw = strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
			host, path, _ := strings.Cut(raw, "/")
			if path != "" {
				path = "/" + strings.TrimSuffix(path, "/")
			}
			_, _ = db.ExecContext(ctx, `INSERT OR IGNORE INTO app_domains(application_id, host, path, https, created_at) VALUES(?,?,?,?,?)`,
				a.ID, strings.ToLower(host), path, https, Now())
		}
	}
	return nil
}
