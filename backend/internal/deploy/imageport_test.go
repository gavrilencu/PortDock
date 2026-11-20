package deploy

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/store"
)

// fakeDocker is a Docker server whose registry describes one image.
type fakeDocker struct {
	config string // what docker buildx imagetools inspect prints
	cmds   []string
}

func (f *fakeDocker) Run(_ context.Context, cmd string, o executor.RunOpts) error {
	f.cmds = append(f.cmds, cmd)
	if strings.Contains(cmd, "imagetools inspect") && o.Stdout != nil {
		_, _ = io.WriteString(o.Stdout, f.config)
	}
	return nil
}

func (f *fakeDocker) PTY(context.Context, string, int, int) (executor.Session, error) {
	return nil, errors.New("no terminal")
}

func (f *fakeDocker) Close() error { return nil }

const nginxConfig = `{"linux/amd64":{"architecture":"amd64","config":{"ExposedPorts":{"80/tcp":{}},"Cmd":["nginx"]}},"linux/arm64":{"architecture":"arm64","config":{"ExposedPorts":{"80/tcp":{}}}}}`

// A new application takes the port its image declares; one that has run, or
// whose PORT variable is set, keeps its own and is only warned.
func TestAdoptImagePort(t *testing.T) {
	ctx := context.Background()
	f := newKubeFixture(t)
	adopt := func(a store.Application, runtime map[string]string, exposed []int) (store.Application, string, *fakeDocker) {
		t.Helper()
		x := &fakeDocker{config: nginxConfig}
		l := newLogger(f.e, 0)
		defer l.Close()
		if runtime == nil {
			runtime = map[string]string{}
		}
		j := &job{e: f.e, app: a, d: &store.Deployment{ApplicationID: a.ID}, log: l, env: Env{Runtime: runtime}, exposed: exposed}
		j.adoptImagePort(ctx, "nginx:1.27", x)
		saved, _ := store.ByID[store.Application](ctx, f.e.db, "applications", a.ID)
		if saved.Port != j.app.Port {
			t.Fatalf("the job runs port %d but %d is saved", j.app.Port, saved.Port)
		}
		return saved, l.String(), x
	}
	image := func(port int) store.Application {
		a := nginxApp()
		a.Port = port
		return f.app(a)
	}

	// A new prebuilt image on the wrong port: read from the registry, adopted.
	got, logs, x := adopt(image(3000), nil, nil)
	if got.Port != 80 || !strings.Contains(logs, "the application's port is now 80 instead of 3000") {
		t.Fatalf("new application: port %d\n%s", got.Port, logs)
	}
	if len(x.cmds) == 0 || !strings.Contains(x.cmds[len(x.cmds)-1], "docker buildx imagetools inspect --format '{{json .Image}}' nginx:1.27") {
		t.Fatalf("the image must be read from its registry: %v", x.cmds)
	}

	// The right port already: nothing to say.
	if got, logs, _ := adopt(image(80), nil, nil); got.Port != 80 || strings.Contains(logs, "EXPOSE") {
		t.Fatalf("matching port: %d\n%s", got.Port, logs)
	}

	// An application that has run keeps its port.
	ran := image(3000)
	if _, err := store.Insert(ctx, f.e.db, "deployments", &store.Deployment{UUID: "ran", ApplicationID: ran.ID, Status: "success", CreatedAt: store.Now()}); err != nil {
		t.Fatal(err)
	}
	if got, logs, _ := adopt(ran, nil, nil); got.Port != 3000 || !strings.Contains(logs, "⚠ The image declares port 80 (EXPOSE)") {
		t.Fatalf("an application that ran: port %d\n%s", got.Port, logs)
	}

	// The user's PORT variable wins.
	if got, logs, _ := adopt(image(3000), map[string]string{"PORT": "3000"}, nil); got.Port != 3000 || !strings.Contains(logs, "⚠") {
		t.Fatalf("PORT set by the user: port %d\n%s", got.Port, logs)
	}

	// An image built from Git: the ports read after the build are used.
	git := f.app(store.Application{Name: "site", SourceType: "git", BuildPack: "dockerfile", GitRepository: "https://git.corp/site.git", GitBranch: "main", Port: 3000})
	got, logs, x = adopt(git, nil, []int{80})
	if got.Port != 80 || len(x.cmds) != 0 {
		t.Fatalf("built image: port %d, registry calls %v\n%s", got.Port, x.cmds, logs)
	}

	// A rollback or a preview changes nothing.
	for _, d := range []store.Deployment{{RollbackOf: 1}, {PRNumber: 4}} {
		a := image(3000)
		l := newLogger(f.e, 0)
		j := &job{e: f.e, app: a, d: &d, log: l, env: Env{Runtime: map[string]string{}}}
		j.adoptImagePort(ctx, "nginx:1.27", &fakeDocker{config: nginxConfig})
		l.Close()
		if j.app.Port != 3000 {
			t.Fatalf("%+v changed the port to %d", d, j.app.Port)
		}
	}
}

func TestPickPort(t *testing.T) {
	for _, c := range []struct {
		ports []int
		want  int
	}{
		{[]int{5432}, 5432},
		{[]int{80, 443}, 80},
		{[]int{8080, 50000}, 8080},
		{[]int{6379, 16379}, 6379},
		{nil, 0},
	} {
		if got := pickPort(c.ports); got != c.want {
			t.Errorf("pickPort(%v) = %d, want %d", c.ports, got, c.want)
		}
	}
}

func TestConfigPorts(t *testing.T) {
	for in, want := range map[string]string{
		nginxConfig: "80",
		`{"architecture":"amd64","config":{"ExposedPorts":{"8080/tcp":{},"8443/tcp":{}}}}`: "8080, 8443",
		`{"linux/arm64":{"config":{"ExposedPorts":{"9000/tcp":{}}}}}`:                      "9000",
		`{"architecture":"amd64","config":{"Cmd":["sh"]}}`:                                 "",
		"not json": "",
		"":         "",
	} {
		if got := joinPorts(configPorts(in)); got != want {
			t.Errorf("configPorts(%.40q) = %q, want %q", in, got, want)
		}
	}
}
