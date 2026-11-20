package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/docker"
)

// cert-manager issues the certificate of each HTTPS host of an Ingress into the
// Secret TLSSecretName(host), through a Certificate of the same name. These
// read why one is not issued yet, and ask for a new one.

func notFound(err error) bool {
	s := err.Error()
	return strings.Contains(s, "NotFound") || strings.Contains(s, "not found")
}

// CertificateProblem tells why cert-manager has not issued the certificate of
// host: the message of its Ready condition and, while Let's Encrypt checks the
// domain, the state of the ACME challenge. "" when it is issued, or unknown.
func (c *Cluster) CertificateProblem(ctx context.Context, host string) string {
	out, err := c.output(ctx, nil, "get", "certificate", TLSSecretName(host), "-o", "json", "--request-timeout=15s")
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "doesn't have a resource type"):
			return "cert-manager is not installed in the cluster: install it and set a ClusterIssuer in the server's settings."
		case notFound(err):
			return "cert-manager has no certificate for " + host + ": check that the server's certificate issuer exists in the cluster."
		}
		return ""
	}
	var cert struct {
		Status struct {
			Conditions []struct {
				Type    string `json:"type"`
				Status  string `json:"status"`
				Message string `json:"message"`
			} `json:"conditions"`
		} `json:"status"`
	}
	if json.Unmarshal([]byte(out), &cert) != nil {
		return ""
	}
	msg := ""
	for _, cond := range cert.Status.Conditions {
		if cond.Type == "Ready" {
			if cond.Status == "True" {
				return ""
			}
			msg = cond.Message
		}
	}
	if why := c.challengeState(ctx, host); why != "" {
		msg = strings.TrimSuffix(strings.TrimSpace(msg), ".") + ". Let's Encrypt check: " + why
	}
	if strings.TrimSpace(msg) == "" {
		return ""
	}
	return "cert-manager: " + strings.TrimSpace(msg)
}

// challengeState is the reason cert-manager gives for the ACME challenge of
// host, when one is in progress (wrong DNS, port 80 not reachable…).
func (c *Cluster) challengeState(ctx context.Context, host string) string {
	out, err := c.output(ctx, nil, "get", "challenges.acme.cert-manager.io", "-o", "json", "--request-timeout=15s")
	if err != nil {
		return ""
	}
	var list struct {
		Items []struct {
			Spec struct {
				DNSName string `json:"dnsName"`
			} `json:"spec"`
			Status struct {
				State  string `json:"state"`
				Reason string `json:"reason"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(out), &list) != nil {
		return ""
	}
	for _, it := range list.Items {
		if it.Spec.DNSName == host && (it.Status.Reason != "" || it.Status.State != "") {
			return strings.TrimSpace(it.Status.State + " " + it.Status.Reason)
		}
	}
	return ""
}

// RenewCertificate asks cert-manager to issue the certificate of host again,
// as cmctl renew does: the Issuing condition is added to its Certificate.
func (c *Cluster) RenewCertificate(ctx context.Context, host string) error {
	name := TLSSecretName(host)
	out, err := c.output(ctx, nil, "get", "certificate", name, "-o", "json", "--request-timeout=15s")
	if err != nil {
		if notFound(err) || strings.Contains(err.Error(), "doesn't have a resource type") {
			return fmt.Errorf("cert-manager has no certificate for %s: set a cert-manager ClusterIssuer in the server's settings", host)
		}
		return err
	}
	var cert struct {
		Status struct {
			Conditions []map[string]any `json:"conditions"`
		} `json:"status"`
	}
	_ = json.Unmarshal([]byte(out), &cert)
	conditions := []map[string]any{}
	for _, cond := range cert.Status.Conditions {
		if cond["type"] != "Issuing" {
			conditions = append(conditions, cond)
		}
	}
	conditions = append(conditions, map[string]any{
		"type": "Issuing", "status": "True", "reason": "ManuallyTriggered",
		"message": "Certificate re-issuance manually triggered by PortDock", "lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
	})
	patch, _ := json.Marshal(map[string]any{"status": map[string]any{"conditions": conditions}})
	if _, err := c.output(ctx, nil, "patch", "certificate", name, "--subresource=status", "--type=merge", "-p", string(patch)); err != nil {
		return fmt.Errorf("ask cert-manager for a new certificate of %s: %w", host, err)
	}
	return nil
}

// HTTPSRedirectName is the Traefik middleware of a namespace that sends HTTP
// requests to HTTPS.
const HTTPSRedirectName = "pd-https-redirect"

// EnsureHTTPSRedirect creates the namespace's Traefik middleware that sends
// HTTP requests to HTTPS, and returns how an Ingress refers to it.
func (c *Cluster) EnsureHTTPSRedirect(ctx context.Context) (string, error) {
	mw := Object{
		"apiVersion": "traefik.io/v1alpha1",
		"kind":       "Middleware",
		"metadata":   meta(HTTPSRedirectName, map[string]string{docker.LabelManaged: "true"}),
		"spec":       Object{"redirectScheme": Object{"scheme": "https", "permanent": true}},
	}
	if err := c.Apply(ctx, mw); err != nil {
		return "", fmt.Errorf("the Traefik middleware could not be created (%v); give PortDock's service account the right on middlewares.traefik.io in this namespace, e.g. by running the Kubernetes installer again", err)
	}
	return c.Namespace + "-" + HTTPSRedirectName + "@kubernetescrd", nil
}
