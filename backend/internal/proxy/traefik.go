// Package proxy manages the Traefik reverse proxy on each server and generates
// routing labels (domains + automatic Let's Encrypt certificates) for workloads.
package proxy

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/shipyard/shipyard/internal/docker"
	"github.com/shipyard/shipyard/internal/executor"
)

const (
	ContainerName = "shipyard-proxy"
	// Image must speak the Docker Engine API of current hosts (Docker 29 dropped
	// API < 1.44, which broke Traefik <= 3.5's docker provider).
	Image        = "traefik:v3.7"
	CertResolver = "letsencrypt"
)

var q = executor.Quote

type Options struct {
	Network   string
	DataDir   string // on the target server
	ACMEEmail string
	Dashboard bool
}

// Ensure starts (or recreates) the proxy container on the server.
func Ensure(ctx context.Context, e executor.Executor, o Options, recreate bool) error {
	if err := docker.EnsureNetwork(ctx, e, o.Network); err != nil {
		return err
	}
	if !recreate {
		if state, _, err := docker.State(ctx, e, ContainerName); err == nil && state == "running" {
			// Upgrade proxies created by an older Shipyard release automatically.
			img, _ := executor.Output(ctx, e, "docker inspect --format '{{.Config.Image}}' "+ContainerName)
			if img == Image {
				return nil
			}
		}
	}
	certDir := o.DataDir + "/proxy"
	email := o.ACMEEmail
	if email == "" {
		email = "admin@example.com"
	}
	args := []string{
		"--providers.docker=true",
		"--providers.docker.exposedbydefault=false",
		"--providers.docker.network=" + o.Network,
		"--providers.file.directory=/etc/traefik/dynamic",
		"--providers.file.watch=true",
		"--entrypoints.web.address=:80",
		"--entrypoints.websecure.address=:443",
		"--entrypoints.websecure.http3=true",
		"--entrypoints.websecure.http.tls=true",
		"--certificatesresolvers." + CertResolver + ".acme.email=" + email,
		"--certificatesresolvers." + CertResolver + ".acme.storage=/certs/acme.json",
		"--certificatesresolvers." + CertResolver + ".acme.httpchallenge=true",
		"--certificatesresolvers." + CertResolver + ".acme.httpchallenge.entrypoint=web",
		"--ping=true",
		"--accesslog=false",
		"--log.level=INFO",
	}
	if o.Dashboard {
		args = append(args, "--api.dashboard=true", "--api.insecure=true")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "mkdir -p %s/dynamic && touch %s/acme.json && chmod 600 %s/acme.json && ", q(certDir), q(certDir), q(certDir))
	fmt.Fprintf(&b, "(docker rm -f %s >/dev/null 2>&1 || true) && ", ContainerName)
	fmt.Fprintf(&b, "docker run -d --name %s --restart unless-stopped --network %s ", ContainerName, q(o.Network))
	b.WriteString("-p 80:80 -p 443:443 -p 443:443/udp ")
	if o.Dashboard {
		b.WriteString("-p 127.0.0.1:8080:8080 ")
	}
	b.WriteString("-v /var/run/docker.sock:/var/run/docker.sock:ro ")
	fmt.Fprintf(&b, "-v %s:/certs -v %s/dynamic:/etc/traefik/dynamic ", q(certDir), q(certDir))
	fmt.Fprintf(&b, "--label %s=true --label %s=proxy ", docker.LabelManaged, docker.LabelKind)
	b.WriteString(Image)
	for _, a := range args {
		b.WriteString(" " + q(a))
	}
	_, err := executor.Output(ctx, e, b.String())
	return err
}

func Stop(ctx context.Context, e executor.Executor) error {
	_, err := executor.Output(ctx, e, "docker rm -f "+ContainerName)
	return err
}

// Status returns the proxy container state or "missing".
func Status(ctx context.Context, e executor.Executor) string {
	state, _, err := docker.State(ctx, e, ContainerName)
	if err != nil {
		return "missing"
	}
	return state
}

type Route struct {
	Name    string   // unique router/service name
	Domains []string // hostnames (scheme/path allowed, see NormalizeDomain)
	Port    int
	HTTPS   bool
	Network string
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9-]+`)

// SafeName makes a Traefik-compatible identifier.
func SafeName(s string) string {
	return strings.Trim(nonAlnum.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// NormalizeDomain strips scheme and path and returns host plus path prefix.
func NormalizeDomain(d string) (host, path string) {
	d = strings.TrimSpace(d)
	d = strings.TrimPrefix(strings.TrimPrefix(d, "https://"), "http://")
	host, path, _ = strings.Cut(d, "/")
	if path != "" {
		path = "/" + strings.TrimSuffix(path, "/")
	}
	return strings.ToLower(host), path
}

func rule(domains []string) string {
	var parts []string
	for _, d := range domains {
		host, path := NormalizeDomain(d)
		if host == "" {
			continue
		}
		r := "Host(`" + host + "`)"
		if path != "" {
			r = "(" + r + " && PathPrefix(`" + path + "`))"
		}
		parts = append(parts, r)
	}
	return strings.Join(parts, " || ")
}

// Labels returns Docker labels that register the route in Traefik.
func Labels(r Route) map[string]string {
	if len(r.Domains) == 0 || r.Port == 0 {
		return map[string]string{}
	}
	n := SafeName(r.Name)
	ru := rule(r.Domains)
	l := map[string]string{
		"traefik.enable":         "true",
		"traefik.docker.network": r.Network,
		"traefik.http.services." + n + ".loadbalancer.server.port": strconv.Itoa(r.Port),
		"traefik.http.routers." + n + "-http.rule":                 ru,
		"traefik.http.routers." + n + "-http.entrypoints":          "web",
		"traefik.http.routers." + n + "-http.service":              n,
		"traefik.http.middlewares." + n + "-gzip.compress":         "true",
	}
	if r.HTTPS {
		l["traefik.http.middlewares."+n+"-redirect.redirectscheme.scheme"] = "https"
		l["traefik.http.middlewares."+n+"-redirect.redirectscheme.permanent"] = "true"
		l["traefik.http.routers."+n+"-http.middlewares"] = n + "-redirect"
		l["traefik.http.routers."+n+"-https.rule"] = ru
		l["traefik.http.routers."+n+"-https.entrypoints"] = "websecure"
		l["traefik.http.routers."+n+"-https.tls"] = "true"
		l["traefik.http.routers."+n+"-https.tls.certresolver"] = CertResolver
		l["traefik.http.routers."+n+"-https.service"] = n
		l["traefik.http.routers."+n+"-https.middlewares"] = n + "-gzip"
	} else {
		l["traefik.http.routers."+n+"-http.middlewares"] = n + "-gzip"
	}
	return l
}

// LabelArgs renders labels as `--label k=v` docker CLI arguments.
func LabelArgs(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(" --label " + q(k+"="+labels[k]))
	}
	return b.String()
}
