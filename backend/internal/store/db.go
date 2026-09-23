// Package store is the persistence layer. It uses SQLite (pure Go driver) and a
// small reflection-based mapper keyed by `db:"column"` struct tags.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

var ErrNotFound = errors.New("not found")

type DB struct {
	*sql.DB
}

func Open(path string) (*DB, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; a single connection avoids SQLITE_BUSY storms.
	sqldb.SetMaxOpenConns(1)
	if _, err := sqldb.Exec(schema); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	for _, m := range migrations {
		if _, err := sqldb.Exec(m); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return nil, fmt.Errorf("migrate %q: %w", m, err)
		}
	}
	db := &DB{sqldb}
	if err := db.backfillDomains(context.Background()); err != nil {
		return nil, fmt.Errorf("backfill domains: %w", err)
	}
	return db, nil
}

// migrations add columns to tables created by earlier versions. Each statement
// must be idempotent or fail only with "duplicate column".
var migrations = []string{
	`ALTER TABLE applications ADD COLUMN github_app_id INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE applications ADD COLUMN github_installation_id INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE applications ADD COLUMN git_full_name TEXT NOT NULL DEFAULT ''`,
	`CREATE INDEX IF NOT EXISTS idx_applications_repo ON applications(github_app_id, git_full_name)`,
	// security: 2FA, SSO, session metadata
	`ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE users ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE users ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE users ADD COLUMN recovery_codes TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE users ADD COLUMN auth_source TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE users ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE users ADD COLUMN last_login_at TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE sessions ADD COLUMN created_at TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE sessions ADD COLUMN last_seen_at TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE sessions ADD COLUMN ip TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE sessions ADD COLUMN user_agent TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE sessions ADD COLUMN method TEXT NOT NULL DEFAULT 'password'`,
	`CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id)`,
	// registries & multi-server deployments
	`ALTER TABLE applications ADD COLUMN registry_id INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE applications ADD COLUMN registry_image TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE applications ADD COLUMN build_server_id INTEGER NOT NULL DEFAULT 0`,
	// access control, approvals, audit
	`ALTER TABLE projects ADD COLUMN default_role TEXT NOT NULL DEFAULT 'developer'`,
	`ALTER TABLE environments ADD COLUMN protected INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE environments ADD COLUMN require_approval INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE deployments ADD COLUMN requested_by INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE deployments ADD COLUMN reviewed_by INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE deployments ADD COLUMN reviewed_at TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE deployments ADD COLUMN review_comment TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE activity ADD COLUMN ip TEXT NOT NULL DEFAULT ''`,
	`CREATE INDEX IF NOT EXISTS idx_activity_created ON activity(created_at)`,
	// certificates
	`ALTER TABLE app_domains ADD COLUMN cert_mode TEXT NOT NULL DEFAULT 'auto'`,
	`ALTER TABLE app_domains ADD COLUMN cert_status TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE app_domains ADD COLUMN cert_issuer TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE app_domains ADD COLUMN cert_expires_at TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE app_domains ADD COLUMN cert_checked_at TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE app_domains ADD COLUMN cert_error TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE users ADD COLUMN locale TEXT NOT NULL DEFAULT ''`,
}

func Now() string { return time.Now().UTC().Format(time.RFC3339) }

// ---- reflection mapper ----

type fieldInfo struct {
	col   string
	index []int
}

var metaCache sync.Map // reflect.Type -> []fieldInfo

func fields(t reflect.Type) []fieldInfo {
	if v, ok := metaCache.Load(t); ok {
		return v.([]fieldInfo)
	}
	out := collectFields(t, nil)
	metaCache.Store(t, out)
	return out
}

// collectFields walks tagged fields, descending into untagged embedded structs.
// Outer fields shadow embedded ones with the same column name.
func collectFields(t reflect.Type, prefix []int) []fieldInfo {
	var own, embedded []fieldInfo
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		idx := append(append([]int{}, prefix...), i)
		tag := f.Tag.Get("db")
		if tag == "" && f.Anonymous && f.Type.Kind() == reflect.Struct {
			embedded = append(embedded, collectFields(f.Type, idx)...)
			continue
		}
		if tag == "" || tag == "-" {
			continue
		}
		own = append(own, fieldInfo{col: tag, index: idx})
	}
	seen := map[string]bool{}
	for _, f := range own {
		seen[f.col] = true
	}
	for _, f := range embedded {
		if !seen[f.col] {
			own = append(own, f)
		}
	}
	return own
}

func scanRows[T any](rows *sql.Rows) ([]T, error) {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var zero T
	fi := fields(reflect.TypeOf(zero))
	byCol := make(map[string][]int, len(fi))
	for _, f := range fi {
		byCol[f.col] = f.index
	}
	out := []T{}
	for rows.Next() {
		var item T
		v := reflect.ValueOf(&item).Elem()
		dest := make([]any, len(cols))
		for i, c := range cols {
			if idx, ok := byCol[c]; ok {
				dest[i] = v.FieldByIndex(idx).Addr().Interface()
			} else {
				dest[i] = new(any)
			}
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Select runs a query and maps all rows into T.
func Select[T any](ctx context.Context, db *DB, query string, args ...any) ([]T, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return scanRows[T](rows)
}

// Get runs a query and returns the first row or ErrNotFound.
func Get[T any](ctx context.Context, db *DB, query string, args ...any) (T, error) {
	items, err := Select[T](ctx, db, query, args...)
	var zero T
	if err != nil {
		return zero, err
	}
	if len(items) == 0 {
		return zero, ErrNotFound
	}
	return items[0], nil
}

// ByID loads a row by primary key from table.
func ByID[T any](ctx context.Context, db *DB, table string, id int64) (T, error) {
	return Get[T](ctx, db, "SELECT * FROM "+table+" WHERE id = ?", id)
}

// Insert writes all tagged fields except "id" and returns the new id.
func Insert(ctx context.Context, db *DB, table string, item any) (int64, error) {
	v := reflect.Indirect(reflect.ValueOf(item))
	var cols, marks []string
	var args []any
	for _, f := range fields(v.Type()) {
		if f.col == "id" {
			continue
		}
		cols = append(cols, quoteIdent(f.col))
		marks = append(marks, "?")
		args = append(args, v.FieldByIndex(f.index).Interface())
	}
	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ","), strings.Join(marks, ","))
	res, err := db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err == nil {
		if idf := v.FieldByName("ID"); idf.IsValid() && idf.CanSet() {
			idf.SetInt(id)
		}
	}
	return id, err
}

// Update writes all tagged fields except id/uuid/created_at for the row with item.ID.
func Update(ctx context.Context, db *DB, table string, item any) error {
	v := reflect.Indirect(reflect.ValueOf(item))
	var sets []string
	var args []any
	var id any
	for _, f := range fields(v.Type()) {
		val := v.FieldByIndex(f.index).Interface()
		switch f.col {
		case "id":
			id = val
			continue
		case "uuid", "created_at":
			continue
		}
		sets = append(sets, quoteIdent(f.col)+" = ?")
		args = append(args, val)
	}
	args = append(args, id)
	_, err := db.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET %s WHERE id = ?", table, strings.Join(sets, ",")), args...)
	return err
}

func Delete(ctx context.Context, db *DB, table string, id int64) error {
	_, err := db.ExecContext(ctx, "DELETE FROM "+table+" WHERE id = ?", id)
	return err
}

func quoteIdent(s string) string { return `"` + s + `"` }

// ---- settings ----

func (db *DB) Setting(ctx context.Context, key string) string {
	var v string
	_ = db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&v)
	return v
}

func (db *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := db.ExecContext(ctx, "INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
	return err
}

type clientIPKey struct{}

// WithClientIP records the caller's address for audit entries.
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, clientIPKey{}, ip)
}

// OnActivity, when set, receives every audit entry after it is stored
// (used to stream the audit log to a SIEM). It must not block.
var OnActivity func(Activity)

func (db *DB) LogActivity(ctx context.Context, userID int64, action, target string) {
	ip, _ := ctx.Value(clientIPKey{}).(string)
	a := Activity{UserID: userID, Action: action, Target: target, IP: ip, CreatedAt: Now()}
	res, err := db.ExecContext(ctx, "INSERT INTO activity(user_id, action, target, ip, created_at) VALUES(?,?,?,?,?)", userID, action, target, ip, a.CreatedAt)
	if err != nil {
		return
	}
	a.ID, _ = res.LastInsertId()
	if OnActivity != nil {
		OnActivity(a)
	}
}
