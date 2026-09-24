package composefile

import (
	"fmt"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Check refuses the settings of a compose file that would hand a container the
// server: privileged mode, host namespaces, added capabilities, devices, and
// bind mounts of host paths (relative paths inside the checkout are fine).
// With hostAccess, which an administrator grants per application, host paths,
// devices and capabilities are allowed, but never the Docker socket, the root
// filesystem or the system directories.
func Check(raw string, hostAccess bool) error {
	var spec struct {
		Services map[string]yaml.Node `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(raw), &spec); err != nil {
		return fmt.Errorf("invalid compose file: %w", err)
	}
	names := make([]string, 0, len(spec.Services))
	for n := range spec.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := checkService(n, spec.Services[n], hostAccess); err != nil {
			return err
		}
	}
	return nil
}

func checkService(name string, node yaml.Node, hostAccess bool) error {
	var svc struct {
		Privileged  bool        `yaml:"privileged"`
		NetworkMode string      `yaml:"network_mode"`
		PID         string      `yaml:"pid"`
		IPC         string      `yaml:"ipc"`
		UsernsMode  string      `yaml:"userns_mode"`
		Cgroup      string      `yaml:"cgroup"`
		CapAdd      []string    `yaml:"cap_add"`
		Devices     []yaml.Node `yaml:"devices"`
		SecurityOpt []string    `yaml:"security_opt"`
		Volumes     []yaml.Node `yaml:"volumes"`
		Sysctls     yaml.Node   `yaml:"sysctls"`
	}
	if err := node.Decode(&svc); err != nil {
		return fmt.Errorf("service %s: %w", name, err)
	}
	deny := func(what string) error {
		if hostAccess {
			return nil
		}
		return fmt.Errorf("service %s uses %s, which gives the container access to the server; an administrator can allow host access for this application", name, what)
	}
	if svc.Privileged {
		if err := deny("privileged: true"); err != nil {
			return err
		}
	}
	if svc.NetworkMode == "host" {
		if err := deny("network_mode: host"); err != nil {
			return err
		}
	}
	for k, v := range map[string]string{"pid": svc.PID, "ipc": svc.IPC, "userns_mode": svc.UsernsMode, "cgroup": svc.Cgroup} {
		if v == "host" {
			if err := deny(k + ": host"); err != nil {
				return err
			}
		}
	}
	if len(svc.CapAdd) > 0 {
		if err := deny("cap_add"); err != nil {
			return err
		}
	}
	if len(svc.Devices) > 0 {
		if err := deny("devices"); err != nil {
			return err
		}
	}
	for _, o := range svc.SecurityOpt {
		if l := strings.ToLower(o); strings.Contains(l, "unconfined") || strings.HasPrefix(l, "label=disable") {
			if err := deny("security_opt: " + o); err != nil {
				return err
			}
		}
	}
	for _, v := range svc.Volumes {
		src, err := volumeSource(v)
		if err != nil {
			return fmt.Errorf("service %s: %w", name, err)
		}
		if src == "" || isNamedVolume(src) {
			continue
		}
		if strings.HasPrefix(src, "./") || src == "." || (!strings.HasPrefix(src, "/") && !strings.HasPrefix(src, "~") && !strings.HasPrefix(src, "..")) {
			// Relative to the compose file, inside the checkout — unless it climbs out.
			if !climbsOut(src) {
				continue
			}
		}
		if err := forbiddenHostPath(src); err != nil {
			return fmt.Errorf("service %s: %w", name, err)
		}
		if err := deny("a bind mount of the server path " + src); err != nil {
			return err
		}
	}
	return nil
}

// volumeSource returns the host side of a volume entry, in either syntax.
func volumeSource(v yaml.Node) (string, error) {
	switch v.Kind {
	case yaml.ScalarNode:
		s := v.Value
		if strings.HasPrefix(s, "/") || strings.HasPrefix(s, ".") || strings.HasPrefix(s, "~") {
			// "/host:/ctr", "./dir:/ctr" — the first colon after the path separates source and target.
			if i := strings.Index(s, ":"); i > 0 {
				return s[:i], nil
			}
			return s, nil
		}
		parts := strings.Split(s, ":")
		if len(parts) == 1 {
			return "", nil // anonymous volume
		}
		return parts[0], nil
	case yaml.MappingNode:
		var long struct {
			Type   string `yaml:"type"`
			Source string `yaml:"source"`
		}
		if err := v.Decode(&long); err != nil {
			return "", err
		}
		switch long.Type {
		case "bind":
			if long.Source == "" {
				return "/", nil
			}
			return long.Source, nil
		case "", "volume", "tmpfs", "npipe", "cluster":
			if long.Type == "" && long.Source != "" && !isNamedVolume(long.Source) {
				return long.Source, nil
			}
			return "", nil
		}
		return "", fmt.Errorf("volume type %q is not allowed", long.Type)
	}
	return "", nil
}

func isNamedVolume(src string) bool {
	if src == "" || strings.ContainsAny(src, "/\\") || strings.HasPrefix(src, ".") || strings.HasPrefix(src, "~") || strings.HasPrefix(src, "$") {
		return false
	}
	return true
}

func climbsOut(rel string) bool {
	for _, seg := range strings.Split(strings.ReplaceAll(rel, "\\", "/"), "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// forbiddenHostPath lists what is never mounted, host access or not.
func forbiddenHostPath(src string) error {
	p := strings.TrimRight(src, "/")
	if p == "" || p == "/" || strings.HasPrefix(src, "~") || strings.HasPrefix(src, "$") {
		return fmt.Errorf("the root of the server (or a path resolved on it, %q) cannot be mounted", src)
	}
	if climbsOut(p) {
		return fmt.Errorf("the path %q leaves the repository", src)
	}
	for _, bad := range []string{"/var/run/docker.sock", "/run/docker.sock", "/var/lib/docker", "/etc", "/proc", "/sys", "/dev", "/boot", "/root", "/var/run", "/run", "/data/portdock"} {
		if p == bad || strings.HasPrefix(p, bad+"/") {
			return fmt.Errorf("%s cannot be mounted into a container", src)
		}
	}
	return nil
}
