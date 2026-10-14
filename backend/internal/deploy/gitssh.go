package deploy

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/store"
)

// Git over SSH: repositories as git@host:group/repo.git or
// ssh://git@host:2222/group/repo.git, cloned with the application's own
// deploy key. The host key is checked against PortDock's list of known Git
// hosts (Settings → Git sources): an unknown host is trusted on first use
// with its fingerprint written to the deployment log, or refused when the
// administrators only allow the hosts they listed (git_ssh_strict).

// GitKnownHost is a trusted host key of a Git server.
type GitKnownHost struct {
	Host         string `db:"host" json:"host"` // host, or [host]:port
	Keys         string `db:"keys" json:"keys"` // known_hosts lines
	Fingerprints string `db:"fingerprints" json:"fingerprints"`
	Source       string `db:"source" json:"source"` // "first use" | "admin"
	CreatedAt    string `db:"created_at" json:"created_at"`
}

var (
	scpLikeRe  = regexp.MustCompile(`^([A-Za-z0-9._-]+)@([A-Za-z0-9.-]+):([^/].*)$`)
	sshHostRe  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
	sshUserRe  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	gitPathBad = regexp.MustCompile(`[\s'"\\` + "`" + `$;|&<>]`)
)

// ParseSSHRepo returns the host and port of an SSH repository URL; ok is
// false for other URLs (https://…).
func ParseSSHRepo(raw string) (host string, port int, ok bool, err error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "ssh://") {
		u, perr := url.Parse(raw)
		if perr != nil || u.Hostname() == "" || u.Path == "" || u.Path == "/" {
			return "", 0, true, errors.New("invalid SSH repository URL (expected ssh://git@host[:port]/group/repo.git)")
		}
		host, port = u.Hostname(), 22
		if p := u.Port(); p != "" {
			if port, perr = strconv.Atoi(p); perr != nil || port < 1 || port > 65535 {
				return "", 0, true, fmt.Errorf("invalid port %q", p)
			}
		}
		if u.User != nil {
			if _, hasPass := u.User.Password(); hasPass || !sshUserRe.MatchString(u.User.Username()) {
				return "", 0, true, errors.New("an SSH repository URL takes a user name only (the deploy key authenticates)")
			}
		}
		if gitPathBad.MatchString(u.Path) {
			return "", 0, true, errors.New("the repository path contains characters that are not allowed")
		}
	} else if m := scpLikeRe.FindStringSubmatch(raw); m != nil && !strings.Contains(raw, "://") {
		host, port = m[2], 22
		if gitPathBad.MatchString(m[3]) {
			return "", 0, true, errors.New("the repository path contains characters that are not allowed")
		}
	} else {
		return "", 0, false, nil
	}
	// A host starting with "-" would be read by ssh as an option.
	if !sshHostRe.MatchString(host) {
		return "", 0, true, fmt.Errorf("invalid SSH host %q", host)
	}
	return host, port, true, nil
}

// KnownHostName is how known_hosts names a host.
func KnownHostName(host string, port int) string {
	if port == 22 {
		return host
	}
	return fmt.Sprintf("[%s]:%d", host, port)
}

// KeyScan reads the host keys of an SSH server from server x (where the
// clones run) and returns them as known_hosts lines with their fingerprints.
func (e *Engine) KeyScan(ctx context.Context, x executor.Executor, host string, port int) (keys string, fingerprints []string, err error) {
	out, err := executor.Output(ctx, x, fmt.Sprintf("docker run --rm%s --entrypoint ssh-keyscan %s -T 10 -p %d %s 2>/dev/null",
		envFlags(buildProxyEnv(e.Network(ctx))), gitImage, port, q(host)))
	if err != nil && strings.TrimSpace(out) == "" {
		return "", nil, fmt.Errorf("%s:%d did not answer as an SSH server", host, port)
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		_, _, key, _, _, perr := ssh.ParseKnownHosts([]byte(l))
		if perr != nil {
			continue
		}
		lines = append(lines, l)
		fingerprints = append(fingerprints, key.Type()+" "+ssh.FingerprintSHA256(key))
	}
	if len(lines) == 0 {
		return "", nil, fmt.Errorf("no host key received from %s:%d", host, port)
	}
	return strings.Join(lines, "\n") + "\n", fingerprints, nil
}

// ValidKnownHosts checks known_hosts lines for name and returns their fingerprints.
func ValidKnownHosts(name, keys string) ([]string, error) {
	var fps []string
	for _, l := range strings.Split(keys, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		_, hosts, key, _, _, err := ssh.ParseKnownHosts([]byte(l))
		if err != nil {
			return nil, fmt.Errorf("invalid known_hosts line %q", l)
		}
		match := false
		for _, h := range hosts {
			match = match || h == name
		}
		if !match {
			return nil, fmt.Errorf("the line is for %s, not %s", strings.Join(hosts, ","), name)
		}
		fps = append(fps, key.Type()+" "+ssh.FingerprintSHA256(key))
	}
	if len(fps) == 0 {
		return nil, errors.New("no host key")
	}
	return fps, nil
}

// knownHostsFor returns the trusted keys of host, trusting it on first use
// unless the administrators only allow listed hosts.
func (e *Engine) knownHostsFor(ctx context.Context, x executor.Executor, host string, port int, logf func(string, ...any)) (string, error) {
	name := KnownHostName(host, port)
	if h, err := store.Get[GitKnownHost](ctx, e.db, "SELECT * FROM git_known_hosts WHERE host = ?", name); err == nil {
		return h.Keys, nil
	}
	if e.db.Setting(ctx, "git_ssh_strict") == "true" {
		return "", fmt.Errorf("%s is not a known Git host: an administrator adds its host key in Settings → Git sources", name)
	}
	keys, fps, err := e.KeyScan(ctx, x, host, port)
	if err != nil {
		return "", err
	}
	h := GitKnownHost{Host: name, Keys: keys, Fingerprints: strings.Join(fps, "\n"), Source: "first use", CreatedAt: store.Now()}
	if _, err := e.db.ExecContext(ctx, "INSERT OR IGNORE INTO git_known_hosts(host, keys, fingerprints, source, created_at) VALUES (?, ?, ?, ?, ?)",
		h.Host, h.Keys, h.Fingerprints, h.Source, h.CreatedAt); err != nil {
		return "", err
	}
	logf("Trusted the SSH host key of %s on first use: %s. Check it in Settings → Git sources.", name, strings.Join(fps, ", "))
	return keys, nil
}

// gitSSH prepares an SSH clone of app's repository in workdir: it writes the
// deploy key and the host keys under workdir/.ssh (outside the build context)
// and returns the docker flag setting GIT_SSH_COMMAND, plus a cleanup.
func (j *job) gitSSH(ctx context.Context) (flags string, cleanup func(), err error) {
	host, port, isSSH, err := ParseSSHRepo(j.app.GitRepository)
	if err != nil || !isSSH {
		return "", func() {}, err
	}
	if j.app.GitSSHKey == "" {
		return "", func() {}, errors.New("this repository is cloned over SSH: generate a deploy key in the application's settings and add its public key to the repository")
	}
	priv, err := j.e.box.Decrypt(j.app.GitSSHKey)
	if err != nil {
		return "", func() {}, err
	}
	known, err := j.e.knownHostsFor(ctx, j.x, host, port, j.log.Printf)
	if err != nil {
		return "", func() {}, err
	}
	dir := j.workdir + "/.ssh"
	cleanup = func() { _, _ = executor.Output(context.WithoutCancel(ctx), j.x, "rm -rf "+q(dir)) }
	if err := executor.WriteFile(ctx, j.x, dir+"/id_deploy", []byte(strings.TrimSpace(priv)+"\n"), "0600"); err != nil {
		return "", cleanup, err
	}
	if err := executor.WriteFile(ctx, j.x, dir+"/known_hosts", []byte(known), "0644"); err != nil {
		return "", cleanup, err
	}
	cmd := "ssh -i /work/.ssh/id_deploy -o IdentitiesOnly=yes -o UserKnownHostsFile=/work/.ssh/known_hosts -o StrictHostKeyChecking=yes -o BatchMode=yes -o ConnectTimeout=20"
	return " -e " + q("GIT_SSH_COMMAND="+cmd), cleanup, nil
}
