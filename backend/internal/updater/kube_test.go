package updater

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fakeKubectl answers kubectl calls from a table and records them.
type fakeKubectl struct {
	pod     string          // JSON of the pod
	can     map[string]bool // "verb resource" → allowed
	logs    string
	created []map[string]any
	calls   []string
}

func (f *fakeKubectl) run(_ context.Context, stdin string, args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	if len(args) > 2 && args[0] == "-n" {
		args = args[2:]
	}
	switch {
	case args[0] == "get" && args[1] == "pod":
		return f.pod, nil
	case args[0] == "auth":
		if f.can[args[2]+" "+args[3]] {
			return "yes", nil
		}
		return "no", errors.New("exit status 1")
	case args[0] == "create":
		var m map[string]any
		if err := json.Unmarshal([]byte(stdin), &m); err != nil {
			return "", err
		}
		f.created = append(f.created, m)
		return "job.batch/created", nil
	case args[0] == "logs":
		return f.logs, nil
	}
	return "", errors.New("unexpected kubectl " + strings.Join(args, " "))
}

const testPod = `{"metadata":{"name":"portdock-0","ownerReferences":[{"kind":"StatefulSet","name":"portdock"}]},
 "spec":{"serviceAccountName":"portdock","containers":[{"name":"portdock","image":"ghcr.io/acme/portdock:v1.17.1"}]}}`

func allRights() map[string]bool {
	m := map[string]bool{}
	for _, r := range kubeUpdateRights {
		m[r[0]+" "+r[1]] = true
	}
	return m
}

func TestReadKubeSelf(t *testing.T) {
	f := &fakeKubectl{pod: testPod}
	self, err := ReadKubeSelf(context.Background(), f.run, "portdock", "portdock-0")
	if err != nil {
		t.Fatal(err)
	}
	want := KubeSelf{Namespace: "portdock", Pod: "portdock-0", StatefulSet: "portdock", Container: "portdock", Image: "ghcr.io/acme/portdock:v1.17.1", ServiceAccount: "portdock"}
	if self != want {
		t.Fatalf("self = %+v", self)
	}
	f.pod = `{"metadata":{"ownerReferences":[{"kind":"ReplicaSet","name":"portdock-7d9"}]},"spec":{"containers":[{"name":"portdock","image":"x"}]}}`
	if _, err := ReadKubeSelf(context.Background(), f.run, "portdock", "portdock-7d9-abc"); err == nil || !strings.Contains(err.Error(), "StatefulSet") {
		t.Fatalf("a pod of a Deployment: %v", err)
	}
}

func TestCanSelfUpdate(t *testing.T) {
	f := &fakeKubectl{can: allRights()}
	if err := CanSelfUpdate(context.Background(), f.run, "portdock"); err != nil {
		t.Fatal(err)
	}
	delete(f.can, "create jobs.batch")
	err := CanSelfUpdate(context.Background(), f.run, "portdock")
	if err == nil || !strings.Contains(err.Error(), "create jobs.batch") || strings.Contains(err.Error(), "patch") {
		t.Fatalf("missing right: %v", err)
	}
}

// The Job runs the current image, moves the StatefulSet to the verified
// image, and goes back (deleting the broken pod) when it does not start.
func TestLaunchKube(t *testing.T) {
	dir := t.TempDir()
	f := &fakeKubectl{pod: testPod, can: allRights()}
	self, _ := ReadKubeSelf(context.Background(), f.run, "portdock", "portdock-0")
	backedUp := false
	p := Plan{DataDir: dir, From: "v1.17.1", To: "v1.18.0", Image: "ghcr.io/acme/portdock:v1.18.0",
		Backup: func(context.Context) (string, error) { backedUp = true; return dir + "/backup.db", nil },
		Verify: func(_ context.Context, image string) (string, string, error) {
			return image + "@sha256:" + strings.Repeat("a", 64), "✓ Signature verified", nil
		}}
	st, err := LaunchKube(context.Background(), f.run, self, p)
	if err != nil {
		t.Fatal(err)
	}
	if !backedUp || st.Status != "running" || st.Job == "" || st.Backup == "" || LoadState(dir).Job != st.Job {
		t.Fatalf("state: %+v", st)
	}
	if len(f.created) != 1 {
		t.Fatalf("jobs created: %d", len(f.created))
	}
	job := f.created[0]
	tmpl := job["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	c := tmpl["containers"].([]any)[0].(map[string]any)
	env := map[string]string{}
	for _, e := range c["env"].([]any) {
		e := e.(map[string]any)
		env[e["name"].(string)] = e["value"].(string)
	}
	script := c["command"].([]any)[2].(string)
	if c["image"] != "ghcr.io/acme/portdock:v1.17.1" || tmpl["serviceAccountName"] != "portdock" || tmpl["restartPolicy"] != "Never" {
		t.Fatalf("the job must run the current image with PortDock's service account: %v", tmpl)
	}
	if env["IMAGE"] != p.Image+"@sha256:"+strings.Repeat("a", 64) || env["STS"] != "portdock" || env["CONTAINER"] != "portdock" || env["POD"] != "portdock-0" || env["NS"] != "portdock" {
		t.Fatalf("env: %v", env)
	}
	for _, want := range []string{`set image "statefulset/$STS" "$CONTAINER=$IMAGE"`, "rollout status", "rollout undo", `delete pod "$POD"`} {
		if !strings.Contains(script, want) {
			t.Errorf("the script should run %q:\n%s", want, script)
		}
	}

	// A signature that does not verify changes nothing.
	f.created = nil
	p.Verify = func(context.Context, string) (string, string, error) { return "", "", errors.New("no signature") }
	if _, err := LaunchKube(context.Background(), f.run, self, p); err == nil || len(f.created) != 0 || LoadState(dir).Status != "failed" {
		t.Fatalf("unverified image: %v, %d jobs", err, len(f.created))
	}
}

func TestFinishKube(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f := &fakeKubectl{logs: "Updating statefulset/portdock\nThe new version did not become ready within 5 minutes: going back to the previous one\n"}
	running := func() {
		saveState(dir, State{ID: "upd-1", Status: "running", From: "v1.17.1", To: "v1.18.0", Job: "portdock-upd-1"})
	}

	running()
	if st, changed := FinishKube(ctx, f.run, dir, "v1.18.0", "portdock"); !changed || st.Status != "success" {
		t.Fatalf("the new version runs: %+v", st)
	}
	if _, changed := FinishKube(ctx, f.run, dir, "v1.18.0", "portdock"); changed {
		t.Fatal("a finished update is recorded once")
	}

	running()
	st, changed := FinishKube(ctx, f.run, dir, "v1.17.1", "portdock")
	if !changed || st.Status != "rolled_back" || !strings.Contains(st.Error, "v1.18.0 did not become ready") {
		t.Fatalf("the previous version is back: %+v", st)
	}
	joined := ""
	for _, s := range st.Steps {
		joined += s.Message + "\n"
	}
	if !strings.Contains(joined, "going back to the previous one") {
		t.Fatalf("the job's log should be kept: %s", joined)
	}

	// A Docker update in progress is not this function's business.
	saveState(dir, State{ID: "upd-2", Status: "running", To: "v1.18.0"})
	if _, changed := FinishKube(ctx, f.run, dir, "v1.18.0", "portdock"); changed {
		t.Fatal("a state without a job is left alone")
	}
}
