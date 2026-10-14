package deploy

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/store"
	"github.com/portdock/portdock/internal/stream"
)

func TestCheckHostPath(t *testing.T) {
	e := testEngine(t)
	e.cfg.DataDir = "/data/portdock"
	for _, bad := range []string{"/", "/etc", "/var/run/docker.sock", "/data/portdock", "/data/portdock/apps", "/data"} {
		if e.CheckHostPath(bad) == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
	for _, good := range []string{"/srv/media", "/data/media", "/mnt/portdock-backup"} {
		if err := e.CheckHostPath(good); err != nil {
			t.Errorf("%s: %v", good, err)
		}
	}
}

// TestVolumesE2E mounts application volumes in real containers:
// PORTDOCK_DOCKER_E2E=1 go test ./internal/deploy -run VolumesE2E
func TestVolumesE2E(t *testing.T) {
	if os.Getenv("PORTDOCK_DOCKER_E2E") != "1" {
		t.Skip("set PORTDOCK_DOCKER_E2E=1 to run the volume tests against Docker")
	}
	os.Setenv("MSYS_NO_PATHCONV", "1")
	e := testEngine(t)
	e.hub = stream.NewHub()
	e.pool = executor.NewPool(e.db, e.box)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	x := executor.Local{}
	const uuid = "pdvoltest"
	for _, q := range []string{
		"INSERT INTO projects(id, name, created_at) VALUES (1, 'p', '')",
		"INSERT INTO environments(id, project_id, name, created_at) VALUES (1, 1, 'production', '')",
		"INSERT INTO servers(id, name, host, status, is_local) VALUES (1, 'local', 'localhost', 'reachable', 1)",
		"INSERT INTO applications(id, uuid, environment_id, server_id, name) VALUES (1, '" + uuid + "', 1, 1, 'shop')",
		"INSERT INTO deployments(id, uuid, application_id, status, created_at) VALUES (1, 'd1', 1, 'running', '')",
		"INSERT INTO app_volumes(application_id, name, mount_path, read_only) VALUES (1, 'data', '/data', 0), (1, 'conf', '/conf', 1)",
	} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	cleanup := func() { _ = removeAppVolumes(context.Background(), x, uuid) }
	cleanup()
	t.Cleanup(cleanup)

	app := store.Application{ID: 1, UUID: uuid, Name: "shop", ServerID: 1}
	mounts := func(pr int) string {
		j := &job{e: e, app: app, x: x, d: &store.Deployment{ID: 1, PRNumber: pr}, log: newLogger(e, 1)}
		m, err := j.volumeArgs(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	run := func(m, script string) (string, error) {
		return executor.Output(ctx, x, "docker run --rm"+m+" alpine:3 sh -c "+executor.Quote(script))
	}

	m := mounts(0)
	if !strings.Contains(m, "pd-"+uuid+"_data:/data") || !strings.Contains(m, "pd-"+uuid+"_conf:/conf:ro") {
		t.Fatalf("mounts: %s", m)
	}
	if _, err := run(m, "echo kept > /data/file"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(m, "touch /conf/x"); err == nil {
		t.Fatal("a read-only volume accepted a write")
	}
	// A later deployment (new container) sees the data; creating again is a no-op.
	if out, err := run(mounts(0), "cat /data/file"); err != nil || out != "kept" {
		t.Fatalf("data after redeploy: %q %v", out, err)
	}
	// Previews get their own, empty volumes.
	pm := mounts(3)
	if !strings.Contains(pm, "pd-"+uuid+"_data_pr3:/data") {
		t.Fatalf("preview mounts: %s", pm)
	}
	if out, _ := run(pm, "cat /data/file 2>/dev/null || echo empty"); out != "empty" {
		t.Fatalf("preview sees production data: %q", out)
	}
	labels, _ := exec.Command("docker", "volume", "inspect", "-f", "{{json .Labels}}", "pd-"+uuid+"_data_pr3").Output()
	if !strings.Contains(string(labels), `"portdock.pr":"3"`) || !strings.Contains(string(labels), `"portdock.volume":"data"`) {
		t.Fatalf("labels: %s", labels)
	}

	// A volume made beforehand under the same name is not taken over.
	_ = exec.Command("docker", "volume", "create", "pd-"+uuid+"_conf_pr4").Run()
	j4 := &job{e: e, app: app, x: x, d: &store.Deployment{ID: 1, PRNumber: 4}, log: newLogger(e, 1)}
	if _, err := j4.volumeArgs(ctx); err == nil || !strings.Contains(err.Error(), "create volumes") {
		t.Fatalf("foreign volume reused: %v", err)
	}
	_ = exec.Command("docker", "volume", "rm", "-f", "pd-"+uuid+"_conf_pr4", "pd-"+uuid+"_data_pr4").Run()

	// Closing the preview removes only its volumes.
	if err := removeAppVolumes(ctx, x, uuid, "portdock.pr=3"); err != nil {
		t.Fatal(err)
	}
	list := func() string {
		out, _ := executor.Output(ctx, x, volumesCmd(uuid))
		return out
	}
	if l := list(); strings.Contains(l, "_pr3") || !strings.Contains(l, "_data") {
		t.Fatalf("after preview cleanup: %q", l)
	}
	if u := e.VolumeUsage(ctx, app); len(u) != 1 || u[0].Error != "" || strings.Join(u[0].Volumes, ",") != "conf,data" {
		t.Fatalf("usage: %+v", u)
	}

	// Data of a volume in use is kept and reported; once unused it is deleted.
	if _, err := executor.Output(ctx, x, "docker run -d --name pdvoltest-c"+m+" alpine:3 sleep 300"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", "pdvoltest-c").Run() })
	if err := e.RemoveVolumeData(ctx, app, "data"); err == nil || !strings.Contains(err.Error(), "still used") {
		t.Fatalf("in use: %v", err)
	}
	_ = exec.Command("docker", "rm", "-f", "pdvoltest-c").Run()
	if err := e.RemoveVolumeData(ctx, app, "data"); err != nil {
		t.Fatal(err)
	}
	if l := list(); strings.Contains(l, "_data") || !strings.Contains(l, "_conf") {
		t.Fatalf("after removing data: %q", l)
	}
	if err := removeAppVolumes(ctx, x, uuid); err != nil {
		t.Fatal(err)
	}
	if l := list(); l != "" {
		t.Fatalf("volumes left: %q", l)
	}
}
