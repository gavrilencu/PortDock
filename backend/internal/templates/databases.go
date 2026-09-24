// Package templates defines one-click database engines and service templates.
package templates

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/portdock/portdock/internal/executor"
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

	// Restore is false when a backup can be downloaded but not restored from PortDock.
	Restore bool `json:"restore"`

	env       func(user, pass, db string) map[string]string
	command   func(pass string) string
	health    func(user, pass, db string) string
	url       func(user, pass, host string, port int, db string) string
	backupCmd func(user, pass, db string) string // runs inside the container, writes the backup to stdout
	// restore reads a backup from stdin; file is the backup's name, so older
	// formats of the same engine keep restoring.
	restore func(user, pass, db, file string) string
	// init runs inside the container once it is healthy (e.g. creating the database).
	init func(user, pass, db string) string
}

func (e Engine) Env(user, pass, db string) map[string]string { return e.env(user, pass, db) }
func (e Engine) Command(pass string) string {
	if e.command == nil {
		return ""
	}
	return e.command(pass)
}
func (e Engine) HealthCmd(user, pass, db string) string { return e.health(user, pass, db) }
func (e Engine) URL(user, pass, host string, port int, db string) string {
	return e.url(user, pass, host, port, db)
}
func (e Engine) BackupCmd(user, pass, db string) string {
	if e.backupCmd == nil {
		return ""
	}
	return e.backupCmd(user, pass, db)
}
func (e Engine) RestoreCmd(user, pass, db, file string) string {
	if e.restore == nil {
		return ""
	}
	return e.restore(user, pass, db, file)
}

// InitCmd is run once the container is healthy ("" when the engine needs nothing).
func (e Engine) InitCmd(user, pass, db string) string {
	if e.init == nil {
		return ""
	}
	return e.init(user, pass, db)
}

// Backups are written by the commands below following each engine's own
// documentation: consistent online dumps (single transaction, background
// saves, copy-only backups) that don't block or disturb the database.

// The helpers below define shell functions (r, C, S) rather than variables, so
// quoted arguments such as passwords survive intact.

// redisBackup triggers a background save (BGSAVE doesn't block clients, unlike
// SAVE), waits for it to finish successfully and prints the RDB file.
func redisBackup(cli, pass string) string {
	return `r() { ` + cli + ` -a ` + q(pass) + ` --no-auth-warning "$@"; }; ` +
		// LASTSAVE has a one-second resolution: start the save in a later second
		// than the previous one so its completion is visible.
		`before=$(r LASTSAVE) || exit 1; while [ "$before" -ge "$(date +%s)" ]; do sleep 1; done; r BGSAVE >/dev/null 2>&1; i=0; ` +
		`while [ "$(r LASTSAVE)" = "$before" ]; do i=$((i+1)); ` +
		`if [ $i -gt 900 ]; then echo "background save did not finish" >&2; exit 1; fi; ` +
		`if [ $i -gt 5 ] && ! r INFO persistence | grep -q "rdb_bgsave_in_progress:1"; then echo "background save failed to start" >&2; exit 1; fi; ` +
		`sleep 1; done; ` +
		`r INFO persistence | grep -q "rdb_last_bgsave_status:ok" || { echo "background save failed" >&2; exit 1; }; ` +
		`dir=$(r CONFIG GET dir | sed -n 2p); f=$(r CONFIG GET dbfilename | sed -n 2p); cat "$dir/$f"`
}

// clickhouseClient defines C, clickhouse-client connected to the database.
func clickhouseClient(u, p, d string) string {
	return `C() { clickhouse-client --user ` + q(u) + ` --password ` + q(p) + ` --database ` + q(d) + ` "$@"; }; `
}

// sqlcmd defines S, the SQL Server command-line tool (mssql-tools18 on current
// images, which needs -C to trust the self-signed certificate).
func sqlcmd(pass string) string {
	return `S() { if [ -x /opt/mssql-tools18/bin/sqlcmd ]; then /opt/mssql-tools18/bin/sqlcmd -C -S localhost -U sa -P ` + q(pass) + ` -b "$@"; ` +
		`else /opt/mssql-tools/bin/sqlcmd -S localhost -U sa -P ` + q(pass) + ` -b "$@"; fi; }; `
}

func userinfo(user, pass string) string {
	return url.UserPassword(user, pass).String()
}

var Engines = map[string]Engine{
	"postgres": {
		ID: "postgres", Name: "PostgreSQL", DefaultImage: "postgres:18-alpine", Port: 5432,
		DataPath: "/var/lib/postgresql", HasUser: true, HasDatabase: true, BackupExt: "dump",
		env: func(u, p, d string) map[string]string {
			return map[string]string{"POSTGRES_USER": u, "POSTGRES_PASSWORD": p, "POSTGRES_DB": d}
		},
		health: func(u, p, d string) string { return "pg_isready -U " + q(u) + " -d " + q(d) },
		url: func(u, p, h string, port int, d string) string {
			return fmt.Sprintf("postgres://%s@%s:%d/%s", userinfo(u, p), h, port, d)
		},
		Restore: true,
		// Custom format: compressed, and pg_restore can replace the existing objects.
		backupCmd: func(u, p, d string) string {
			return fmt.Sprintf("pg_dump -U %s -d %s --format=custom --no-owner --no-privileges", q(u), q(d))
		},
		restore: func(u, p, d, file string) string {
			if strings.HasSuffix(file, ".sql.gz") { // plain dumps made by earlier versions
				return fmt.Sprintf("(echo 'DROP SCHEMA public CASCADE; CREATE SCHEMA public;'; gunzip) | psql -U %s -d %s -v ON_ERROR_STOP=1 --single-transaction -q", q(u), q(d))
			}
			// --single-transaction: either the whole backup is restored or nothing changes.
			return fmt.Sprintf("pg_restore -U %s -d %s --clean --if-exists --no-owner --no-privileges --single-transaction", q(u), q(d))
		},
	},
	"mysql": {
		ID: "mysql", Name: "MySQL", DefaultImage: "mysql:8.4", Port: 3306,
		DataPath: "/var/lib/mysql", HasUser: true, HasDatabase: true, BackupExt: "sql.gz",
		env: func(u, p, d string) map[string]string {
			return map[string]string{"MYSQL_ROOT_PASSWORD": p, "MYSQL_USER": u, "MYSQL_PASSWORD": p, "MYSQL_DATABASE": d}
		},
		health: func(u, p, d string) string { return "mysqladmin ping -h 127.0.0.1 -uroot -p" + q(p) },
		url: func(u, p, h string, port int, d string) string {
			return fmt.Sprintf("mysql://%s@%s:%d/%s", userinfo(u, p), h, port, d)
		},
		Restore: true,
		// --single-transaction gives a consistent InnoDB snapshot without locking tables.
		backupCmd: func(u, p, d string) string {
			return fmt.Sprintf("mysqldump --single-transaction --quick --routines --events --triggers --hex-blob -uroot -p%s %s | gzip", q(p), q(d))
		},
		restore: func(u, p, d, file string) string {
			return fmt.Sprintf("gunzip | mysql -uroot -p%s %s", q(p), q(d))
		},
	},
	"mariadb": {
		ID: "mariadb", Name: "MariaDB", DefaultImage: "mariadb:11", Port: 3306,
		DataPath: "/var/lib/mysql", HasUser: true, HasDatabase: true, BackupExt: "sql.gz",
		env: func(u, p, d string) map[string]string {
			return map[string]string{"MARIADB_ROOT_PASSWORD": p, "MARIADB_USER": u, "MARIADB_PASSWORD": p, "MARIADB_DATABASE": d}
		},
		health: func(u, p, d string) string { return "healthcheck.sh --connect --innodb_initialized" },
		url: func(u, p, h string, port int, d string) string {
			return fmt.Sprintf("mysql://%s@%s:%d/%s", userinfo(u, p), h, port, d)
		},
		Restore: true,
		backupCmd: func(u, p, d string) string {
			return fmt.Sprintf("mariadb-dump --single-transaction --quick --routines --events --triggers --hex-blob -uroot -p%s %s | gzip", q(p), q(d))
		},
		restore: func(u, p, d, file string) string {
			return fmt.Sprintf("gunzip | mariadb -uroot -p%s %s", q(p), q(d))
		},
	},
	"mongodb": {
		ID: "mongodb", Name: "MongoDB", DefaultImage: "mongo:8", Port: 27017,
		DataPath: "/data/db", HasUser: true, HasDatabase: true, BackupExt: "archive.gz",
		env: func(u, p, d string) map[string]string {
			return map[string]string{"MONGO_INITDB_ROOT_USERNAME": u, "MONGO_INITDB_ROOT_PASSWORD": p, "MONGO_INITDB_DATABASE": d}
		},
		// Authenticated, so the database only counts as healthy once the
		// entrypoint has created the root user (it first starts without auth).
		health: func(u, p, d string) string {
			return "mongosh --quiet -u " + q(u) + " -p " + q(p) + " --authenticationDatabase admin --eval 'db.runCommand({ping:1})'"
		},
		url: func(u, p, h string, port int, d string) string {
			return fmt.Sprintf("mongodb://%s@%s:%d/%s?authSource=admin", userinfo(u, p), h, port, d)
		},
		Restore: true,
		backupCmd: func(u, p, d string) string {
			return fmt.Sprintf("mongodump --quiet --username %s --password %s --authenticationDatabase admin --archive --gzip", q(u), q(p))
		},
		restore: func(u, p, d, file string) string {
			return fmt.Sprintf("mongorestore --quiet --username %s --password %s --authenticationDatabase admin --archive --gzip --drop", q(u), q(p))
		},
	},
	"redis": {
		ID: "redis", Name: "Redis", DefaultImage: "redis:8-alpine", Port: 6379,
		DataPath: "/data", BackupExt: "rdb",
		env:     func(u, p, d string) map[string]string { return map[string]string{"REDIS_PASSWORD": p} },
		command: func(p string) string { return "redis-server --requirepass " + q(p) + " --appendonly yes" },
		health:  func(u, p, d string) string { return "redis-cli -a " + q(p) + " --no-auth-warning ping" },
		url: func(u, p, h string, port int, d string) string {
			return fmt.Sprintf("redis://default:%s@%s:%d/0", url.QueryEscape(p), h, port)
		},
		backupCmd: func(u, p, d string) string { return redisBackup("redis-cli", p) },
	},
	"valkey": {
		ID: "valkey", Name: "Valkey", DefaultImage: "valkey/valkey:8-alpine", Port: 6379,
		DataPath: "/data", BackupExt: "rdb",
		env:     func(u, p, d string) map[string]string { return map[string]string{"VALKEY_PASSWORD": p} },
		command: func(p string) string { return "valkey-server --requirepass " + q(p) + " --appendonly yes" },
		health:  func(u, p, d string) string { return "valkey-cli -a " + q(p) + " --no-auth-warning ping" },
		url: func(u, p, h string, port int, d string) string {
			return fmt.Sprintf("redis://default:%s@%s:%d/0", url.QueryEscape(p), h, port)
		},
		backupCmd: func(u, p, d string) string { return redisBackup("valkey-cli", p) },
	},
	"clickhouse": {
		ID: "clickhouse", Name: "ClickHouse", DefaultImage: "clickhouse/clickhouse-server:25.8", Port: 8123,
		DataPath: "/var/lib/clickhouse", HasUser: true, HasDatabase: true, BackupExt: "tar.gz", Restore: true,
		env: func(u, p, d string) map[string]string {
			return map[string]string{"CLICKHOUSE_USER": u, "CLICKHOUSE_PASSWORD": p, "CLICKHOUSE_DB": d, "CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT": "1"}
		},
		health: func(u, p, d string) string { return "wget -qO- http://127.0.0.1:8123/ping" },
		url: func(u, p, h string, port int, d string) string {
			return fmt.Sprintf("http://%s@%s:%d/?database=%s", userinfo(u, p), h, port, d)
		},
		// The schema (SHOW CREATE) and the data (Native format) of every table,
		// then the views, packed in a tar archive. The hidden inner tables of
		// materialized views are left out: the views recreate them.
		backupCmd: func(u, p, d string) string {
			return `set -e; ` + clickhouseClient(u, p, d) + `t=$(mktemp -d); ` +
				`C -q "SELECT name FROM system.tables WHERE database = currentDatabase() AND NOT is_temporary AND engine NOT LIKE '%View' AND name NOT LIKE '.inner%' ORDER BY name" > "$t/tables.txt"; ` +
				`C -q "SELECT name FROM system.tables WHERE database = currentDatabase() AND engine LIKE '%View' ORDER BY name" > "$t/views.txt"; ` +
				`while IFS= read -r n; do [ -n "$n" ] || continue; ` +
				"C -q \"SHOW CREATE TABLE \\`$n\\`\" --format TSVRaw > \"$t/$n.sql\"; " +
				"C -q \"SELECT * FROM \\`$n\\` FORMAT Native\" > \"$t/$n.native\"; done < \"$t/tables.txt\"; " +
				"while IFS= read -r n; do [ -n \"$n\" ] || continue; C -q \"SHOW CREATE TABLE \\`$n\\`\" --format TSVRaw > \"$t/$n.sql\"; done < \"$t/views.txt\"; " +
				`tar -C "$t" -czf - . ; rm -rf "$t"`
		},
		restore: func(u, p, d, file string) string {
			return `set -e; ` + clickhouseClient(u, p, d) + `t=$(mktemp -d); tar -xzf - -C "$t"; ` +
				"while IFS= read -r n; do [ -n \"$n\" ] || continue; C -q \"DROP TABLE IF EXISTS \\`$n\\`\"; done < \"$t/views.txt\"; " +
				"while IFS= read -r n; do [ -n \"$n\" ] || continue; C -q \"DROP TABLE IF EXISTS \\`$n\\`\"; " +
				"C --multiquery < \"$t/$n.sql\"; C -q \"INSERT INTO \\`$n\\` FORMAT Native\" < \"$t/$n.native\"; done < \"$t/tables.txt\"; " +
				`while IFS= read -r n; do [ -n "$n" ] || continue; C --multiquery < "$t/$n.sql"; done < "$t/views.txt"; ` +
				`rm -rf "$t"`
		},
	},
	"mssql": {
		ID: "mssql", Name: "Microsoft SQL Server", DefaultImage: "mcr.microsoft.com/mssql/server:2022-latest", Port: 1433,
		DataPath: "/var/opt/mssql", HasDatabase: true, BackupExt: "bak.gz", Restore: true,
		// Express is free for production use; set MSSQL_PID in the image's
		// environment for another edition you are licensed for.
		env: func(u, p, d string) map[string]string {
			return map[string]string{"ACCEPT_EULA": "Y", "MSSQL_SA_PASSWORD": p, "MSSQL_PID": "Express"}
		},
		health: func(u, p, d string) string { return sqlcmd(p) + `S -Q "SELECT 1" >/dev/null` },
		url: func(u, p, h string, port int, d string) string {
			return fmt.Sprintf("sqlserver://%s@%s:%d?database=%s", userinfo("sa", p), h, port, url.QueryEscape(d))
		},
		init: func(u, p, d string) string {
			return sqlcmd(p) + `S -Q "IF DB_ID(N'` + d + `') IS NULL CREATE DATABASE [` + d + `]"`
		},
		// A copy-only full backup (it doesn't break an existing backup chain),
		// with checksums, verified with RESTORE VERIFYONLY before it is handed over.
		backupCmd: func(u, p, d string) string {
			return `set -e; ` + sqlcmd(p) + `mkdir -p /var/opt/mssql/backup; f=/var/opt/mssql/backup/portdock-$$.bak; ` +
				`S -Q "BACKUP DATABASE [` + d + `] TO DISK = N'$f' WITH COPY_ONLY, INIT, CHECKSUM" >&2; ` +
				`S -Q "RESTORE VERIFYONLY FROM DISK = N'$f' WITH CHECKSUM" >&2; ` +
				`gzip -c "$f"; rm -f "$f"`
		},
		// Other sessions are disconnected (SINGLE_USER) for the restore and let
		// back in afterwards, also when the restore fails.
		restore: func(u, p, d, file string) string {
			return `set -e; ` + sqlcmd(p) + `mkdir -p /var/opt/mssql/backup; f=/var/opt/mssql/backup/portdock-restore.bak; gunzip -c > "$f"; ` +
				`S -Q "IF DB_ID(N'` + d + `') IS NOT NULL ALTER DATABASE [` + d + `] SET SINGLE_USER WITH ROLLBACK IMMEDIATE; RESTORE DATABASE [` + d + `] FROM DISK = N'$f' WITH REPLACE, CHECKSUM; ALTER DATABASE [` + d + `] SET MULTI_USER" >&2 ` +
				`|| { S -Q "IF DB_ID(N'` + d + `') IS NOT NULL ALTER DATABASE [` + d + `] SET MULTI_USER" >&2; rm -f "$f"; exit 1; }; rm -f "$f"`
		},
	},
}

func EngineList() []Engine {
	order := []string{"postgres", "mysql", "mariadb", "mssql", "mongodb", "redis", "valkey", "clickhouse"}
	out := make([]Engine, 0, len(order))
	for _, id := range order {
		out = append(out, Engines[id])
	}
	return out
}
