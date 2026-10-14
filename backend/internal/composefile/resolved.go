package composefile

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/portdock/portdock/internal/docker"
	"go.yaml.in/yaml/v3"
)

// Policy is what a compose project may use on the server.
type Policy struct {
	// HostAccess, granted by an administrator, allows privileged mode, host
	// namespaces, devices, capabilities and paths of the server (except the
	// ones that hand over the host, see forbiddenHostPath).
	HostAccess bool
	// Root is the directory of the project on the server (the checkout):
	// files under it can be mounted, built and read.
	Root string
	// Project is the compose project name; the project's own volumes and
	// networks are named <project>_<name>.
	Project string
	// Network is the proxy network, which every project may join.
	Network string
	// Forbidden lists more server paths that are never mounted or read (PortDock's data).
	Forbidden []string
}

// CheckResolved checks the output of `docker compose config --no-env-resolution`:
// the compose file with variables interpolated, extends and include merged and
// paths made absolute, which is what `docker compose up` runs. Checking the
// file as written is not enough, since ${VAR}, extends and include can bring
// in anything. It returns the paths under Root that the project uses, which
// the caller resolves on the server to refuse symbolic links leaving Root.
func CheckResolved(rendered string, p Policy) ([]string, error) {
	var spec struct {
		Services map[string]yaml.Node `yaml:"services"`
		Volumes  map[string]struct {
			Name       string            `yaml:"name"`
			Driver     string            `yaml:"driver"`
			DriverOpts map[string]string `yaml:"driver_opts"`
			External   bool              `yaml:"external"`
		} `yaml:"volumes"`
		Networks map[string]struct {
			Name     string `yaml:"name"`
			Driver   string `yaml:"driver"`
			External bool   `yaml:"external"`
		} `yaml:"networks"`
		Secrets map[string]fileRef `yaml:"secrets"`
		Configs map[string]fileRef `yaml:"configs"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &spec); err != nil {
		return nil, fmt.Errorf("invalid compose configuration: %w", err)
	}
	c := checker{p: p, root: clean(p.Root)}
	deny := c.deny

	for _, n := range sortedKeys(spec.Volumes) {
		v := spec.Volumes[n]
		if v.External {
			if err := deny("volume " + n + " uses the existing volume " + v.Name + " (external)"); err != nil {
				return nil, err
			}
		} else if !c.own(v.Name) {
			if err := deny("volume " + n + " is named " + v.Name + " (outside the project)"); err != nil {
				return nil, err
			}
		}
		if v.Driver != "" && v.Driver != "local" {
			if err := deny("volume " + n + " uses the driver " + v.Driver); err != nil {
				return nil, err
			}
		}
		if len(v.DriverOpts) > 0 {
			if err := deny("volume " + n + " sets driver_opts (which can mount paths of the server)"); err != nil {
				return nil, err
			}
		}
	}
	for _, n := range sortedKeys(spec.Networks) {
		nw := spec.Networks[n]
		// macvlan, ipvlan and host put containers on the server's own network.
		if nw.Driver != "" && nw.Driver != "bridge" && nw.Driver != "overlay" {
			if err := deny("network " + n + " uses the driver " + nw.Driver); err != nil {
				return nil, err
			}
		}
		if nw.Name == p.Network || (!nw.External && c.own(nw.Name)) {
			continue
		}
		if err := deny("network " + n + " joins the network " + nw.Name + " (outside the project)"); err != nil {
			return nil, err
		}
	}
	for kind, refs := range map[string]map[string]fileRef{"secret": spec.Secrets, "config": spec.Configs} {
		for _, n := range sortedKeys(refs) {
			if f := refs[n].File; f != "" {
				if err := c.path(kind+" "+n, f); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, n := range sortedKeys(spec.Services) {
		if err := c.service(n, spec.Services[n]); err != nil {
			return nil, err
		}
	}
	// A container can replace anything under its writable bind mounts with a
	// symbolic link, between the check on the server and the moment Docker
	// mounts, builds or reads the path: no path the project uses may lie
	// inside another one's writable bind mount.
	for _, u := range c.used {
		for _, w := range c.writable {
			if strings.HasPrefix(u.path, w.path+"/") {
				c.what = ""
				if err := deny(fmt.Sprintf("%s %s is inside the writable bind mount %s of service %s (mount it read-only, :ro)",
					u.what, strings.TrimPrefix(u.path, c.root+"/"), strings.TrimPrefix(w.path, c.root+"/"), w.service)); err != nil {
					return nil, err
				}
			}
		}
	}
	paths := make([]string, 0, len(c.used))
	for _, u := range c.used {
		paths = append(paths, u.path)
	}
	sort.Strings(paths)
	return slices.Compact(paths), nil
}

type fileRef struct {
	File string `yaml:"file"`
}

type checker struct {
	p        Policy
	root     string
	used     []usedPath // paths under Root the project uses
	writable []usedPath // writable bind mounts under Root (what = the service)
	what     string     // the service being checked, for messages
}

type usedPath struct {
	what, path, service string
}

func (c *checker) deny(what string) error {
	if c.p.HostAccess {
		return nil
	}
	if c.what != "" {
		what = "service " + c.what + " " + what
	}
	return fmt.Errorf("%s, which gives the containers access to the server; an administrator can allow host access", what)
}

// own reports whether a volume or network name belongs to the project.
func (c *checker) own(name string) bool {
	return c.p.Project != "" && strings.HasPrefix(name, c.p.Project+"_")
}

// path checks a file or directory of the server the project uses.
func (c *checker) path(what, p string) error {
	if isURL(p) {
		return nil
	}
	p = clean(p)
	if c.root != "" && (p == c.root || strings.HasPrefix(p, c.root+"/")) {
		c.used = append(c.used, usedPath{what: what, path: p})
		return nil
	}
	if err := forbiddenHostPath(p); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	for _, f := range c.p.Forbidden {
		f = clean(f)
		if f != "" && (p == f || strings.HasPrefix(p, f+"/") || strings.HasPrefix(f, p+"/")) {
			return fmt.Errorf("%s: %s holds PortDock's data and cannot be used", what, p)
		}
	}
	return c.deny(what + " uses the server path " + p)
}

var namespaceModes = map[string]bool{"": true, "none": true, "bridge": true, "default": true}

func (c *checker) service(name string, node yaml.Node) error {
	var s struct {
		Privileged        bool        `yaml:"privileged"`
		NetworkMode       string      `yaml:"network_mode"`
		PID               string      `yaml:"pid"`
		IPC               string      `yaml:"ipc"`
		UTS               string      `yaml:"uts"`
		UsernsMode        string      `yaml:"userns_mode"`
		Cgroup            string      `yaml:"cgroup"`
		CapAdd            []string    `yaml:"cap_add"`
		Devices           []yaml.Node `yaml:"devices"`
		DeviceCgroupRules []string    `yaml:"device_cgroup_rules"`
		SecurityOpt       []string    `yaml:"security_opt"`
		Runtime           string      `yaml:"runtime"`
		UseAPISocket      bool        `yaml:"use_api_socket"`
		Provider          yaml.Node   `yaml:"provider"`
		Labels            yaml.Node   `yaml:"labels"`
		LabelFile         yaml.Node   `yaml:"label_file"`
		EnvFile           yaml.Node   `yaml:"env_file"`
		VolumesFrom       []string    `yaml:"volumes_from"`
		Volumes           []struct {
			Type     string `yaml:"type"`
			Source   string `yaml:"source"`
			ReadOnly bool   `yaml:"read_only"`
		} `yaml:"volumes"`
		Build *struct {
			Context            string            `yaml:"context"`
			Dockerfile         string            `yaml:"dockerfile"`
			AdditionalContexts map[string]string `yaml:"additional_contexts"`
			SSH                yaml.Node         `yaml:"ssh"`
			Network            string            `yaml:"network"`
			Privileged         bool              `yaml:"privileged"`
			Entitlements       []string          `yaml:"entitlements"`
			CacheFrom          []string          `yaml:"cache_from"`
			CacheTo            []string          `yaml:"cache_to"`
		} `yaml:"build"`
		PostStart []struct {
			Privileged bool `yaml:"privileged"`
		} `yaml:"post_start"`
		PreStop []struct {
			Privileged bool `yaml:"privileged"`
		} `yaml:"pre_stop"`
	}
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("service %s: %w", name, err)
	}
	c.what = name
	defer func() { c.what = "" }()
	deny := c.deny

	// The Docker socket hands over the server even with host access.
	if s.UseAPISocket {
		return fmt.Errorf("service %s uses use_api_socket (the Docker socket), which cannot be given to a container", name)
	}
	checks := []struct {
		bad  bool
		what string
	}{
		{s.Privileged, "uses privileged: true"},
		{s.Build != nil && s.Build.Privileged, "builds with privileged: true"},
		{len(s.CapAdd) > 0, "uses cap_add"},
		{len(s.Devices) > 0, "uses devices"},
		{len(s.DeviceCgroupRules) > 0, "uses device_cgroup_rules"},
		{s.UsernsMode == "host", "uses userns_mode: host"},
		{s.UTS == "host", "uses uts: host"},
		{s.Cgroup == "host", "uses cgroup: host"},
		{!namespaceModes[s.NetworkMode] && !strings.HasPrefix(s.NetworkMode, "service:"), "uses network_mode: " + s.NetworkMode},
		{badNamespace(s.PID), "uses pid: " + s.PID},
		{badNamespace(s.IPC), "uses ipc: " + s.IPC},
		{s.Runtime != "" && s.Runtime != "runc" && s.Runtime != "io.containerd.runc.v2", "uses the runtime " + s.Runtime},
		{!s.Provider.IsZero(), "uses a provider (a program run on the server)"},
		{s.Build != nil && s.Build.Network == "host", "builds with network: host"},
		{s.Build != nil && !s.Build.SSH.IsZero(), "builds with ssh (keys of the server)"},
		{s.Build != nil && len(s.Build.Entitlements) > 0, "builds with entitlements"},
		{s.Build != nil && slices.ContainsFunc(append(s.Build.CacheFrom, s.Build.CacheTo...), func(v string) bool { return strings.Contains(v, "type=local") }),
			"builds with a local cache (a path of the server)"},
	}
	for _, h := range append(s.PostStart, s.PreStop...) {
		if h.Privileged {
			checks = append(checks, struct {
				bad  bool
				what string
			}{true, "runs a privileged lifecycle hook"})
		}
	}
	for _, ch := range checks {
		if ch.bad {
			if err := deny(ch.what); err != nil {
				return err
			}
		}
	}
	for _, o := range s.SecurityOpt {
		l := strings.ToLower(o)
		customSeccomp := strings.HasPrefix(l, "seccomp") && l != "seccomp=default" && l != "seccomp:default"
		if customSeccomp || strings.Contains(l, "unconfined") || strings.Contains(l, "label=disable") || strings.Contains(l, "label:disable") {
			if err := deny("uses security_opt: " + o); err != nil {
				return err
			}
		}
	}
	for _, v := range s.VolumesFrom {
		if strings.HasPrefix(v, "container:") {
			if err := deny("mounts the volumes of " + v); err != nil {
				return err
			}
		}
	}
	for _, v := range s.Volumes {
		switch v.Type {
		case "bind":
			if err := c.path("bind mount", v.Source); err != nil {
				return err
			}
			if src := clean(v.Source); !v.ReadOnly && c.root != "" && (src == c.root || strings.HasPrefix(src, c.root+"/")) {
				c.writable = append(c.writable, usedPath{path: src, service: name})
			}
		case "volume", "tmpfs", "image", "":
		default:
			if err := deny("uses a volume of type " + v.Type); err != nil {
				return err
			}
		}
	}
	if s.Build != nil {
		if s.Build.Context != "" {
			if err := c.path("build context", s.Build.Context); err != nil {
				return err
			}
			if s.Build.Dockerfile != "" && !isURL(s.Build.Context) {
				df := s.Build.Dockerfile
				if !isAbs(df) {
					df = path.Join(clean(s.Build.Context), df)
				}
				if err := c.path("Dockerfile", df); err != nil {
					return err
				}
			}
		}
		for _, k := range sortedKeys(s.Build.AdditionalContexts) {
			v := s.Build.AdditionalContexts[k]
			if strings.Contains(v, "://") || strings.HasPrefix(v, "service:") || strings.HasPrefix(v, "target:") {
				continue
			}
			if err := c.path("build context "+k, v); err != nil {
				return err
			}
		}
	}
	for _, f := range stringsOf(s.EnvFile, "path") {
		if err := c.path("env_file", f); err != nil {
			return err
		}
	}
	for _, f := range stringsOf(s.LabelFile, "") {
		if err := c.path("label_file", f); err != nil {
			return err
		}
	}
	for _, k := range labelKeys(s.Labels) {
		if err := docker.CheckLabel(k); err != nil {
			if derr := deny("sets the label " + k); derr != nil {
				return fmt.Errorf("service %s: %w", name, err)
			}
		}
	}
	return nil
}

// badNamespace refuses sharing the host's or another container's namespace
// (sharing with a service of the project is fine).
func badNamespace(mode string) bool {
	return mode == "host" || strings.HasPrefix(mode, "container:")
}

// stringsOf reads a string, a list of strings, or a list of mappings holding key.
func stringsOf(n yaml.Node, key string) []string {
	switch n.Kind {
	case yaml.ScalarNode:
		return []string{n.Value}
	case yaml.SequenceNode:
		var out []string
		for _, item := range n.Content {
			switch item.Kind {
			case yaml.ScalarNode:
				out = append(out, item.Value)
			case yaml.MappingNode:
				for i := 0; i+1 < len(item.Content); i += 2 {
					if item.Content[i].Value == key {
						out = append(out, item.Content[i+1].Value)
					}
				}
			}
		}
		return out
	}
	return nil
}

// labelKeys reads labels written as a mapping or as a list of key=value.
func labelKeys(n yaml.Node) []string {
	var out []string
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			out = append(out, n.Content[i].Value)
		}
	case yaml.SequenceNode:
		for _, item := range n.Content {
			k, _, _ := strings.Cut(item.Value, "=")
			out = append(out, k)
		}
	}
	return out
}

var driveRe = regexp.MustCompile(`^[A-Za-z]:/`)

// clean normalizes a path of the server; Windows paths (Docker Desktop) use slashes.
func clean(p string) string {
	if p == "" {
		return ""
	}
	return path.Clean(strings.ReplaceAll(p, `\`, "/"))
}

func isAbs(p string) bool {
	p = strings.ReplaceAll(p, `\`, "/")
	return strings.HasPrefix(p, "/") || driveRe.MatchString(p)
}

func isURL(p string) bool {
	return strings.Contains(p, "://") || strings.HasPrefix(p, "git@")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// CheckEnvFiles refuses env files outside the project in a compose file as
// written (for Docker Compose versions whose `config` inlines them).
func CheckEnvFiles(raw string) error {
	var spec struct {
		Services map[string]struct {
			EnvFile yaml.Node `yaml:"env_file"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(raw), &spec); err != nil {
		return fmt.Errorf("invalid compose file: %w", err)
	}
	for _, n := range sortedKeys(spec.Services) {
		for _, f := range stringsOf(spec.Services[n].EnvFile, "path") {
			if isAbs(f) || climbsOut(f) || strings.ContainsAny(f, "$~") {
				return fmt.Errorf("service %s reads the env file %s outside the project", n, f)
			}
		}
	}
	return nil
}
