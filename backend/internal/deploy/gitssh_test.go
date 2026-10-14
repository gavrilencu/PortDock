package deploy

import (
	"strings"
	"testing"

	"github.com/portdock/portdock/internal/executor"
)

func TestParseSSHRepo(t *testing.T) {
	for _, c := range []struct {
		in       string
		host     string
		port     int
		ssh, bad bool
	}{
		{in: "git@gitlab.corp.local:team/app.git", host: "gitlab.corp.local", port: 22, ssh: true},
		{in: "ssh://git@bitbucket.corp:7999/proj/repo.git", host: "bitbucket.corp", port: 7999, ssh: true},
		{in: "ssh://gitea.internal/owner/repo", host: "gitea.internal", port: 22, ssh: true},
		{in: "https://gitlab.com/acme/web.git", ssh: false},
		{in: "http://git.local/x.git", ssh: false},
		{in: "git@-oProxyCommand=evil:repo", ssh: false}, // not scp-like (host must start alnum)
		{in: "ssh://-oProxyCommand=evil/repo", ssh: true, bad: true},
		{in: "ssh://git:secret@host/repo", ssh: true, bad: true},
		{in: "ssh://git@host:99999/repo", ssh: true, bad: true},
		{in: "git@host:repo;rm -rf /", ssh: true, bad: true},
		{in: "ssh://git@host/", ssh: true, bad: true},
	} {
		host, port, isSSH, err := ParseSSHRepo(c.in)
		if isSSH != c.ssh || (err != nil) != c.bad {
			t.Errorf("%s: ssh=%v err=%v", c.in, isSSH, err)
			continue
		}
		if c.ssh && !c.bad && (host != c.host || port != c.port) {
			t.Errorf("%s: %s:%d", c.in, host, port)
		}
	}
	if KnownHostName("h", 22) != "h" || KnownHostName("h", 2222) != "[h]:2222" {
		t.Error("known_hosts names")
	}
}

func TestValidKnownHosts(t *testing.T) {
	_, pub, err := executor.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Join(strings.Fields(pub)[:2], " ")
	fps, err := ValidKnownHosts("[git.corp]:2222", "# comment\n[git.corp]:2222 "+key+"\n")
	if err != nil || len(fps) != 1 || !strings.HasPrefix(fps[0], "ssh-ed25519 SHA256:") {
		t.Fatalf("valid: %v %v", fps, err)
	}
	if _, err := ValidKnownHosts("git.corp", "other.host "+key); err == nil {
		t.Error("a key of another host was accepted")
	}
	if _, err := ValidKnownHosts("git.corp", "git.corp not-a-key"); err == nil {
		t.Error("garbage accepted")
	}
	if _, err := ValidKnownHosts("git.corp", "\n"); err == nil {
		t.Error("empty accepted")
	}
}
