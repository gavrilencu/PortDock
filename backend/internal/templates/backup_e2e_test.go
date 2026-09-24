package templates

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestBackupRestoreE2E runs every engine in Docker, writes data, backs it up,
// changes the data, restores the backup and checks the data is back.
// It pulls several images, so it only runs with PORTDOCK_DB_E2E=1
// (PORTDOCK_DB_E2E_ENGINES=postgres,mssql limits it to some engines).
func TestBackupRestoreE2E(t *testing.T) {
	if os.Getenv("PORTDOCK_DB_E2E") != "1" {
		t.Skip("set PORTDOCK_DB_E2E=1 to run the database backup tests against Docker")
	}
	only := map[string]bool{}
	for _, e := range strings.Split(os.Getenv("PORTDOCK_DB_E2E_ENGINES"), ",") {
		if e = strings.TrimSpace(e); e != "" {
			only[e] = true
		}
	}
	const user, pass, db = "portdock", "Pd3xamplePassw0rd", "app"
	users := map[string]string{"postgres": "postgres", "mongodb": "root", "clickhouse": "default"}

	type check struct {
		write, change, read string // shell commands run inside the container
		want                string // expected output of read after the restore
		magic               string // for engines without restore: the backup must start with this
	}
	checks := map[string]check{
		"postgres": {
			write:  `psql -U postgres -d app -v ON_ERROR_STOP=1 -c "CREATE TABLE t(id int primary key, v text); INSERT INTO t VALUES (1, 'hello');"`,
			change: `psql -U postgres -d app -v ON_ERROR_STOP=1 -c "UPDATE t SET v = 'changed'; INSERT INTO t VALUES (2, 'new');"`,
			read:   `psql -U postgres -d app -tAc "SELECT string_agg(v, ',' ORDER BY id) FROM t"`,
			want:   "hello",
		},
		"mysql": {
			write:  `mysql -uroot -p` + pass + ` app -e "CREATE TABLE t(id int primary key, v text); INSERT INTO t VALUES (1, 'hello');"`,
			change: `mysql -uroot -p` + pass + ` app -e "UPDATE t SET v = 'changed'; INSERT INTO t VALUES (2, 'new');"`,
			read:   `mysql -uroot -p` + pass + ` app -N -e "SELECT GROUP_CONCAT(v ORDER BY id) FROM t"`,
			want:   "hello",
		},
		"mariadb": {
			write:  `mariadb -uroot -p` + pass + ` app -e "CREATE TABLE t(id int primary key, v text); INSERT INTO t VALUES (1, 'hello');"`,
			change: `mariadb -uroot -p` + pass + ` app -e "UPDATE t SET v = 'changed'; INSERT INTO t VALUES (2, 'new');"`,
			read:   `mariadb -uroot -p` + pass + ` app -N -e "SELECT GROUP_CONCAT(v ORDER BY id) FROM t"`,
			want:   "hello",
		},
		"mongodb": {
			write:  `mongosh --quiet -u root -p ` + pass + ` --authenticationDatabase admin app --eval 'db.t.insertOne({_id: 1, v: "hello"})'`,
			change: `mongosh --quiet -u root -p ` + pass + ` --authenticationDatabase admin app --eval 'db.t.updateOne({_id: 1}, {$set: {v: "changed"}}); db.t.insertOne({_id: 2, v: "new"})'`,
			read:   `mongosh --quiet -u root -p ` + pass + ` --authenticationDatabase admin app --eval 'print(db.t.find().sort({_id: 1}).toArray().map(d => d.v).join(","))'`,
			want:   "hello",
		},
		"redis":  {write: `redis-cli -a ` + pass + ` --no-auth-warning SET k hello`, magic: "REDIS"},
		"valkey": {write: `valkey-cli -a ` + pass + ` --no-auth-warning SET k hello`, magic: "REDIS"},
		"clickhouse": {
			write: `clickhouse-client --password ` + pass + ` --database app --multiquery -q "CREATE TABLE t (id UInt32, v String) ENGINE = MergeTree ORDER BY id; ` +
				`INSERT INTO t VALUES (1, 'hello'); CREATE VIEW vt AS SELECT v FROM t;"`,
			change: `clickhouse-client --password ` + pass + ` --database app --multiquery -q "TRUNCATE TABLE t; INSERT INTO t VALUES (2, 'new');"`,
			read:   `clickhouse-client --password ` + pass + ` --database app -q "SELECT arrayStringConcat(groupArray(v), ',') FROM (SELECT v FROM vt ORDER BY v)"`,
			want:   "hello",
		},
		"mssql": {
			write:  sqlcmd(pass) + `S -d app -Q "CREATE TABLE t(id int primary key, v nvarchar(50)); INSERT INTO t VALUES (1, 'hello');"`,
			change: sqlcmd(pass) + `S -d app -Q "UPDATE t SET v = 'changed'; INSERT INTO t VALUES (2, 'new');"`,
			read:   sqlcmd(pass) + `S -d app -h -1 -W -Q "SET NOCOUNT ON; SELECT STRING_AGG(v, ',') WITHIN GROUP (ORDER BY id) FROM t"`,
			want:   "hello",
		},
	}

	for _, eng := range EngineList() {
		c, ok := checks[eng.ID]
		if !ok {
			t.Errorf("%s: no backup test", eng.ID)
			continue
		}
		if len(only) > 0 && !only[eng.ID] {
			continue
		}
		t.Run(eng.ID, func(t *testing.T) {
			u := user
			if v, ok := users[eng.ID]; ok {
				u = v
			}
			name := "portdock-e2e-" + eng.ID
			_ = exec.Command("docker", "rm", "-f", name).Run()
			args := []string{"run", "-d", "--name", name}
			for k, v := range eng.Env(u, pass, db) {
				args = append(args, "-e", k+"="+v)
			}
			args = append(args, eng.DefaultImage)
			if cmd := eng.Command(pass); cmd != "" {
				args = append(args, strings.Fields(cmd)...)
			}
			if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
				t.Fatalf("start: %v\n%s", err, out)
			}
			t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })

			sh := func(script string, stdin []byte) ([]byte, error) {
				cmd := exec.Command("docker", "exec", "-i", name, "sh", "-c", script)
				if stdin != nil {
					cmd.Stdin = bytes.NewReader(stdin)
				}
				var out, errb bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &errb
				err := cmd.Run()
				if err != nil {
					return out.Bytes(), fmt.Errorf("%v: %s", err, errb.String())
				}
				return out.Bytes(), nil
			}
			deadline := time.Now().Add(4 * time.Minute)
			for {
				if _, err := sh(eng.HealthCmd(u, pass, db), nil); err == nil {
					break
				}
				if time.Now().After(deadline) {
					logs, _ := exec.Command("docker", "logs", "--tail", "30", name).CombinedOutput()
					t.Fatalf("not healthy in time:\n%s", logs)
				}
				time.Sleep(2 * time.Second)
			}
			if init := eng.InitCmd(u, pass, db); init != "" {
				if _, err := sh(init, nil); err != nil {
					t.Fatalf("init: %v", err)
				}
			}
			if _, err := sh(c.write, nil); err != nil {
				t.Fatalf("write: %v", err)
			}

			const pipefail = "(set -o pipefail) 2>/dev/null && set -o pipefail; "
			backup, err := sh(pipefail+eng.BackupCmd(u, pass, db), nil)
			if err != nil || len(backup) == 0 {
				t.Fatalf("backup (%d bytes): %v", len(backup), err)
			}
			t.Logf("backup: %d bytes (.%s)", len(backup), eng.BackupExt)
			if c.magic != "" {
				if !bytes.HasPrefix(backup, []byte(c.magic)) {
					t.Fatalf("backup doesn't start with %q: %q", c.magic, backup[:min(16, len(backup))])
				}
				if eng.Restore {
					t.Fatal("engine claims restore support but has no restore check")
				}
				return
			}
			if !eng.Restore {
				t.Fatal("engine has a data check but no restore")
			}
			if _, err := sh(c.change, nil); err != nil {
				t.Fatalf("change: %v", err)
			}
			if _, err := sh(eng.RestoreCmd(u, pass, db, "backup."+eng.BackupExt), backup); err != nil {
				t.Fatalf("restore: %v", err)
			}
			out, err := sh(c.read, nil)
			if got := strings.TrimSpace(string(out)); err != nil || got != c.want {
				t.Fatalf("after restore: got %q, want %q (%v)", got, c.want, err)
			}
			// A second restore of the same backup must work too (objects already exist).
			if _, err := sh(eng.RestoreCmd(u, pass, db, "backup."+eng.BackupExt), backup); err != nil {
				t.Fatalf("second restore: %v", err)
			}
		})
	}
}
