package api

import (
	"strings"
	"testing"
)

// Readers of a container's inspect document never see values that can hold
// secrets: environment, command, arguments, health check.
func TestStripEnvHidesSecrets(t *testing.T) {
	raw := `[{"Id":"abc","Path":"redis-server","Args":["--requirepass","s3cret"],
		"State":{"Status":"running","Health":{"Log":[{"Output":"s3cret"}]}},
		"Config":{"Image":"redis:7","Env":["REDIS_PASSWORD=s3cret","TZ=UTC"],"Cmd":["redis-server","--requirepass","s3cret"],
		"Healthcheck":{"Test":["CMD-SHELL","redis-cli -a s3cret ping"]}}}]`
	out := string(stripEnv([]byte(raw)))
	if strings.Contains(out, "s3cret") {
		t.Fatalf("secret left in %s", out)
	}
	for _, keep := range []string{`"Image":"redis:7"`, `"REDIS_PASSWORD=********"`, `"Status":"running"`} {
		if !strings.Contains(out, keep) {
			t.Errorf("missing %s in %s", keep, out)
		}
	}
}
