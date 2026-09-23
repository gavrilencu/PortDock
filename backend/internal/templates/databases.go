// Package templates defines one-click database engines and service templates.
package templates

import (
	"fmt"
	"net/url"

	"github.com/shipyard/shipyard/internal/executor"
)

var q = executor.Quote

type Engine struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DefaultImage string `json:"default_image"`
	Port         int    `json:"port"`
	DataPath     string `json:"-"`
	HasUser      bool   `json:"has_user"`
	HasDatabase  bool   `json:"has_database"`
	BackupExt    string `json:"backup_ext"`

	env       func(user, pass, db string) map[string]string
	command   func(pass string) string
	health    func(user, pass string) string
	url       func(user, pass, host string, port int, db string) string
	backupCmd func(user, pass, db string) string // runs inside the container, writes to stdout
	restore   func(user, pass, db string) string // reads backup from stdin
}

func (e Engine) Env(user, pass, db string) map[string]string { return e.env(user, pass, db) }
func (e Engine) Command(pass string) string {
	if e.command == nil {
		return ""
	}
	return e.command(pass)
}
func (e Engine) HealthCmd(user, pass string) string { return e.health(user, pass) }
func (e Engine) URL(user, pass, host string, port int, db string) string {
	return e.url(user, pass, host, port, db)
}
func (e Engine) BackupCmd(user, pass, db string) string {
	if e.backupCmd == nil {
		return ""
	}
	return e.backupCmd(user, pass, db)
}
func (e Engine) RestoreCmd(user, pass, db string) string {
	if e.restore == nil {
		return ""
	}
	return e.restore(user, pass, db)
}

func userinfo(user, pass string) string {
	return url.UserPassword(user, pass).String()
}

var Engines = map[string]Engine{
	"postgres": {
		ID: "postgres", Name: "PostgreSQL", DefaultImage: "postgres:18-alpine", Port: 5432,
		DataPath: "/var/lib/postgresql", HasUser: true, HasDatabase: true, BackupExt: "sql.gz",
		env: func(u, p, d string) map[string]string {
			return map[string]string{"POSTGRES_USER": u, "POSTGRES_PASSWORD": p, "POSTGRES_DB": d}
		},
		health: func(u, p string) string { return "pg_isready -U " + q(u) },
		url: func(u, p, h string, port int, d string) string {
			return fmt.Sprintf("postgres://%s@%s:%d/%s", userinfo(u, p), h, port, d)
		},
		backupCmd: func(u, p, d string) string { return fmt.Sprintf("pg_dump -U %s -d %s | gzip", q(u), q(d)) },
		restore:   func(u, p, d string) string { return fmt.Sprintf("gunzip | psql -U %s -d %s", q(u), q(d)) },
	},
	"mysql": {
		ID: "mysql", Name: "MySQL", DefaultImage: "mysql:8.4", Port: 3306,
		DataPath: "/var/lib/mysql", HasUser: true, HasDatabase: true, BackupExt: "sql.gz",
		env: func(u, p, d string) map[string]string {
			return map[string]string{"MYSQL_ROOT_PASSWORD": p, "MYSQL_USER": u, "MYSQL_PASSWORD": p, "MYSQL_DATABASE": d}
		},
		health: func(u, p string) string { return "mysqladmin ping -h 127.0.0.1 -uroot -p" + q(p) },
		url: func(u, p, h string, port int, d string) string {
			return fmt.Sprintf("mysql://%s@%s:%d/%s", userinfo(u, p), h, port, d)
		},
		backupCmd: func(u, p, d string) string {
			return fmt.Sprintf("mysqldump --single-transaction -uroot -p%s %s | gzip", q(p), q(d))
		},
		restore: func(u, p, d string) string { return fmt.Sprintf("gunzip | mysql -uroot -p%s %s", q(p), q(d)) },
	},
	"mariadb": {
		ID: "mariadb", Name: "MariaDB", DefaultImage: "mariadb:11", Port: 3306,
		DataPath: "/var/lib/mysql", HasUser: true, HasDatabase: true, BackupExt: "sql.gz",
		env: func(u, p, d string) map[string]string {
			return map[string]string{"MARIADB_ROOT_PASSWORD": p, "MARIADB_USER": u, "MARIADB_PASSWORD": p, "MARIADB_DATABASE": d}
		},
		health: func(u, p string) string { return "healthcheck.sh --connect --innodb_initialized" },
		url: func(u, p, h string, port int, d string) string {
			return fmt.Sprintf("mysql://%s@%s:%d/%s", userinfo(u, p), h, port, d)
		},
		backupCmd: func(u, p, d string) string {
			return fmt.Sprintf("mariadb-dump --single-transaction -uroot -p%s %s | gzip", q(p), q(d))
		},
		restore: func(u, p, d string) string { return fmt.Sprintf("gunzip | mariadb -uroot -p%s %s", q(p), q(d)) },
	},
	"mongodb": {
		ID: "mongodb", Name: "MongoDB", DefaultImage: "mongo:8", Port: 27017,
		DataPath: "/data/db", HasUser: true, HasDatabase: true, BackupExt: "archive.gz",
		env: func(u, p, d string) map[string]string {
			return map[string]string{"MONGO_INITDB_ROOT_USERNAME": u, "MONGO_INITDB_ROOT_PASSWORD": p, "MONGO_INITDB_DATABASE": d}
		},
		health: func(u, p string) string { return "mongosh --quiet --eval 'db.runCommand({ping:1})'" },
		url: func(u, p, h string, port int, d string) string {
			return fmt.Sprintf("mongodb://%s@%s:%d/%s?authSource=admin", userinfo(u, p), h, port, d)
		},
		backupCmd: func(u, p, d string) string {
			return fmt.Sprintf("mongodump --username %s --password %s --authenticationDatabase admin --archive --gzip", q(u), q(p))
		},
		restore: func(u, p, d string) string {
			return fmt.Sprintf("mongorestore --username %s --password %s --authenticationDatabase admin --archive --gzip --drop", q(u), q(p))
		},
	},
	"redis": {
		ID: "redis", Name: "Redis", DefaultImage: "redis:8-alpine", Port: 6379,
		DataPath: "/data", BackupExt: "rdb",
		env:     func(u, p, d string) map[string]string { return map[string]string{"REDIS_PASSWORD": p} },
		command: func(p string) string { return "redis-server --requirepass " + q(p) + " --appendonly yes" },
		health:  func(u, p string) string { return "redis-cli -a " + q(p) + " --no-auth-warning ping" },
		url: func(u, p, h string, port int, d string) string {
			return fmt.Sprintf("redis://default:%s@%s:%d/0", url.QueryEscape(p), h, port)
		},
		backupCmd: func(u, p, d string) string {
			return "redis-cli -a " + q(p) + " --no-auth-warning SAVE >/dev/null && cat /data/dump.rdb"
		},
	},
	"valkey": {
		ID: "valkey", Name: "Valkey", DefaultImage: "valkey/valkey:8-alpine", Port: 6379,
		DataPath: "/data", BackupExt: "rdb",
		env:     func(u, p, d string) map[string]string { return map[string]string{"VALKEY_PASSWORD": p} },
		command: func(p string) string { return "valkey-server --requirepass " + q(p) + " --appendonly yes" },
		health:  func(u, p string) string { return "valkey-cli -a " + q(p) + " --no-auth-warning ping" },
		url: func(u, p, h string, port int, d string) string {
			return fmt.Sprintf("redis://default:%s@%s:%d/0", url.QueryEscape(p), h, port)
		},
		backupCmd: func(u, p, d string) string {
			return "valkey-cli -a " + q(p) + " --no-auth-warning SAVE >/dev/null && cat /data/dump.rdb"
		},
	},
	"clickhouse": {
		ID: "clickhouse", Name: "ClickHouse", DefaultImage: "clickhouse/clickhouse-server:25.8", Port: 8123,
		DataPath: "/var/lib/clickhouse", HasUser: true, HasDatabase: true, BackupExt: "",
		env: func(u, p, d string) map[string]string {
			return map[string]string{"CLICKHOUSE_USER": u, "CLICKHOUSE_PASSWORD": p, "CLICKHOUSE_DB": d, "CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT": "1"}
		},
		health: func(u, p string) string { return "wget -qO- http://127.0.0.1:8123/ping" },
		url: func(u, p, h string, port int, d string) string {
			return fmt.Sprintf("http://%s@%s:%d/?database=%s", userinfo(u, p), h, port, d)
		},
	},
}

func EngineList() []Engine {
	order := []string{"postgres", "mysql", "mariadb", "mongodb", "redis", "valkey", "clickhouse"}
	out := make([]Engine, 0, len(order))
	for _, id := range order {
		out = append(out, Engines[id])
	}
	return out
}
