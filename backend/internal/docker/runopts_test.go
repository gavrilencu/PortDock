package docker

import (
	"strings"
	"testing"
)

func TestRunOptions(t *testing.T) {
	cases := []struct {
		in      string
		trusted bool
		want    string // "" with wantErr, or the expected rendering
		wantErr string
	}{
		{in: "", want: ""},
		{in: "-v sy-app-1:/data --shm-size 1g", want: " -v sy-app-1:/data --shm-size 1g"},
		{in: `-e FOO="a b" --init`, want: ` -e 'FOO=a b' --init`},
		{in: "--user=1000:1000 -w /app", want: " --user 1000:1000 -w /app"},
		{in: "--mount type=volume,source=cache,target=/cache", want: " --mount type=volume,source=cache,target=/cache"},
		{in: "--mount type=tmpfs,target=/tmp", want: " --mount type=tmpfs,target=/tmp"},
		{in: "--label app.team=shop", want: " --label app.team=shop"},
		{in: "--security-opt no-new-privileges --cap-drop ALL", want: " --security-opt no-new-privileges --cap-drop ALL"},
		{in: "--gpus all", want: " --gpus all"},
		// Shell metacharacters are quoted, never interpreted.
		{in: `-e CMD="; curl x | sh"`, want: ` -e 'CMD=; curl x | sh'`},
		// Not options at all.
		{in: "; curl x | sh #", wantErr: "not an option"},
		{in: "$(id)", wantErr: "not an option"},
		{in: "nginx", wantErr: "not an option"},
		// Denied for everyone.
		{in: "--privileged", wantErr: "not allowed"},
		{in: "--privileged", trusted: true, wantErr: "not allowed"},
		{in: "--pid host", wantErr: "not allowed"},
		{in: "--pid=host", trusted: true, wantErr: "not allowed"},
		{in: "--network host", wantErr: "not allowed"},
		{in: "--net=host", wantErr: "not allowed"},
		{in: "--cap-add SYS_ADMIN", trusted: true, wantErr: "not allowed"},
		{in: "--userns host", wantErr: "not allowed"},
		{in: "--ipc host", wantErr: "not allowed"},
		{in: "-p 80:80", wantErr: "not allowed"},
		{in: "--cgroupns host", wantErr: "not allowed"},
		{in: "--security-opt seccomp=unconfined", wantErr: "administrator"},
		{in: "--security-opt apparmor=unconfined", trusted: true, want: " --security-opt apparmor=unconfined"},
		{in: "--security-opt label=disable", trusted: true, wantErr: "not allowed"},
		{in: "--sysctl kernel.shmmax=1", wantErr: "net.*"},
		{in: "--label traefik.http.routers.x.rule=Host(`a`)", wantErr: "managed by PortDock"},
		{in: "--label traefik.enable=true", wantErr: "managed by PortDock"},
		{in: "--label traefik.http.middlewares.gz.compress=true", want: " --label traefik.http.middlewares.gz.compress=true"},
		{in: "-l portdock.resource=abc", trusted: true, wantErr: "managed by PortDock"},
		{in: "--restart no", wantErr: "managed"},
		{in: "--init=yes", wantErr: "takes no value"},
		{in: "-e", wantErr: "needs a value"},
		{in: `-e "unterminated`, wantErr: "unterminated"},
		// Bind mounts: members no, administrators yes, but never the host itself.
		{in: "-v /:/host", wantErr: "administrators"},
		{in: "-v /var/lib/data:/data", wantErr: "administrators"},
		{in: "-v ./x:/x", wantErr: "administrators"},
		{in: "-v /srv/media:/media:ro", trusted: true, want: " -v /srv/media:/media:ro"},
		{in: "-v /:/host", trusted: true, wantErr: "root of the server"},
		{in: "-v /var/run/docker.sock:/var/run/docker.sock", trusted: true, wantErr: "cannot be mounted"},
		{in: "-v /etc/passwd:/p", trusted: true, wantErr: "cannot be mounted"},
		{in: "--mount type=bind,source=/srv/x,target=/x", wantErr: "administrators"},
		{in: "--mount type=bind,source=/srv/x,target=/x", trusted: true, want: " --mount type=bind,source=/srv/x,target=/x"},
		{in: "--mount type=bind,src=/var/lib/docker,dst=/d", trusted: true, wantErr: "cannot be mounted"},
		{in: "--device /dev/dri:/dev/dri", wantErr: "administrator"},
		{in: "--device /dev/dri:/dev/dri", trusted: true, want: " --device /dev/dri:/dev/dri"},
	}
	for _, c := range cases {
		got, err := RunOptions(c.in, c.trusted)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("RunOptions(%q, %v) = %q, %v; want error containing %q", c.in, c.trusted, got, err, c.wantErr)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("RunOptions(%q, %v) = %q, %v; want %q", c.in, c.trusted, got, err, c.want)
		}
	}
}

func TestCommandArgs(t *testing.T) {
	if got, err := CommandArgs(`--max_connections=200 -c "shared_buffers=1GB"`); err != nil || got != " --max_connections=200 -c shared_buffers=1GB" {
		t.Fatalf("got %q, %v", got, err)
	}
	// Anything shell-like is one quoted argument for the server, not a command.
	if got, err := CommandArgs(`; curl x | sh`); err != nil || got != ` ';' curl x '|' sh` {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := CommandArgs(`"open`); err == nil {
		t.Fatal("expected an error for an unterminated quote")
	}
}
