package docker

import (
	"encoding/csv"
	"fmt"
	"regexp"
	"strings"

	"github.com/portdock/portdock/internal/executor"
)

// Extra `docker run` options typed by users are parsed into arguments, checked
// against an allow-list and quoted one by one, so they can neither break out of
// the shell command nor give a container the host: no privileged mode, host
// namespaces, capabilities, devices or bind mounts of host paths. Named volumes
// are fine. Administrators (who already administer the servers over SSH) may
// also use bind mounts and devices.

// runFlags maps an option to whether it takes a value. Anything absent is refused.
var runFlags = map[string]bool{
	"-v": true, "--volume": true, "--mount": true, "--tmpfs": true,
	"-e": true, "--env": true, "-l": true, "--label": true,
	"-u": true, "--user": true, "-w": true, "--workdir": true, "--entrypoint": true,
	"--hostname": true, "-h": true, "--add-host": true, "--dns": true, "--dns-search": true, "--dns-option": true, "--domainname": true,
	"--expose": true, "--shm-size": true, "--ulimit": true, "--sysctl": true, "--pids-limit": true,
	"--memory-swap": true, "--memory-reservation": true, "--memory-swappiness": true, "--cpu-shares": true, "--cpuset-cpus": true, "--cpu-period": true, "--cpu-quota": true, "--cpus": true, "--memory": true, "-m": true,
	"--stop-signal": true, "--stop-timeout": true, "--health-cmd": true, "--health-interval": true, "--health-timeout": true, "--health-retries": true, "--health-start-period": true,
	"--cap-drop": true, "--security-opt": true, "--group-add": true, "--gpus": true, "--platform": true, "--log-opt": true, "--restart": true, "--oom-score-adj": true, "--storage-opt": true, "--annotation": true,
	"--init": false, "--read-only": false, "--no-healthcheck": false, "-t": false, "--tty": false, "-i": false, "--interactive": false,
}

// trustedFlags are allowed only for administrators.
var trustedFlags = map[string]bool{"--device": true, "--device-read-bps": true, "--device-write-bps": true, "--device-read-iops": true, "--device-write-iops": true}

// CheckLabel refuses the labels that PortDock and the proxy act on: the proxy
// reads traefik.* labels from containers (a router label could take over
// another application's domain) and PortDock finds its containers by its own
// labels. Defining Traefik middlewares is fine; they only apply to routers
// that reference them.
func CheckLabel(key string) error {
	k := strings.ToLower(strings.TrimSpace(key))
	if strings.HasPrefix(k, "traefik.http.middlewares.") {
		return nil
	}
	for _, p := range []string{"traefik.", "portdock.", "shipyard."} {
		if strings.HasPrefix(k, p) {
			return fmt.Errorf("labels starting with %s are managed by PortDock (only traefik.http.middlewares.* can be added)", p)
		}
	}
	return nil
}

// ParseLabels splits "key=value" lines into labels, refusing reserved keys.
func ParseLabels(text string) (map[string]string, error) {
	out := map[string]string{}
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		k, v, found := strings.Cut(l, "=")
		k = strings.TrimSpace(k)
		if !found || k == "" {
			return nil, fmt.Errorf("label %q must be key=value", l)
		}
		if err := CheckLabel(k); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

var volumeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// RunOptions validates extra `docker run` options and returns them quoted for
// the shell, as one string that starts with a space (or "" when empty).
func RunOptions(opts string, trusted bool) (string, error) {
	args, err := executor.SplitArgs(strings.TrimSpace(opts))
	if err != nil {
		return "", fmt.Errorf("docker options: %w", err)
	}
	var out []string
	for i := 0; i < len(args); i++ {
		flag, value, inline := strings.Cut(args[i], "=")
		if !strings.HasPrefix(flag, "-") || flag == "-" || flag == "--" {
			return "", fmt.Errorf("docker options: %q is not an option; put command arguments in the start command", args[i])
		}
		// Short flags may carry their value attached (-e FOO=bar is split by "=" above, so re-join).
		if len(flag) == 2 && strings.HasPrefix(flag, "-") && !strings.HasPrefix(flag, "--") && inline {
			value, inline = flag[2:]+"="+value, false
			flag = flag[:2]
			if value == "=" {
				value = ""
			}
			inline = value != ""
		}
		takesValue, known := runFlags[flag]
		if !known {
			if !trustedFlags[flag] {
				return "", fmt.Errorf("docker options: %s is not allowed", flag)
			}
			if !trusted {
				return "", fmt.Errorf("docker options: %s can only be set by an administrator", flag)
			}
			takesValue = true
		}
		if takesValue && !inline {
			if i+1 >= len(args) {
				return "", fmt.Errorf("docker options: %s needs a value", flag)
			}
			i++
			value = args[i]
		}
		if !takesValue && inline {
			return "", fmt.Errorf("docker options: %s takes no value", flag)
		}
		if err := checkRunValue(flag, value, trusted); err != nil {
			return "", err
		}
		if takesValue {
			out = append(out, flag, executor.Quote(value))
		} else {
			out = append(out, flag)
		}
	}
	if len(out) == 0 {
		return "", nil
	}
	return " " + strings.Join(out, " "), nil
}

func checkRunValue(flag, value string, trusted bool) error {
	switch flag {
	case "-v", "--volume":
		src, _, hasTarget := strings.Cut(value, ":")
		if !hasTarget {
			return nil // anonymous volume
		}
		if !volumeName.MatchString(src) {
			if !trusted {
				return fmt.Errorf("docker options: only named volumes can be mounted (%q); bind mounts of server paths are for administrators", value)
			}
			if err := CheckBindSource(src); err != nil {
				return fmt.Errorf("docker options: %w", err)
			}
		} else if err := checkVolumeOwner(src, trusted); err != nil {
			return err
		}
	case "--mount":
		fields, err := mountFields(value)
		if err != nil {
			return err
		}
		typ := fields["type"]
		src := fields["source"]
		if src == "" {
			src = fields["src"]
		}
		switch {
		case typ == "" || typ == "volume":
			if src != "" && !volumeName.MatchString(src) {
				return fmt.Errorf("docker options: invalid volume name %q", src)
			}
			if err := checkVolumeOwner(src, trusted); err != nil {
				return err
			}
		case typ == "tmpfs":
		case typ == "bind":
			if !trusted {
				return fmt.Errorf("docker options: bind mounts of server paths are for administrators")
			}
			if err := CheckBindSource(src); err != nil {
				return fmt.Errorf("docker options: %w", err)
			}
		default:
			return fmt.Errorf("docker options: mount type %q is not allowed", typ)
		}
	case "-l", "--label":
		key, _, _ := strings.Cut(value, "=")
		if err := CheckLabel(key); err != nil {
			return fmt.Errorf("docker options: %w", err)
		}
	case "--security-opt":
		if v := strings.ToLower(strings.TrimSpace(value)); v != "no-new-privileges" && v != "no-new-privileges=true" && !strings.HasPrefix(v, "seccomp=") && !strings.HasPrefix(v, "apparmor=") {
			return fmt.Errorf("docker options: security option %q is not allowed", value)
		}
		if v := strings.ToLower(value); strings.Contains(v, "unconfined") || strings.Contains(v, "seccomp=/") {
			if !trusted {
				return fmt.Errorf("docker options: security option %q can only be set by an administrator", value)
			}
		}
	case "--sysctl":
		if !strings.HasPrefix(value, "net.") {
			return fmt.Errorf("docker options: only net.* sysctls can be set")
		}
	case "--restart":
		if strings.HasPrefix(value, "no") {
			return fmt.Errorf("docker options: the restart policy is managed by PortDock")
		}
	case "--log-opt":
		if !strings.HasPrefix(value, "max-size=") && !strings.HasPrefix(value, "max-file=") && !strings.HasPrefix(value, "tag=") && !strings.HasPrefix(value, "labels=") && !strings.HasPrefix(value, "env=") {
			return fmt.Errorf("docker options: log option %q is not allowed", value)
		}
	}
	return nil
}

// reservedVolume matches the Docker volumes PortDock names: databases
// (sy-<uuid>), compose projects (sy-<uuid>_<name>), managed volumes of
// applications (pd-<uuid>_<name>) and those the image catalog used to create
// (sy-<app>-<suffix>-<n>). Options saved earlier keep working: they are
// validated as trusted while unchanged.
var reservedVolume = regexp.MustCompile(`^(sy|pd)-`)

func checkVolumeOwner(name string, trusted bool) error {
	if !trusted && reservedVolume.MatchString(strings.ToLower(name)) {
		return fmt.Errorf("docker options: volume %s belongs to another PortDock resource; add persistent storage from the Storage tab", name)
	}
	return nil
}

// mountKeys are the --mount fields that are allowed. volume-driver and
// volume-opt are not: the local driver's options mount any path of the server
// (type=none,o=bind,device=/).
var mountKeys = map[string]bool{
	"type": true, "source": true, "src": true, "target": true, "destination": true, "dst": true,
	"readonly": true, "ro": true, "volume-nocopy": true, "volume-subpath": true, "volume-label": true,
	"tmpfs-size": true, "tmpfs-mode": true, "bind-propagation": true, "bind-recursive": true, "bind-nonrecursive": true, "consistency": true,
}

// mountFields parses a --mount value the way Docker does (CSV, so quoted
// fields can hold commas) and refuses unknown fields.
func mountFields(value string) (map[string]string, error) {
	r := csv.NewReader(strings.NewReader(value))
	r.LazyQuotes = true
	rec, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("docker options: invalid --mount %q: %v", value, err)
	}
	fields := map[string]string{}
	for _, f := range rec {
		k, v, _ := strings.Cut(strings.TrimSpace(f), "=")
		k = strings.ToLower(strings.TrimSpace(k))
		if !mountKeys[k] {
			return nil, fmt.Errorf("docker options: --mount option %q is not allowed", k)
		}
		if _, dup := fields[k]; dup {
			return nil, fmt.Errorf("docker options: --mount option %q is repeated", k)
		}
		fields[k] = v
	}
	return fields, nil
}

// CheckBindSource refuses the host paths that hand over the host even to an
// administrator's application: the root filesystem, the Docker socket and the
// directories Docker and the system keep their state in.
func CheckBindSource(src string) error {
	p := strings.TrimRight(src, "/")
	if p == "" || p == "/" {
		return fmt.Errorf("the root of the server cannot be mounted")
	}
	for _, bad := range []string{"/var/run/docker.sock", "/run/docker.sock", "/var/lib/docker", "/etc", "/proc", "/sys", "/dev", "/boot", "/root", "/var/run", "/run"} {
		if p == bad || strings.HasPrefix(p, bad+"/") {
			return fmt.Errorf("%s cannot be mounted into a container", src)
		}
	}
	return nil
}

// CommandArgs validates extra arguments for a container's command (for example
// database server flags) and returns them quoted, prefixed with a space.
func CommandArgs(args string) (string, error) {
	parts, err := executor.SplitArgs(strings.TrimSpace(args))
	if err != nil {
		return "", fmt.Errorf("arguments: %w", err)
	}
	if len(parts) == 0 {
		return "", nil
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(" " + executor.Quote(p))
	}
	return b.String(), nil
}
