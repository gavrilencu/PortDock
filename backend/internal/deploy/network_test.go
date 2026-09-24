package deploy

import (
	"strings"
	"testing"

	"github.com/portdock/portdock/internal/netcfg"
)

func TestContainerProxyEnv(t *testing.T) {
	on := netcfg.Config{HTTPProxy: "http://proxy:3128", NoProxy: "localhost", Containers: true}
	off := netcfg.Config{HTTPProxy: "http://proxy:3128", NoProxy: "localhost"}
	none := netcfg.Config{}
	cases := []struct {
		net  netcfg.Config
		mode string
		want bool
	}{
		{on, "", true}, {on, "off", false}, {on, "on", true},
		{off, "", false}, {off, "on", true}, {off, "off", false},
		{none, "on", false},
	}
	for _, c := range cases {
		env := containerProxyEnv(c.net, c.mode)
		if (env != nil) != c.want {
			t.Errorf("containers=%v mode=%q: %v", c.net.Containers, c.mode, env)
		}
		if env != nil && (env["HTTP_PROXY"] != "http://proxy:3128" || env["no_proxy"] != "localhost") {
			t.Errorf("env: %v", env)
		}
	}
	if buildProxyEnv(off) != nil || buildProxyEnv(netcfg.Config{HTTPProxy: "http://p:1", Builds: true})["HTTPS_PROXY"] != "http://p:1" {
		t.Error("build proxy")
	}
	f := envFlags(map[string]string{"B": "2", "A": "x y"})
	if f != " -e 'A=x y' -e B=2" {
		t.Errorf("flags: %q", f)
	}
	if a := proxyArgs("--build-arg", map[string]string{"HTTP_PROXY": "http://p:1"}); !strings.Contains(a, "--build-arg HTTP_PROXY=http://p:1") {
		t.Errorf("args: %q", a)
	}
	cmd := trivyCmd("trivy", "img", false, netcfg.Config{HTTPProxy: "http://p:1", Builds: true, TrivyDB: "nexus.corp/trivy-db"})
	if !strings.Contains(cmd, "-e HTTPS_PROXY=http://p:1") || !strings.Contains(cmd, "--db-repository nexus.corp/trivy-db") {
		t.Errorf("trivy: %s", cmd)
	}
}
