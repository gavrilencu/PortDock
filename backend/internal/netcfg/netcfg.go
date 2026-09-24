// Package netcfg holds the network settings of a corporate environment: an
// HTTP(S) proxy for PortDock itself, for builds and for containers, a Docker
// Hub mirror (Nexus, Artifactory, Harbor…) for the Docker daemon of each
// server, and package mirrors (npm, PyPI, Go) used during builds.
package netcfg

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
)

// Config is the network configuration, loaded from the instance settings.
type Config struct {
	HTTPProxy  string // http://proxy.corp:3128, without credentials
	HTTPSProxy string // defaults to HTTPProxy
	NoProxy    string // comma separated
	Username   string
	Password   string

	// Where the proxy is used.
	PortDock   bool // PortDock's own requests (Git providers, updates, notifications, uptime checks…)
	Builds     bool // git clone, docker build, nixpacks, vulnerability database downloads
	Containers bool // HTTP_PROXY… in application and service containers (unless an application opts out)

	RegistryMirror   string // Docker Hub pull-through mirror, for the Docker daemon
	RegistryInsecure bool   // the mirror uses plain HTTP or a self-signed certificate

	NPMRegistry string            // npm / yarn / pnpm registry
	PyPIIndex   string            // pip index URL
	GoProxy     string            // GOPROXY
	BuildEnv    map[string]string // other build variables (MAVEN_OPTS, NUGET_SOURCE…)
	TrivyDB     string            // mirror of ghcr.io/aquasecurity/trivy-db
}

// DefaultNoProxy is proposed for new configurations: loopback, private networks and Docker's own names.
const DefaultNoProxy = "localhost,127.0.0.1,::1,.local,.internal,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"

func withAuth(raw, user, pass string) string {
	if raw == "" || user == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User = url.UserPassword(user, pass)
	return u.String()
}

// ProxyURLs returns the proxy URLs with their credentials.
func (c Config) ProxyURLs() (httpURL, httpsURL string) {
	h, s := c.HTTPProxy, c.HTTPSProxy
	if s == "" {
		s = h
	}
	return withAuth(h, c.Username, c.Password), withAuth(s, c.Username, c.Password)
}

// Enabled reports whether a proxy is configured.
func (c Config) Enabled() bool { return c.HTTPProxy != "" || c.HTTPSProxy != "" }

// ProxyEnv returns HTTP_PROXY, HTTPS_PROXY and NO_PROXY in both cases, as
// understood by curl, git, npm, pip, Go, Java agents and most tools.
func (c Config) ProxyEnv() map[string]string {
	if !c.Enabled() {
		return map[string]string{}
	}
	h, s := c.ProxyURLs()
	env := map[string]string{}
	if h != "" {
		env["HTTP_PROXY"], env["http_proxy"] = h, h
	}
	if s != "" {
		env["HTTPS_PROXY"], env["https_proxy"] = s, s
	}
	if c.NoProxy != "" {
		env["NO_PROXY"], env["no_proxy"] = c.NoProxy, c.NoProxy
	}
	return env
}

// PackageEnv returns the variables that point package managers at the mirrors.
func (c Config) PackageEnv() map[string]string {
	env := map[string]string{}
	if c.NPMRegistry != "" {
		env["NPM_CONFIG_REGISTRY"] = c.NPMRegistry
		env["npm_config_registry"] = c.NPMRegistry
		env["YARN_NPM_REGISTRY_SERVER"] = c.NPMRegistry // yarn 2+
		env["BUN_CONFIG_REGISTRY"] = c.NPMRegistry
	}
	if c.PyPIIndex != "" {
		env["PIP_INDEX_URL"] = c.PyPIIndex
		env["UV_INDEX_URL"] = c.PyPIIndex
		if u, err := url.Parse(c.PyPIIndex); err == nil && u.Hostname() != "" {
			env["PIP_TRUSTED_HOST"] = u.Hostname()
		}
	}
	if c.GoProxy != "" {
		env["GOPROXY"] = c.GoProxy
	}
	for k, v := range c.BuildEnv {
		env[k] = v
	}
	return env
}

// Keys returns the keys of a variable map in a stable order.
func Keys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ParseEnvLines reads KEY=VALUE lines (blank lines and # comments are skipped).
func ParseEnvLines(s string) (map[string]string, error) {
	out := map[string]string{}
	for i, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || !validName(k) {
			return nil, fmt.Errorf("line %d: use KEY=value", i+1)
		}
		out[k] = strings.TrimSpace(v)
	}
	return out, nil
}

func validName(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// Validate checks the URLs of a configuration.
func (c Config) Validate() error {
	for name, raw := range map[string]string{"HTTP proxy": c.HTTPProxy, "HTTPS proxy": c.HTTPSProxy} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5") || u.Host == "" {
			return fmt.Errorf("%s: enter a URL such as http://proxy.corp.local:3128", name)
		}
		if u.User != nil {
			return fmt.Errorf("%s: enter the user name and password in their own fields", name)
		}
	}
	for name, raw := range map[string]string{"Docker Hub mirror": c.RegistryMirror, "npm registry": c.NPMRegistry, "PyPI index": c.PyPIIndex} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%s: enter a URL starting with https:// or http://", name)
		}
	}
	if (c.PortDock || c.Builds || c.Containers) && !c.Enabled() {
		return errors.New("enter the proxy address before choosing where it is used")
	}
	return nil
}

// ---- NO_PROXY ----

// Bypass reports whether host (with or without a port) must be reached
// directly according to a NO_PROXY list: exact names, domain suffixes
// (".corp" or "corp"), IP addresses, CIDR ranges, host:port and "*".
func Bypass(noProxy, hostport string) bool {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	ip := net.ParseIP(host)
	for _, entry := range strings.Split(noProxy, ",") {
		e := strings.ToLower(strings.TrimSpace(entry))
		if e == "" {
			continue
		}
		if e == "*" {
			return true
		}
		if _, cidr, err := net.ParseCIDR(e); err == nil {
			if ip != nil && cidr.Contains(ip) {
				return true
			}
			continue
		}
		eh, ep := e, ""
		if h, p, err := net.SplitHostPort(e); err == nil {
			eh, ep = h, p
		}
		eh = strings.Trim(eh, "[]")
		if ep != "" && ep != port {
			continue
		}
		if eip := net.ParseIP(eh); eip != nil {
			if ip != nil && eip.Equal(ip) {
				return true
			}
			continue
		}
		eh = strings.TrimPrefix(strings.TrimPrefix(eh, "*"), ".")
		if host == eh || strings.HasSuffix(host, "."+eh) {
			return true
		}
	}
	return false
}

// ProxyFor returns the proxy for a request URL (nil: connect directly).
func (c Config) ProxyFor(u *url.URL) (*url.URL, error) {
	if !c.Enabled() || u == nil {
		return nil, nil
	}
	hostport := u.Host
	if u.Port() == "" {
		port := "80"
		if u.Scheme == "https" {
			port = "443"
		}
		hostport = net.JoinHostPort(u.Hostname(), port)
	}
	host := u.Hostname()
	if host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback() {
		return nil, nil
	}
	if Bypass(c.NoProxy, hostport) {
		return nil, nil
	}
	h, s := c.ProxyURLs()
	raw := h
	if u.Scheme == "https" || u.Scheme == "wss" {
		raw = s
	}
	if raw == "" {
		return nil, nil
	}
	return url.Parse(raw)
}

// ---- PortDock's own requests ----

var current atomic.Pointer[Config]

// SetCurrent makes c the configuration of PortDock's outgoing requests.
func SetCurrent(c Config) { current.Store(&c) }

// Current returns the configuration set last.
func Current() Config {
	if c := current.Load(); c != nil {
		return *c
	}
	return Config{}
}

// Proxy is an http.Transport Proxy function: the corporate proxy when it is
// enabled for PortDock, otherwise the HTTP_PROXY environment of the process.
func Proxy(req *http.Request) (*url.URL, error) {
	if c := current.Load(); c != nil && c.PortDock {
		return c.ProxyFor(req.URL)
	}
	return http.ProxyFromEnvironment(req)
}

// Install routes the requests of http.DefaultTransport (and so of every
// client built on it) through Proxy.
func Install() {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		t.Proxy = Proxy
	}
}

// ---- settings ----

// Setting keys of the configuration.
var SettingKeys = []string{
	"proxy_http", "proxy_https", "proxy_no", "proxy_username", "proxy_password",
	"proxy_portdock", "proxy_builds", "proxy_containers",
	"registry_mirror", "registry_insecure", "npm_registry", "pypi_index", "go_proxy", "build_env", "trivy_db_repository",
}

// FromSettings builds a configuration from setting values (the password already decrypted).
func FromSettings(get func(string) string) Config {
	env, _ := ParseEnvLines(get("build_env"))
	return Config{
		HTTPProxy: get("proxy_http"), HTTPSProxy: get("proxy_https"), NoProxy: get("proxy_no"),
		Username: get("proxy_username"), Password: get("proxy_password"),
		PortDock: get("proxy_portdock") == "true", Builds: get("proxy_builds") == "true", Containers: get("proxy_containers") == "true",
		RegistryMirror: strings.TrimRight(get("registry_mirror"), "/"), RegistryInsecure: get("registry_insecure") == "true",
		NPMRegistry: get("npm_registry"), PyPIIndex: get("pypi_index"), GoProxy: get("go_proxy"), BuildEnv: env,
		TrivyDB: get("trivy_db_repository"),
	}
}

// ---- Docker daemon ----

// DaemonProxyDropIn is the systemd drop-in that sends the Docker daemon's
// pulls through the proxy (/etc/systemd/system/docker.service.d/http-proxy.conf).
func (c Config) DaemonProxyDropIn() string {
	var b strings.Builder
	b.WriteString("# Written by PortDock\n[Service]\n")
	env := c.ProxyEnv()
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
		if v := env[k]; v != "" {
			b.WriteString("Environment=" + strconv.Quote(k+"="+v) + "\n")
		}
	}
	return b.String()
}

// MirrorHost is the host[:port] of the registry mirror.
func (c Config) MirrorHost() string {
	if u, err := url.Parse(c.RegistryMirror); err == nil {
		return u.Host
	}
	return ""
}

// ApplyDaemonJSON merges the registry mirror into an existing daemon.json;
// other keys are kept. An empty mirror removes the one PortDock set.
func (c Config) ApplyDaemonJSON(existing map[string]any, previousMirror string) map[string]any {
	if existing == nil {
		existing = map[string]any{}
	}
	mirrors := stringList(existing["registry-mirrors"])
	mirrors = remove(mirrors, previousMirror)
	insecure := stringList(existing["insecure-registries"])
	if prev, err := url.Parse(previousMirror); err == nil && prev.Host != "" {
		insecure = remove(insecure, prev.Host)
	}
	if c.RegistryMirror != "" {
		mirrors = append([]string{c.RegistryMirror}, remove(mirrors, c.RegistryMirror)...)
		if c.RegistryInsecure {
			insecure = append(remove(insecure, c.MirrorHost()), c.MirrorHost())
		}
	}
	// An emptied list stays in the file: dockerd keeps a mirror whose key is
	// missing when it reloads its configuration.
	set := func(k string, v []string) {
		_, had := existing[k]
		switch {
		case len(v) > 0:
			existing[k] = v
		case had || previousMirror != "":
			existing[k] = []string{}
		}
	}
	set("registry-mirrors", mirrors)
	set("insecure-registries", insecure)
	return existing
}

func stringList(v any) []string {
	var out []string
	if list, ok := v.([]any); ok {
		for _, x := range list {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	if list, ok := v.([]string); ok {
		out = append(out, list...)
	}
	return out
}

func remove(list []string, v string) []string {
	out := list[:0:0]
	for _, x := range list {
		if v == "" || strings.TrimRight(x, "/") != strings.TrimRight(v, "/") {
			out = append(out, x)
		}
	}
	return out
}

// Loader reads the configuration from the instance settings.
type Loader func(ctx context.Context) Config
