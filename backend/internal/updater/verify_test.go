package updater

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestReleasePolicy(t *testing.T) {
	re := regexp.MustCompile(ReleasePolicy("gavrilencu/PortDock").Identity)
	for subject, want := range map[string]bool{
		"https://github.com/gavrilencu/PortDock/.github/workflows/release.yml@refs/tags/v1.14.0":                 true,
		"https://github.com/gavrilencu/portdock/.github/workflows/release.yml@refs/tags/v1.14.0-rc1":             true,
		"https://github.com/gavrilencu/PortDock/.github/workflows/release.yml@refs/heads/main":                   false,
		"https://github.com/gavrilencu/PortDock/.github/workflows/ci.yml@refs/tags/v1.14.0":                      false,
		"https://github.com/gavrilencu/PortDock-fork/.github/workflows/release.yml@refs/tags/v1.0":               false,
		"https://github.com/mallory/PortDock/.github/workflows/release.yml@refs/tags/v1.14.0":                    false,
		"https://evil.example/https://github.com/gavrilencu/PortDock/.github/workflows/release.yml@refs/tags/v1": false,
	} {
		if re.MatchString(subject) != want {
			t.Errorf("%s: got %v", subject, !want)
		}
	}
}

func TestVerifySignature(t *testing.T) {
	const digest = "sha256:65da32389e894597467b15e43c80a793175724e7f5894c8f8c5d6b6aa3624bb4"
	var got string
	run := func(out string, err error) Runner {
		return func(_ context.Context, cmd string) (string, error) { got = cmd; return out, err }
	}
	ok := `Verification for ghcr.io/acme/portdock:v1.14.0 --
[{"critical":{"image":{"docker-manifest-digest":"` + digest + `"}},"optional":{"Subject":"https://github.com/acme/portdock/.github/workflows/release.yml@refs/tags/v1.14.0"}}]`
	v, err := VerifySignature(context.Background(), run(ok, nil), "", "ghcr.io/acme/portdock:v1.14.0", ReleasePolicy("acme/portdock"))
	if err != nil {
		t.Fatal(err)
	}
	if v.Digest != digest || !strings.Contains(v.Subject, "release.yml") {
		t.Fatalf("verified: %+v", v)
	}
	if p := v.Pinned("ghcr.io/acme/portdock:v1.14.0"); p != "ghcr.io/acme/portdock@"+digest {
		t.Fatalf("pinned: %s", p)
	}
	if p := v.Pinned("registry.local:5000/portdock:v1"); p != "registry.local:5000/portdock@"+digest {
		t.Fatalf("pinned with a registry port: %s", p)
	}
	if !strings.HasPrefix(got, "docker run --rm '"+DefaultCosignImage+"' verify --certificate-identity-regexp '(?i)^https://github") ||
		!strings.Contains(got, "--certificate-oidc-issuer '"+GitHubIssuer+"'") || !strings.HasSuffix(got, "'ghcr.io/acme/portdock:v1.14.0'") {
		t.Fatalf("command: %s", got)
	}
	if _, err := VerifySignature(context.Background(), run("", errors.New("no matching signatures")), "", "x:1", ReleasePolicy("a/b")); err == nil {
		t.Fatal("failure accepted")
	}
	if _, err := VerifySignature(context.Background(), run(`[{"critical":{"image":{"docker-manifest-digest":"sha256:../../x"}}}]`, nil), "", "x:1", ReleasePolicy("a/b")); err == nil {
		t.Fatal("invalid digest accepted")
	}
	if _, err := VerifySignature(context.Background(), run("not json", nil), "", "x:1", ReleasePolicy("a/b")); err == nil {
		t.Fatal("garbage accepted")
	}
}

// TestVerifySignatureE2E runs cosign for real against public images:
// PORTDOCK_DOCKER_E2E=1 go test ./internal/updater -run VerifySignatureE2E
func TestVerifySignatureE2E(t *testing.T) {
	if os.Getenv("PORTDOCK_DOCKER_E2E") != "1" {
		t.Skip("set PORTDOCK_DOCKER_E2E=1 to verify signatures with cosign in Docker")
	}
	os.Setenv("MSYS_NO_PATHCONV", "1")
	run := func(ctx context.Context, cmd string) (string, error) {
		out, err := exec.CommandContext(ctx, "sh", "-c", cmd).Output()
		return string(out), err
	}
	ctx := context.Background()
	// Signed by its own GitHub release workflow (keyless, like PortDock's images).
	signed := "ghcr.io/external-secrets/external-secrets:v0.10.0"
	theirs := SignaturePolicy{Identity: `^https://github\.com/external-secrets/external-secrets/\.github/workflows/release\.yml@`, Issuer: GitHubIssuer}
	v, err := VerifySignature(ctx, run, "", signed, theirs)
	if err != nil {
		t.Fatalf("signed image: %v", err)
	}
	t.Logf("verified %s: %s", v.Pinned(signed), v.Subject)
	// The same image is refused when PortDock's release workflow must have signed it.
	if _, err := VerifySignature(ctx, run, "", signed, ReleasePolicy("gavrilencu/PortDock")); err == nil {
		t.Fatal("image signed by another repository accepted")
	}
	// An unsigned image is refused.
	if _, err := VerifySignature(ctx, run, "", "docker.io/library/alpine:3", ReleasePolicy("gavrilencu/PortDock")); err == nil {
		t.Fatal("unsigned image accepted")
	}
}
