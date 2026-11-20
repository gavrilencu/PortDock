package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/portdock/portdock/internal/updater"
)

// In a pod with the rights to update itself, the dashboard offers the update
// and starts it as a Job; without them, it says how to grant them.
func TestKubeSelfUpdate(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.43.0.1")
	t.Setenv("POD_NAME", "portdock-0")
	nsFile := filepath.Join(t.TempDir(), "namespace")
	if err := os.WriteFile(nsFile, []byte("portdock\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var (
		mu      sync.Mutex
		allowed = true
		jobs    []map[string]any
	)
	fake := func(_ context.Context, stdin string, args ...string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		cmd := strings.Join(args, " ")
		switch {
		case strings.Contains(cmd, "get pod portdock-0"):
			return `{"metadata":{"ownerReferences":[{"kind":"StatefulSet","name":"portdock"}]},"spec":{"serviceAccountName":"portdock","containers":[{"name":"portdock","image":"ghcr.io/acme/portdock:v1.17.1"}]}}`, nil
		case strings.Contains(cmd, "auth can-i"):
			if allowed {
				return "yes", nil
			}
			return "no", errors.New("exit status 1")
		case strings.Contains(cmd, "create -f -"):
			var m map[string]any
			_ = json.Unmarshal([]byte(stdin), &m)
			jobs = append(jobs, m)
			return "job.batch/created", nil
		}
		return "", errors.New("unexpected kubectl " + cmd)
	}
	oldKubectl, oldNS, oldLook := kubectl, serviceAccountNamespace, lookPath
	kubectl, serviceAccountNamespace = fake, nsFile
	lookPath = func(string) (string, error) { return "/usr/bin/kubectl", nil }
	defer func() { kubectl, serviceAccountNamespace, lookPath = oldKubectl, oldNS, oldLook }()

	e := newTestEnv(t)
	e.user("owner@corp.com", "correct-horse", "owner")
	c := e.client()
	if code, _ := e.do(c, "POST", "/api/auth/login", map[string]string{"email": "owner@corp.com", "password": "correct-horse"}); code != 200 {
		t.Fatalf("login: %d", code)
	}
	if _, st := e.do(c, "GET", "/api/system/update", nil); st["supported"] != true {
		t.Fatalf("with the rights, the update is offered: %v", st["unsupported_reason"])
	}

	// The update starts as a Job (signature check off: no Sigstore in tests).
	_ = e.db.SetSetting(t.Context(), "update_verify", updater.VerifyOff)
	if code, res := e.do(c, "POST", "/api/system/update", map[string]any{"version": "v99.0.0"}); code != 200 {
		t.Fatalf("run: %d %v", code, res)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		n := len(jobs)
		mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no update job was created; state: %+v", updater.LoadState(e.dir))
		}
		time.Sleep(50 * time.Millisecond)
	}
	st := updater.LoadState(e.dir)
	if st.Status != "running" || st.Job == "" || st.Backup == "" || st.To != "v99.0.0" {
		t.Fatalf("state: %+v", st)
	}

	// Without the rights (a manifest from before), the reason says what to do.
	mu.Lock()
	allowed = false
	mu.Unlock()
	e.api.updates.mu.Lock()
	e.api.updates.kubeCheckedAt = time.Time{}
	e.api.updates.mu.Unlock()
	_, res := e.do(c, "GET", "/api/system/update", nil)
	if res["supported"] != false || !strings.Contains(res["unsupported_reason"].(string), "installer again") {
		t.Fatalf("without the rights: %v", res)
	}
}
