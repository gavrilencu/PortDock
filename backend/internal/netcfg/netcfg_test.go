package netcfg

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestBypass(t *testing.T) {
	no := "localhost,.corp.local,internal.example.com,10.0.0.0/8,192.168.1.5,[::1],git.local:8443,*.svc"
	cases := map[string]bool{
		"localhost:80":             true,
		"nexus.corp.local:8443":    true,
		"corp.local":               true,
		"evilcorp.local":           false,
		"internal.example.com":     true,
		"api.internal.example.com": true,
		"example.com":              false,
		"10.20.30.40:443":          true,
		"11.0.0.1":                 false,
		"192.168.1.5":              true,
		"192.168.1.6":              false,
		"[::1]:443":                true,
		"git.local:8443":           true,
		"git.local:443":            false,
		"db.svc":                   true,
		"github.com:443":           false,
	}
	for host, want := range cases {
		if got := Bypass(no, host); got != want {
			t.Errorf("Bypass(%q) = %v, want %v", host, got, want)
		}
	}
	if !Bypass("*", "anything.com") || Bypass("", "anything.com") {
		t.Error("wildcard / empty")
	}
}

func TestProxyForAndEnv(t *testing.T) {
	c := Config{HTTPProxy: "http://proxy.corp:3128", NoProxy: "localhost,.corp", Username: "dom\\alice", Password: "p@ss:w/rd"}
	u, _ := url.Parse("https://github.com/x")
	p, err := c.ProxyFor(u)
	if err != nil || p == nil || p.Host != "proxy.corp:3128" {
		t.Fatalf("proxy for github: %v %v", p, err)
	}
	if pw, _ := p.User.Password(); p.User.Username() != "dom\\alice" || pw != "p@ss:w/rd" {
		t.Fatalf("credentials: %v", p.User)
	}
	for _, direct := range []string{"http://nexus.corp/repo", "http://localhost:8080", "http://127.0.0.1:9000"} {
		u, _ := url.Parse(direct)
		if p, _ := c.ProxyFor(u); p != nil {
			t.Errorf("%s must be direct", direct)
		}
	}
	env := c.ProxyEnv()
	if env["HTTPS_PROXY"] != env["HTTP_PROXY"] || env["https_proxy"] == "" || env["NO_PROXY"] != "localhost,.corp" || !strings.Contains(env["HTTP_PROXY"], "%40") {
		t.Fatalf("env: %v", env)
	}
	if len((Config{}).ProxyEnv()) != 0 {
		t.Error("no proxy, no variables")
	}
	d := c.DaemonProxyDropIn()
	if !strings.Contains(d, "[Service]") || !strings.Contains(d, `Environment="NO_PROXY=localhost,.corp"`) || !strings.Contains(d, "HTTPS_PROXY=") {
		t.Fatalf("drop-in:\n%s", d)
	}
}

func TestPackageEnvAndValidate(t *testing.T) {
	extra, err := ParseEnvLines("# Maven\nMAVEN_OPTS=-Dmaven.repo.remote=https://nexus/maven\n\nNUGET_SOURCE = https://nexus/nuget\n")
	if err != nil {
		t.Fatal(err)
	}
	c := Config{NPMRegistry: "https://nexus.corp/repository/npm/", PyPIIndex: "https://nexus.corp/repository/pypi/simple", GoProxy: "https://nexus.corp/repository/go,direct", BuildEnv: extra}
	env := c.PackageEnv()
	for k, v := range map[string]string{
		"NPM_CONFIG_REGISTRY": "https://nexus.corp/repository/npm/", "npm_config_registry": "https://nexus.corp/repository/npm/",
		"PIP_INDEX_URL": "https://nexus.corp/repository/pypi/simple", "PIP_TRUSTED_HOST": "nexus.corp",
		"GOPROXY": "https://nexus.corp/repository/go,direct", "MAVEN_OPTS": "-Dmaven.repo.remote=https://nexus/maven", "NUGET_SOURCE": "https://nexus/nuget",
	} {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
	if _, err := ParseEnvLines("NOT VALID"); err == nil {
		t.Error("bad line accepted")
	}
	if _, err := ParseEnvLines("1ABC=x"); err == nil {
		t.Error("bad name accepted")
	}
	for _, bad := range []Config{
		{HTTPProxy: "proxy.corp:3128"},
		{HTTPProxy: "http://u:p@proxy.corp:3128"},
		{RegistryMirror: "nexus.corp:8443"},
		{Builds: true},
	} {
		if bad.Validate() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if err := (Config{HTTPProxy: "http://proxy.corp:3128", Builds: true, RegistryMirror: "https://nexus.corp:8443"}).Validate(); err != nil {
		t.Error(err)
	}
}

func TestApplyDaemonJSON(t *testing.T) {
	var existing map[string]any
	_ = json.Unmarshal([]byte(`{"log-driver":"json-file","registry-mirrors":["https://old.mirror","https://keep.mirror"],"insecure-registries":["old.mirror"]}`), &existing)
	c := Config{RegistryMirror: "http://nexus.corp:8082", RegistryInsecure: true}
	got := c.ApplyDaemonJSON(existing, "https://old.mirror")
	b, _ := json.Marshal(got)
	s := string(b)
	if !strings.Contains(s, `"log-driver":"json-file"`) || !strings.Contains(s, `"registry-mirrors":["http://nexus.corp:8082","https://keep.mirror"]`) ||
		!strings.Contains(s, `"insecure-registries":["nexus.corp:8082"]`) {
		t.Fatalf("merged: %s", s)
	}
	// Removing the mirror only removes PortDock's entries.
	got = Config{}.ApplyDaemonJSON(got, "http://nexus.corp:8082")
	b, _ = json.Marshal(got)
	if s := string(b); s != `{"insecure-registries":[],"log-driver":"json-file","registry-mirrors":["https://keep.mirror"]}` {
		t.Fatalf("removed: %s", s)
	}
	// Without anything PortDock set, the file is left alone.
	if got := (Config{}).ApplyDaemonJSON(map[string]any{"debug": true}, ""); len(got) != 1 {
		t.Fatalf("untouched: %v", got)
	}
}

// A forward proxy that answers every request itself and records them.
type recordingProxy struct {
	mu   sync.Mutex
	seen []string
	auth []string
}

func (p *recordingProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.seen = append(p.seen, r.Method+" "+r.URL.String())
	p.auth = append(p.auth, r.Header.Get("Proxy-Authorization"))
	p.mu.Unlock()
	_, _ = io.WriteString(w, "via proxy")
}

func TestPortDockRequestsUseTheProxy(t *testing.T) {
	rec := &recordingProxy{}
	proxy := httptest.NewServer(rec)
	defer proxy.Close()
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "direct") }))
	defer direct.Close()

	Install()
	defer SetCurrent(Config{})
	get := func(u string) string {
		t.Helper()
		res, err := http.Get(u)
		if err != nil {
			t.Fatalf("GET %s: %v", u, err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}

	// Configured but not for PortDock: requests go direct.
	SetCurrent(Config{HTTPProxy: proxy.URL, PortDock: false})
	if got := get(direct.URL); got != "direct" {
		t.Fatalf("proxy off: %s", got)
	}
	SetCurrent(Config{HTTPProxy: proxy.URL, PortDock: true, NoProxy: ".corp", Username: "svc", Password: "secret"})
	if got := get("http://updates.example.test/latest"); got != "via proxy" {
		t.Fatalf("through the proxy: %s", got)
	}
	// NO_PROXY and loopback addresses are reached directly.
	if got := get(direct.URL); got != "direct" {
		t.Fatalf("loopback must be direct: %s", got)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.seen) != 1 || rec.seen[0] != "GET http://updates.example.test/latest" || !strings.HasPrefix(rec.auth[0], "Basic ") {
		t.Fatalf("proxy saw %v %v", rec.seen, rec.auth)
	}
}
