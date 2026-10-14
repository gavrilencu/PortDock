package composefile

import (
	"strings"
	"testing"
)

const root = "/data/portdock/builds/abc/src"

func policy(host bool) Policy {
	return Policy{HostAccess: host, Root: root, Project: "sy-abc", Network: "portdock", Forbidden: []string{"/data/portdock"}}
}

const goodRendered = `name: sy-abc
services:
  web:
    build:
      context: /data/portdock/builds/abc/src/web
      dockerfile: Dockerfile
    env_file:
      - path: /data/portdock/builds/abc/src/.env
    labels:
      traefik.http.middlewares.auth.basicauth.users: "a:b"
      com.example.team: shop
    networks:
      default: null
      portdock: null
    pid: service:worker
    volumes:
      - type: bind
        source: /data/portdock/builds/abc/src/static
        target: /static
      - type: volume
        source: data
        target: /data
      - type: tmpfs
        target: /tmp
    volumes_from:
      - worker
  worker:
    image: alpine
    network_mode: service:web
networks:
  default:
    name: sy-abc_default
  portdock:
    name: portdock
    external: true
volumes:
  data:
    name: sy-abc_data
secrets:
  token:
    name: sy-abc_token
    file: /data/portdock/builds/abc/src/token.txt
`

func TestCheckResolvedAllowsProjectResources(t *testing.T) {
	paths, err := CheckResolved(goodRendered, policy(false))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{root + "/.env", root + "/static", root + "/token.txt", root + "/web", root + "/web/Dockerfile"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("paths: %v", paths)
	}
}

func TestCheckResolvedRefusesHostAccess(t *testing.T) {
	svc := func(body string) string {
		return "name: sy-abc\nservices:\n  web:\n    image: alpine\n" + body
	}
	cases := map[string]string{
		"privileged":         svc("    privileged: true\n"),
		"network host":       svc("    network_mode: host\n"),
		"network container":  svc("    network_mode: container:portdock-dashboard\n"),
		"pid host":           svc("    pid: host\n"),
		"ipc container":      svc("    ipc: container:other\n"),
		"cap_add":            svc("    cap_add: [SYS_ADMIN]\n"),
		"devices":            svc("    devices: [/dev/sda]\n"),
		"seccomp":            svc("    security_opt: [seccomp=unconfined]\n"),
		"volumes_from":       svc("    volumes_from: [container:portdock-dashboard]\n"),
		"bind root":          svc("    volumes:\n      - type: bind\n        source: /\n        target: /host\n"),
		"bind outside":       svc("    volumes:\n      - type: bind\n        source: /srv\n        target: /srv\n"),
		"bind portdock":      svc("    volumes:\n      - type: bind\n        source: /data/portdock\n        target: /p\n"),
		"build context":      svc("    build:\n      context: /data/portdock\n      dockerfile: Dockerfile\n"),
		"dockerfile escape":  svc("    build:\n      context: " + root + "\n      dockerfile: ../../../secret.key\n"),
		"build ssh":          svc("    build:\n      context: " + root + "\n      ssh: [default]\n"),
		"build network":      svc("    build:\n      context: " + root + "\n      network: host\n"),
		"env_file outside":   svc("    env_file:\n      - path: /data/portdock/secret.key\n"),
		"traefik label":      svc("    labels:\n      traefik.http.routers.x.rule: Host(`shop.example.com`)\n"),
		"traefik label list": svc("    labels:\n      - traefik.enable=true\n"),
		"provider":           svc("    provider:\n      type: some-plugin\n"),
		"runtime":            svc("    runtime: sysbox\n"),
		"privileged hook":    svc("    post_start:\n      - command: [id]\n        privileged: true\n"),
		"external volume":    "name: sy-abc\nservices:\n  web:\n    image: alpine\nvolumes:\n  db:\n    name: sy-0123456789abcdef\n    external: true\n",
		"foreign volume":     "name: sy-abc\nservices:\n  web:\n    image: alpine\nvolumes:\n  db:\n    name: sy-0123456789abcdef\n",
		"driver_opts":        "name: sy-abc\nservices:\n  web:\n    image: alpine\nvolumes:\n  data:\n    name: sy-abc_data\n    driver_opts: {type: none, o: bind, device: /}\n",
		"foreign network":    "name: sy-abc\nservices:\n  web:\n    image: alpine\nnetworks:\n  db:\n    name: sy-other_default\n    external: true\n",
		"host network":       "name: sy-abc\nservices:\n  web:\n    image: alpine\nnetworks:\n  h:\n    name: host\n    external: true\n",
		"secret outside":     "name: sy-abc\nservices:\n  web:\n    image: alpine\nsecrets:\n  k:\n    name: sy-abc_k\n    file: /root/.ssh/id_ed25519\n",
	}
	for name, rendered := range cases {
		if _, err := CheckResolved(rendered, policy(false)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Host access allows most of it, never the Docker socket or PortDock's data.
	for _, name := range []string{"privileged", "network host", "cap_add", "bind outside", "external volume", "driver_opts", "traefik label"} {
		if _, err := CheckResolved(cases[name], policy(true)); err != nil {
			t.Errorf("%s with host access: %v", name, err)
		}
	}
	for _, name := range []string{"bind root", "bind portdock", "env_file outside"} {
		if _, err := CheckResolved(cases[name], policy(true)); err == nil {
			t.Errorf("%s with host access: accepted", name)
		}
	}
	if _, err := CheckResolved(svc("    use_api_socket: true\n"), policy(true)); err == nil {
		t.Error("use_api_socket accepted with host access")
	}
	sock := svc("    volumes:\n      - type: bind\n        source: /var/run/docker.sock\n        target: /var/run/docker.sock\n")
	if _, err := CheckResolved(sock, policy(true)); err == nil {
		t.Error("docker socket accepted with host access")
	}
}

// A container can swap anything under its writable bind mounts for a symbolic
// link before Docker mounts or reads it.
func TestCheckResolvedNestedWritableBinds(t *testing.T) {
	bind := func(src string, ro bool) string {
		s := "      - type: bind\n        source: " + root + src + "\n        target: /m" + src + "\n"
		if ro {
			s += "        read_only: true\n"
		}
		return s
	}
	two := func(a, b string) string {
		return "name: sy-abc\nservices:\n  web:\n    image: alpine\n    volumes:\n" + a + "  worker:\n    image: alpine\n    volumes:\n" + b
	}
	refused := map[string]string{
		"bind inside bind":     two(bind("/data", false), bind("/data/x", false)),
		"read-only inside rw":  two(bind("/data", false), bind("/data/x", true)),
		"env_file inside root": "name: sy-abc\nservices:\n  web:\n    image: alpine\n    env_file:\n      - path: " + root + "/.env\n    volumes:\n" + bind("", false),
		"secret inside bind":   "name: sy-abc\nservices:\n  web:\n    image: alpine\n    volumes:\n" + bind("/conf", false) + "secrets:\n  k:\n    name: sy-abc_k\n    file: " + root + "/conf/key\n",
	}
	for name, r := range refused {
		if _, err := CheckResolved(r, policy(false)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	allowed := map[string]string{
		"shared directory":  two(bind("/data", false), bind("/data", false)),
		"inside read-only":  two(bind("/data", true), bind("/data/x", false)),
		"siblings":          two(bind("/a", false), bind("/b", false)),
		"env_file, root ro": "name: sy-abc\nservices:\n  web:\n    image: alpine\n    env_file:\n      - path: " + root + "/.env\n    volumes:\n" + bind("", true),
	}
	for name, r := range allowed {
		if _, err := CheckResolved(r, policy(false)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := CheckResolved(refused["bind inside bind"], policy(true)); err != nil {
		t.Errorf("host access: %v", err)
	}
}

func TestCheckResolvedMoreHostAccess(t *testing.T) {
	svc := func(body string) string { return "name: sy-abc\nservices:\n  web:\n    image: alpine\n" + body }
	for name, r := range map[string]string{
		"uts host":     svc("    uts: host\n"),
		"seccomp file": svc("    security_opt: [seccomp=/srv/allow-all.json]\n"),
		"local cache":  svc("    build:\n      context: " + root + "\n      cache_to: [type=local,dest=/etc/cron.d]\n"),
		"macvlan":      "name: sy-abc\nservices:\n  web:\n    image: alpine\nnetworks:\n  lan:\n    name: sy-abc_lan\n    driver: macvlan\n",
		"ipvlan":       "name: sy-abc\nservices:\n  web:\n    image: alpine\nnetworks:\n  lan:\n    name: sy-abc_lan\n    driver: ipvlan\n",
	} {
		if _, err := CheckResolved(r, policy(false)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for name, r := range map[string]string{
		"default seccomp":  svc("    security_opt: [seccomp=default, no-new-privileges:true]\n"),
		"registry cache":   svc("    build:\n      context: " + root + "\n      cache_from: [type=registry,ref=ghcr.io/acme/web:cache]\n"),
		"bridge with opts": "name: sy-abc\nservices:\n  web:\n    image: alpine\nnetworks:\n  back:\n    name: sy-abc_back\n    driver: bridge\n",
	} {
		if _, err := CheckResolved(r, policy(false)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCheckResolvedWindowsPaths(t *testing.T) {
	r := `name: sy-abc
services:
  web:
    image: alpine
    volumes:
      - type: bind
        source: C:\pd\builds\abc\src\data
        target: /data
`
	p := policy(false)
	p.Root = "C:/pd/builds/abc/src"
	paths, err := CheckResolved(r, p)
	if err != nil || len(paths) != 1 || paths[0] != "C:/pd/builds/abc/src/data" {
		t.Fatalf("%v %v", paths, err)
	}
}
