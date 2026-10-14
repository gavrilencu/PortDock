package api

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/portdock/portdock/internal/instancebackup"
)

func TestInstanceBackupAndRestore(t *testing.T) {
	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	e.user("admin@corp.com", "correct-horse", "admin")
	e.user("dev@corp.com", "correct-horse", "member")
	owner, admin, dev := e.login("owner@corp.com"), e.login("admin@corp.com"), e.login("dev@corp.com")
	e.expect(owner, "POST", "/api/projects", map[string]any{"name": "shop"}, 200)

	e.expect(dev, "GET", "/api/system/backup", nil, 403)
	e.expect(admin, "PUT", "/api/system/backup", map[string]any{"enabled": true, "schedule": "0 3 * * *", "retention": 3}, 400) // no passphrase yet
	e.expect(admin, "PUT", "/api/system/backup", map[string]any{"enabled": true, "retention": 3, "passphrase": "short"}, 400)
	e.expect(admin, "PUT", "/api/system/backup", map[string]any{"enabled": true, "schedule": "every night", "retention": 3, "passphrase": "a long backup passphrase"}, 400)
	e.expect(admin, "PUT", "/api/system/backup", map[string]any{"enabled": true, "retention": 3, "upload_s3": true, "passphrase": "a long backup passphrase"}, 400) // no S3
	st := e.expect(admin, "PUT", "/api/system/backup", map[string]any{"enabled": true, "schedule": "15 2 * * *", "retention": 2, "passphrase": "a long backup passphrase"}, 200)
	if st["passphrase_set"] != true || st["schedule"] != "15 2 * * *" || st["passphrase"] != nil {
		t.Fatalf("settings: %v", st)
	}

	// Retention keeps the two newest backups.
	var name string
	for i := 0; i < 3; i++ {
		st = e.expect(admin, "POST", "/api/system/backup/run", nil, 200)
		files := st["files"].([]any)
		name = files[0].(map[string]any)["name"].(string)
		if i < 2 {
			time.Sleep(1100 * time.Millisecond) // file names carry the second
		}
	}
	if n := len(st["files"].([]any)); n != 2 {
		t.Fatalf("retention: %d files", n)
	}
	if last := st["last"].(map[string]any); last["status"] != "success" || last["file"] != name {
		t.Fatalf("last run: %v", last)
	}

	resp, err := admin.Get(e.srv.URL + "/api/system/backup/files/" + name)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("download: %v %v", err, resp)
	}
	file, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if _, err := instancebackup.Extract(bytes.NewReader(file), "a long backup passphrase", t.TempDir()); err != nil {
		t.Fatalf("downloaded backup does not open: %v", err)
	}
	e.expect(admin, "GET", "/api/system/backup/files/..%2Fsecret.key", nil, 404)

	// Restoring on a running instance: the owner only.
	restarted := make(chan struct{}, 1)
	e.api.restart = func() { restarted <- struct{}{} }
	e.expect(admin, "POST", "/api/system/backup/files/"+name+"/restore", map[string]any{}, 403)
	e.expect(owner, "POST", "/api/system/backup/files/"+name+"/restore", map[string]any{"passphrase": "not the passphrase"}, 400)
	if r := e.expect(owner, "POST", "/api/system/backup/files/"+name+"/restore", map[string]any{}, 200); r["restarting"] != true {
		t.Fatalf("restore: %v", r)
	}
	select {
	case <-restarted:
	case <-time.After(5 * time.Second):
		t.Fatal("PortDock was not restarted")
	}
	if !instancebackup.HasPending(e.dir) {
		t.Fatal("nothing staged")
	}

	// The first-run page refuses once the instance has accounts.
	upload := func(env *testEnv, passphrase string) (int, string) {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		_ = mw.WriteField("passphrase", passphrase)
		fw, _ := mw.CreateFormFile("file", name)
		_, _ = fw.Write(file)
		mw.Close()
		resp, err := http.Post(env.srv.URL+"/api/setup/restore", mw.FormDataContentType(), &body)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, _ := upload(e, "a long backup passphrase"); code != 403 {
		t.Fatalf("setup restore on a set-up instance: %d", code)
	}

	// A new, empty instance restores it from the first-run page.
	fresh := newTestEnv(t)
	freshRestarted := make(chan struct{}, 1)
	fresh.api.restart = func() { freshRestarted <- struct{}{} }
	if code, body := upload(fresh, "wrong passphrase here"); code != 400 {
		t.Fatalf("wrong passphrase: %d %s", code, body)
	}
	if code, body := upload(fresh, "a long backup passphrase"); code != 200 {
		t.Fatalf("setup restore: %d %s", code, body)
	}
	select {
	case <-freshRestarted:
	case <-time.After(5 * time.Second):
		t.Fatal("PortDock was not restarted")
	}
	applied, err := instancebackup.ApplyPending(fresh.dir, filepath.Join(fresh.dir, "restored.db"))
	if err != nil || !applied {
		t.Fatalf("apply: %v %v", applied, err)
	}
}
