package kube

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// scripted answers kubectl calls: the key is the command after the namespace.
type scripted struct {
	answers map[string]string // command prefix → stdout ("!" prefix: an error)
	calls   []string
	stdin   []string
}

func (s *scripted) run(_ context.Context, in io.Reader, out, errw io.Writer, args ...string) error {
	if len(args) >= 2 && args[0] == "--namespace" {
		args = args[2:]
	}
	cmd := strings.Join(args, " ")
	s.calls = append(s.calls, cmd)
	if in != nil {
		b, _ := io.ReadAll(in)
		s.stdin = append(s.stdin, string(b))
	}
	for prefix, answer := range s.answers {
		if strings.HasPrefix(cmd, prefix) {
			if strings.HasPrefix(answer, "!") {
				_, _ = io.WriteString(errw, answer[1:])
				return errors.New("exit status 1")
			}
			_, _ = io.WriteString(out, answer)
			return nil
		}
	}
	return nil
}

func TestCertificateProblem(t *testing.T) {
	ctx := context.Background()
	host := "shop.example.com"
	name := TLSSecretName(host)
	s := &scripted{answers: map[string]string{
		"get certificate " + name: `{"status":{"conditions":[{"type":"Ready","status":"False","message":"Issuing certificate as Secret does not exist."}]}}`,
		"get challenges":          `{"items":[{"spec":{"dnsName":"other.example.com"},"status":{"reason":"ignored"}},{"spec":{"dnsName":"shop.example.com"},"status":{"state":"pending","reason":"Waiting for HTTP-01 challenge propagation: wrong status code '404', expected '200'"}}]}`,
	}}
	c := NewWithRunner("apps", s.run)
	got := c.CertificateProblem(ctx, host)
	for _, want := range []string{"cert-manager: Issuing certificate as Secret does not exist", "Let's Encrypt check: pending Waiting for HTTP-01", "'404'"} {
		if !strings.Contains(got, want) {
			t.Errorf("problem %q should contain %q", got, want)
		}
	}

	s.answers["get certificate "+name] = `{"status":{"conditions":[{"type":"Ready","status":"True"}]}}`
	if got := c.CertificateProblem(ctx, host); got != "" {
		t.Fatalf("an issued certificate has no problem: %q", got)
	}
	s.answers["get certificate "+name] = `!Error from server (NotFound): certificates.cert-manager.io "` + name + `" not found`
	if got := c.CertificateProblem(ctx, host); !strings.Contains(got, "no certificate for shop.example.com") {
		t.Fatalf("no Certificate: %q", got)
	}
	s.answers["get certificate "+name] = `!error: the server doesn't have a resource type "certificate"`
	if got := c.CertificateProblem(ctx, host); !strings.Contains(got, "not installed") {
		t.Fatalf("no cert-manager: %q", got)
	}
}

// A renewal adds the Issuing condition, keeping the others, like cmctl renew.
func TestRenewCertificate(t *testing.T) {
	host := "shop.example.com"
	name := TLSSecretName(host)
	s := &scripted{answers: map[string]string{
		"get certificate " + name: `{"status":{"conditions":[{"type":"Ready","status":"True","reason":"Ready"},{"type":"Issuing","status":"False"}]}}`,
	}}
	c := NewWithRunner("apps", s.run)
	if err := c.RenewCertificate(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	last := s.calls[len(s.calls)-1]
	if !strings.HasPrefix(last, "patch certificate "+name+" --subresource=status --type=merge -p ") {
		t.Fatalf("patch: %s", last)
	}
	var patch struct {
		Status struct {
			Conditions []map[string]any `json:"conditions"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(strings.SplitN(last, " -p ", 2)[1]), &patch); err != nil {
		t.Fatal(err)
	}
	conds := patch.Status.Conditions
	if len(conds) != 2 || conds[0]["type"] != "Ready" || conds[1]["type"] != "Issuing" || conds[1]["status"] != "True" || conds[1]["reason"] != "ManuallyTriggered" {
		t.Fatalf("conditions: %v", conds)
	}

	s.answers["get certificate "+name] = `!Error from server (NotFound): not found`
	if err := c.RenewCertificate(context.Background(), host); err == nil || !strings.Contains(err.Error(), "ClusterIssuer") {
		t.Fatalf("without a Certificate: %v", err)
	}
}

func TestEnsureHTTPSRedirect(t *testing.T) {
	s := &scripted{answers: map[string]string{}}
	c := NewWithRunner("apps", s.run)
	ref, err := c.EnsureHTTPSRedirect(context.Background())
	if err != nil || ref != "apps-pd-https-redirect@kubernetescrd" {
		t.Fatalf("ref %q, %v", ref, err)
	}
	if len(s.stdin) == 0 || !strings.Contains(s.stdin[len(s.stdin)-1], `"kind":"Middleware"`) || !strings.Contains(s.stdin[len(s.stdin)-1], `"redirectScheme":{"permanent":true,"scheme":"https"}`) {
		t.Fatalf("middleware applied: %v", s.stdin)
	}
	s.answers["apply"] = `!error: resource mapping not found for name: "pd-https-redirect": no matches for kind "Middleware" in version "traefik.io/v1alpha1"`
	if _, err := c.EnsureHTTPSRedirect(context.Background()); err == nil || !strings.Contains(err.Error(), "middlewares.traefik.io") {
		t.Fatalf("without the Traefik CRD: %v", err)
	}

	// The Ingress refers to it.
	ing := Ingress("pd-web", nil, []Route{{Host: "shop.example.com", Service: "pd-web", Port: 80, TLS: true}}, IngressOptions{Issuer: "letsencrypt", Middlewares: ref})
	ann := ing["metadata"].(Object)["annotations"].(map[string]string)
	if ann["traefik.ingress.kubernetes.io/router.middlewares"] != ref || ann["cert-manager.io/cluster-issuer"] != "letsencrypt" {
		t.Fatalf("annotations: %v", ann)
	}
}
