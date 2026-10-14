package deploy

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/portdock/portdock/internal/executor"
)

// TestStripSubmoduleCredentialsE2E runs the cleanup in the git image used for checkouts:
// PORTDOCK_DOCKER_E2E=1 go test ./internal/deploy -run StripSubmoduleCredentialsE2E
func TestStripSubmoduleCredentialsE2E(t *testing.T) {
	if os.Getenv("PORTDOCK_DOCKER_E2E") != "1" {
		t.Skip("set PORTDOCK_DOCKER_E2E=1 to run git in Docker")
	}
	os.Setenv("MSYS_NO_PATHCONV", "1")
	const url = "https://pat:SECRET@dev.azure.com/o/p/_git/lib"
	script := "set -e; git config --global --add safe.directory '*'; git config --global user.email a@b; git config --global user.name a; " +
		"mkdir -p /lib && cd /lib && git init -q && git commit -q --allow-empty -m l; " +
		"mkdir -p /w/src && cd /w/src && git init -q && git -c protocol.file.allow=always submodule add -q /lib lib; " +
		"git config submodule.lib.url " + url + "; git -C lib remote set-url origin " + url + "; " +
		stripSubmoduleCredentials + "; cat .git/config .git/modules/lib/config"
	out, err := executor.Output(context.Background(), executor.Local{}, "docker run --rm --entrypoint sh "+gitImage+" -c "+q(script))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "SECRET") || strings.Count(out, "https://dev.azure.com/o/p/_git/lib") != 2 {
		t.Fatalf("credentials left:\n%s", out)
	}
}
