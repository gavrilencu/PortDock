package deploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portdock/portdock/internal/executor"
)

// TestComposeConfigCheckE2E renders compose files with the real Docker Compose:
// PORTDOCK_DOCKER_E2E=1 go test ./internal/deploy -run ComposeConfigCheckE2E
func TestComposeConfigCheckE2E(t *testing.T) {
	if os.Getenv("PORTDOCK_DOCKER_E2E") != "1" {
		t.Skip("set PORTDOCK_DOCKER_E2E=1 to run the compose checks against Docker Compose")
	}
	os.Setenv("MSYS_NO_PATHCONV", "1")
	x := executor.Local{}
	write := func(dir, name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	check := func(t *testing.T, files map[string]string, env map[string]string, link string) error {
		dir := t.TempDir()
		for n, b := range files {
			write(dir, n, b)
		}
		if link != "" {
			if err := os.Symlink(link, filepath.Join(dir, "link")); err != nil {
				t.Skipf("symbolic links unavailable: %v", err)
			}
		}
		root := filepath.ToSlash(dir)
		envPath := root + "/.portdock.env"
		write(dir, ".portdock.env", envFile(env))
		c := ComposeUp{X: x, Project: "sy-e2echeck", Dir: root, File: root + "/docker-compose.yml", Network: "portdock", Root: root, Forbidden: []string{"/data/portdock"}}
		return checkComposeConfig(context.Background(), c, files["docker-compose.yml"], envPath)
	}

	ok := map[string]string{"docker-compose.yml": "services:\n  web:\n    image: nginx:${TAG:-alpine}\n    volumes: [./html:/usr/share/nginx/html, data:/data]\nvolumes:\n  data: {}\n"}
	if err := check(t, ok, nil, ""); err != nil {
		t.Fatalf("plain compose refused: %v", err)
	}
	bad := map[string]struct {
		files map[string]string
		env   map[string]string
	}{
		"interpolated network_mode": {map[string]string{"docker-compose.yml": "services:\n  web:\n    image: alpine\n    network_mode: ${NM}\n"}, map[string]string{"NM": "host"}},
		"interpolated bind source":  {map[string]string{"docker-compose.yml": "services:\n  web:\n    image: alpine\n    volumes: [\"${D}:/host\"]\n"}, map[string]string{"D": "/"}},
		"extends privileged": {map[string]string{
			"docker-compose.yml": "services:\n  web:\n    image: alpine\n    extends: {file: base.yml, service: base}\n",
			"base.yml":           "services:\n  base:\n    image: alpine\n    privileged: true\n",
		}, nil},
		"include host pid": {map[string]string{
			"docker-compose.yml": "include: [inc.yml]\nservices:\n  web:\n    image: alpine\n",
			"inc.yml":            "services:\n  other:\n    image: alpine\n    pid: host\n",
		}, nil},
		"driver_opts bind": {map[string]string{"docker-compose.yml": "services:\n  web:\n    image: alpine\n    volumes: [vol:/v]\nvolumes:\n  vol:\n    driver_opts: {type: none, o: bind, device: /}\n"}, nil},
		"foreign volume":   {map[string]string{"docker-compose.yml": "services:\n  web:\n    image: alpine\n    volumes: [db:/db]\nvolumes:\n  db:\n    external: true\n    name: sy-0123456789abcdef\n"}, nil},
		"traefik label":    {map[string]string{"docker-compose.yml": "services:\n  web:\n    image: alpine\n    labels: [\"traefik.http.routers.x.rule=Host(`a.example.com`)\"]\n"}, nil},
	}
	for name, c := range bad {
		if err := check(t, c.files, c.env, ""); err == nil {
			t.Errorf("%s: accepted", name)
		} else {
			t.Logf("%s: %v", name, err)
		}
	}
	// A bind mount through a symbolic link that leaves the project.
	t.Run("symlink", func(t *testing.T) {
		err := check(t, map[string]string{"docker-compose.yml": "services:\n  web:\n    image: alpine\n    volumes: [./link:/host]\n"}, nil, "/")
		if err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Errorf("symlink escape: %v", err)
		}
	})
}

// inContainer runs commands in a fresh Linux container where /p/src holds a
// link to / and a link inside the project.
type inContainer struct {
	image string
}

func (c inContainer) Run(ctx context.Context, cmd string, o executor.RunOpts) error {
	setup := "mkdir -p /p/src/dir /p/other && ln -s / /p/src/root && ln -s dir /p/src/inner && ln -s ../other /p/src/up && "
	return executor.Local{}.Run(ctx, "docker run --rm "+c.image+" sh -c "+executor.Quote(setup+cmd), o)
}
func (inContainer) PTY(context.Context, string, int, int) (executor.Session, error) { return nil, nil }
func (inContainer) Close() error                                                    { return nil }

// TestEnsureInsideE2E checks the symlink resolution with busybox (the PortDock
// image) and GNU coreutils (Debian/Ubuntu servers).
func TestEnsureInsideE2E(t *testing.T) {
	if os.Getenv("PORTDOCK_DOCKER_E2E") != "1" {
		t.Skip("set PORTDOCK_DOCKER_E2E=1 to run the symlink checks in containers")
	}
	os.Setenv("MSYS_NO_PATHCONV", "1")
	for _, img := range []string{"alpine:3", "debian:stable-slim"} {
		x := inContainer{img}
		ctx := context.Background()
		if err := ensureInside(ctx, x, "/p/src", "/p/src/dir", "/p/src/inner/x", "/p/src/new/deeper", "/p/src"); err != nil {
			t.Errorf("%s: paths inside refused: %v", img, err)
		}
		for _, bad := range []string{"/p/src/root", "/p/src/root/etc", "/p/src/up", "/p/src/up/missing/file"} {
			if err := ensureInside(ctx, x, "/p/src", "/p/src/dir", bad); err == nil || !strings.Contains(err.Error(), "symbolic link") {
				t.Errorf("%s: %s accepted (%v)", img, bad, err)
			}
		}
	}
}
