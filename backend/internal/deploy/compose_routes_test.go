package deploy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/proxy"
	"go.yaml.in/yaml/v3"
)

const monorepo = `
services:
  frontend:
    build: ./frontend
    ports: ["8080:80"]
  backend:
    build: ./backend
    expose: ["4000"]
  db:
    image: postgres:17
`

func TestResolveComposeRoutes(t *testing.T) {
	main, port, services, err := composeMain(monorepo, "frontend", 0)
	if err != nil || main != "frontend" || port != 80 {
		t.Fatalf("main=%s port=%d err=%v", main, port, err)
	}
	routes := []proxy.DomainRoute{
		{Host: "app.example.com"},                                   // main service, its declared port
		{Host: "api.example.com", Service: "backend"},               // backend, its declared port
		{Host: "admin.example.com", Service: "backend", Port: 4100}, // explicit port wins
		{Host: "db.example.com", Service: "db"},                     // no port: skipped
	}
	var log bytes.Buffer
	got, err := resolveComposeRoutes(routes, main, port, services, printer{&log})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"app.example.com": "frontend:80", "api.example.com": "backend:4000", "admin.example.com": "backend:4100"}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for _, r := range got {
		if w := want[r.Host]; w != r.Service+":"+strconv.Itoa(r.Port) {
			t.Errorf("%s → %s:%d, want %s", r.Host, r.Service, r.Port, w)
		}
	}
	if !strings.Contains(log.String(), "db.example.com is not routed") {
		t.Errorf("missing warning for the port-less service: %q", log.String())
	}
	if _, err := resolveComposeRoutes([]proxy.DomainRoute{{Host: "x.example.com", Service: "nope"}}, main, port, services, nil); err == nil || !strings.Contains(err.Error(), "backend, db, frontend") {
		t.Errorf("unknown service must fail listing the services, got %v", err)
	}
}

func TestComposeRoutesRenderPerService(t *testing.T) {
	out := string(proxy.RenderDynamic(proxy.AppRoutes{
		Name:     "app1",
		Services: map[string][]string{"frontend": {"sy-app1-frontend-1"}, "backend": {"sy-app1-backend-1", "sy-app1-backend-2"}},
		Domains: []proxy.DomainRoute{
			{Host: "app.example.com", Service: "frontend", Port: 80, HTTPS: true, Indexing: true},
			{Host: "api.example.com", Service: "backend", Port: 4000, HTTPS: true, Indexing: true},
			{Host: "gone.example.com", Service: "worker", Port: 9000, HTTPS: true}, // nothing running: no router
		},
	}))
	for _, s := range []string{"http://sy-app1-frontend-1:80", "http://sy-app1-backend-1:4000", "http://sy-app1-backend-2:4000", "app1-frontend-p80", "app1-backend-p4000", "Host(`api.example.com`)"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q in:\n%s", s, out)
		}
	}
	if strings.Contains(out, "gone.example.com") {
		t.Error("a domain without running containers must not get a router")
	}
	if proxy.RenderDynamic(proxy.AppRoutes{Name: "a", Domains: []proxy.DomainRoute{{Host: "x.example.com", Service: "s", Port: 80}}}) != nil {
		t.Error("no upstreams at all must remove the routes file")
	}
}

// fakeX answers `cat <file>` from files and records written files and commands.
type fakeX struct {
	mu      sync.Mutex
	files   map[string]string
	written map[string]string
	cmds    []string
}

func (f *fakeX) Run(_ context.Context, cmd string, o executor.RunOpts) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, cmd)
	if strings.HasPrefix(cmd, "cat ") {
		name := strings.Trim(strings.TrimPrefix(cmd, "cat "), "'")
		if o.Stdout != nil {
			_, _ = io.WriteString(o.Stdout, f.files[name])
		}
		return nil
	}
	if strings.Contains(cmd, "&& cat > ") && o.Stdin != nil {
		name := strings.Trim(strings.Fields(strings.SplitN(cmd, "&& cat > ", 2)[1])[0], "'")
		b, _ := io.ReadAll(o.Stdin)
		f.written[name] = string(b)
	}
	return nil
}
func (f *fakeX) PTY(context.Context, string, int, int) (executor.Session, error) { return nil, nil }
func (f *fakeX) Close() error                                                    { return nil }

func TestComposeOverrideForFileRoutes(t *testing.T) {
	x := &fakeX{files: map[string]string{"/src/docker-compose.yml": monorepo}, written: map[string]string{}}
	var log bytes.Buffer
	res, err := RunComposeUp(context.Background(), ComposeUp{
		X: x, Project: "sy-app1", Dir: "/src", File: "/src/docker-compose.yml",
		Service: "frontend", Port: 80, FileRoutes: true, Routed: []string{"backend", "frontend"},
		Env:        map[string]string{"API_URL": "https://api.example.com", "PRICE": "5$off"},
		ServiceEnv: map[string]map[string]string{"backend": {"PORT": "4000", "DATABASE_URL": "postgres://db/app"}},
		Kind:       "application", Resource: "app1", Network: "portdock", Log: printer{&log},
	})
	if err != nil || res.Main != "frontend" || res.Port != 80 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	var override struct {
		Services map[string]struct {
			Labels      map[string]string `yaml:"labels"`
			Networks    []string          `yaml:"networks"`
			EnvFile     []string          `yaml:"env_file"`
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(x.written["/src/.portdock.override.yml"]), &override); err != nil {
		t.Fatal(err)
	}
	for name, svc := range override.Services {
		for k := range svc.Labels {
			if strings.HasPrefix(k, "traefik.") {
				t.Errorf("%s: file-routed services must not carry traefik labels (%s)", name, k)
			}
		}
		onProxy := len(svc.Networks) == 2 && svc.Networks[1] == "portdock"
		if routed := name != "db"; onProxy != routed {
			t.Errorf("%s: on proxy network = %v, want %v", name, onProxy, routed)
		}
	}
	fe, be, db := override.Services["frontend"], override.Services["backend"], override.Services["db"]
	if fe.Environment["API_URL"] != "https://api.example.com" || fe.Environment["PRICE"] != "5$$off" || fe.Environment["PORT"] != "" {
		t.Errorf("main service environment: %v", fe.Environment)
	}
	if be.Environment["PORT"] != "4000" || be.Environment["DATABASE_URL"] != "postgres://db/app" || be.Environment["API_URL"] != "" {
		t.Errorf("backend gets only its own variables: %v", be.Environment)
	}
	if len(db.Environment) != 0 || len(fe.EnvFile)+len(be.EnvFile) != 0 {
		t.Errorf("unexpected env for db %v / env files %v %v", db.Environment, fe.EnvFile, be.EnvFile)
	}
	if _, err := RunComposeUp(context.Background(), ComposeUp{X: x, Dir: "/src", File: "/src/docker-compose.yml", FileRoutes: true, Routed: []string{"nope"}, Log: printer{&log}}); err == nil {
		t.Error("a route to a missing service must fail before compose up")
	}
}

type printer struct{ w io.Writer }

func (p printer) Printf(format string, args ...any) {
	_, _ = io.WriteString(p.w, fmt.Sprintf(format, args...)+"\n")
}
func (p printer) Write(b []byte) (int, error) { return p.w.Write(b) }
