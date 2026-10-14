package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// PortDock release images are signed with Sigstore keyless signing by the
// release workflow of their repository (GitHub OIDC). Before an update, the
// updater checks that signature with cosign, then runs the image by the digest
// that was verified, so a tag moved after the check cannot be swapped in.

// DefaultCosignImage runs the verification (Settings → Updates can point to a mirror).
const DefaultCosignImage = "ghcr.io/sigstore/cosign/cosign:v2.4.1"

// GitHubIssuer is the OIDC issuer of GitHub Actions.
const GitHubIssuer = "https://token.actions.githubusercontent.com"

// Signature modes (setting update_verify).
const (
	VerifyRequired = "required" // default: an unsigned image is not installed
	VerifyWarn     = "warn"     // install anyway and say so (air-gapped networks)
	VerifyOff      = "off"
)

// SignaturePolicy says who must have signed an image.
type SignaturePolicy struct {
	Identity string // regular expression for the certificate subject
	Issuer   string
}

// ReleasePolicy accepts the release workflow of repo, run for a version tag.
func ReleasePolicy(repo string) SignaturePolicy {
	return SignaturePolicy{
		Identity: `(?i)^https://github\.com/` + regexp.QuoteMeta(repo) + `/\.github/workflows/release\.ya?ml@refs/tags/v[0-9]`,
		Issuer:   GitHubIssuer,
	}
}

// Verified describes a valid signature.
type Verified struct {
	Digest  string // sha256:…
	Subject string // the workflow that signed it
}

// Pinned returns image (repository[:tag]) pinned to the verified digest.
func (v Verified) Pinned(image string) string {
	repo := image
	if i := strings.LastIndex(image, "@"); i >= 0 {
		repo = image[:i]
	} else if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		repo = image[:i]
	}
	return repo + "@" + v.Digest
}

// Runner runs a shell command and returns its standard output.
type Runner func(ctx context.Context, cmd string) (string, error)

var digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// VerifySignature checks the keyless signature of image against policy by
// running cosign in a container.
func VerifySignature(ctx context.Context, run Runner, cosignImage, image string, p SignaturePolicy) (Verified, error) {
	if cosignImage == "" {
		cosignImage = DefaultCosignImage
	}
	cmd := fmt.Sprintf("docker run --rm %s verify --certificate-identity-regexp %s --certificate-oidc-issuer %s --output json %s",
		shellQuote(cosignImage), shellQuote(p.Identity), shellQuote(p.Issuer), shellQuote(image))
	out, err := run(ctx, cmd)
	if err != nil {
		return Verified{}, fmt.Errorf("the signature of %s could not be verified: %w", image, err)
	}
	var res []struct {
		Critical struct {
			Image struct {
				Digest string `json:"docker-manifest-digest"`
			} `json:"image"`
		} `json:"critical"`
		Optional map[string]any `json:"optional"`
	}
	// cosign may print notes before the JSON document.
	if i := strings.Index(out, "["); i > 0 {
		out = out[i:]
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res) == 0 {
		return Verified{}, errors.New("unexpected output from cosign verify")
	}
	v := Verified{Digest: res[0].Critical.Image.Digest}
	if s, ok := res[0].Optional["Subject"].(string); ok {
		v.Subject = s
	}
	if !digestRe.MatchString(v.Digest) {
		return Verified{}, fmt.Errorf("cosign reported an invalid digest %q", v.Digest)
	}
	return v, nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
