package composefile

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	ok := []string{
		"services:\n  web:\n    image: nginx\n    ports: ['80']\n",
		"services:\n  web:\n    image: nginx\n    volumes:\n      - data:/data\n      - ./config:/etc/app:ro\n      - cache\nvolumes:\n  data: {}\n",
		"services:\n  web:\n    image: nginx\n    volumes:\n      - type: volume\n        source: data\n        target: /data\n      - type: tmpfs\n        target: /tmp\n",
		"services:\n  web:\n    image: nginx\n    security_opt: [no-new-privileges:true]\n    cap_drop: [ALL]\n",
	}
	for _, c := range ok {
		if err := Check(c, false); err != nil {
			t.Errorf("Check(%q) = %v; want nil", c, err)
		}
	}
	bad := map[string]string{
		"services:\n  web:\n    image: nginx\n    privileged: true\n":                                   "privileged",
		"services:\n  web:\n    image: nginx\n    network_mode: host\n":                                 "network_mode: host",
		"services:\n  web:\n    image: nginx\n    pid: host\n":                                          "pid: host",
		"services:\n  web:\n    image: nginx\n    cap_add: [SYS_ADMIN]\n":                               "cap_add",
		"services:\n  web:\n    image: nginx\n    devices: ['/dev/sda:/dev/sda']\n":                     "devices",
		"services:\n  web:\n    image: nginx\n    security_opt: ['seccomp=unconfined']\n":               "security_opt",
		"services:\n  web:\n    image: nginx\n    volumes: ['/:/host']\n":                               "root of the server",
		"services:\n  web:\n    image: nginx\n    volumes: ['/var/run/docker.sock:/var/run/docker.sock']\n": "cannot be mounted",
		"services:\n  web:\n    image: nginx\n    volumes: ['/srv/media:/media']\n":                     "administrator",
		"services:\n  web:\n    image: nginx\n    volumes: ['../../secret.key:/k']\n":                   "leaves the repository",
		"services:\n  web:\n    image: nginx\n    volumes:\n      - type: bind\n        source: /etc/passwd\n        target: /p\n": "cannot be mounted",
		"services:\n  web:\n    image: nginx\n    volumes:\n      - type: bind\n        source: /srv/x\n        target: /x\n": "administrator",
		"services:\n  web:\n    image: nginx\n    volumes: ['~/.ssh:/root/.ssh']\n":                      "cannot be mounted",
	}
	for c, want := range bad {
		err := Check(c, false)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Check(%q) = %v; want error containing %q", c, err, want)
		}
	}
	// With host access granted by an administrator, host paths, devices and
	// capabilities pass, but the Docker socket and the root never do.
	allowed := "services:\n  web:\n    image: nginx\n    privileged: true\n    cap_add: [NET_ADMIN]\n    devices: ['/dev/dri:/dev/dri']\n    volumes: ['/srv/media:/media']\n"
	if err := Check(allowed, true); err != nil {
		t.Errorf("host access: %v", err)
	}
	for _, c := range []string{"services:\n  web:\n    image: nginx\n    volumes: ['/var/run/docker.sock:/s']\n", "services:\n  web:\n    image: nginx\n    volumes: ['/:/host']\n"} {
		if err := Check(c, true); err == nil {
			t.Errorf("Check(%q, true) = nil; want an error", c)
		}
	}
	if err := Check("services: [", false); err == nil {
		t.Error("invalid YAML accepted")
	}
}
