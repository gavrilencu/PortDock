package proxy

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// DynamicDir is where Traefik's file provider watches for routing files,
// relative to the PortDock data directory on each server.
const DynamicDir = "proxy/dynamic"

// DomainRoute is one host (plus optional path prefix) routed to a workload.
type DomainRoute struct {
	Host     string
	Path     string
	Port     int
	// Service routes to that Docker Compose service's containers (AppRoutes.Services)
	// instead of AppRoutes.Upstreams.
	Service  string
	HTTPS    bool
	WWW      string // none | to-www | to-apex
	Indexing bool
	// Cert selects the certificate source: "" (HTTP-01), "dns" (DNS-01) or "custom" (uploaded).
	Cert string
}

// AppRoutes describes every route of one workload. Upstreams are container
// names reachable on the proxy network, or ip:port of replicas on other
// servers (the private network); traffic only moves to a new version once its
// routes file lists the new containers.
type AppRoutes struct {
	Name      string
	Upstreams []string
	// HealthPath makes Traefik check the upstreams, so a server that is down
	// stops getting traffic (load balancing across servers).
	HealthPath string
	// Services holds the upstreams of each compose service, for domains with a Service.
	Services map[string][]string
	Domains  []DomainRoute
	// Protection applies to every domain of the workload.
	Protection Protection
	// Canary sends Percent of the traffic of the main service to new
	// containers while Upstreams (the current version) get the rest.
	Canary *Canary
}

// Canary is a new version receiving part of the traffic. Tag (the
// deployment) makes the names of its services unique, so Traefik's request
// counters of one canary never mix with an earlier one's.
type Canary struct {
	Upstreams []string
	Percent   int
	Tag       string
}

// CanaryService names the services of a canary split: the weighted service
// routers point to, and its stable and canary halves (Traefik metrics are
// labelled with these names, suffixed with @file).
func CanaryService(name string, port int, tag string) (weighted, stable, canary string) {
	weighted = SafeName(name) + "-p" + strconv.Itoa(port)
	return weighted, weighted + "-stable-" + tag, weighted + "-canary-" + tag
}

// Protection restricts who reaches a workload, in front of it in the proxy.
type Protection struct {
	// IPAllow lists the addresses or CIDR ranges allowed (empty: everyone).
	IPAllow []string
	// IPDepth reads the client address from X-Forwarded-For, this many proxies
	// (a CDN, a load balancer) back; 0 uses the connection's address.
	IPDepth int
	// RateAverage requests per second per client address, with bursts of RateBurst (0: no limit).
	RateAverage int
	RateBurst   int
	// BasicUsers are "user:bcrypt-hash" entries for a password prompt.
	BasicUsers []string
	// ForwardAuth is the address PortDock answers single sign-on checks on.
	ForwardAuth string
}

// AuthResponseHeaders carry the signed-in user to the application.
var AuthResponseHeaders = []string{"X-Forwarded-User", "X-Forwarded-Email", "X-Forwarded-Name"}

// middlewares renders the protection of a workload named n, in the order the
// proxy applies them: address, rate, then identity.
func (p Protection) middlewares(n string) (map[string]any, []string) {
	mws := map[string]any{}
	var chain []string
	add := func(name string, cfg any) {
		mws[n+"-"+name] = cfg
		chain = append(chain, n+"-"+name)
	}
	strategy := func() map[string]any {
		if p.IPDepth > 0 {
			return map[string]any{"ipStrategy": map[string]any{"depth": p.IPDepth}}
		}
		return map[string]any{}
	}
	if len(p.IPAllow) > 0 {
		cfg := strategy()
		cfg["sourceRange"] = p.IPAllow
		add("ipallow", map[string]any{"ipAllowList": cfg})
	}
	if p.RateAverage > 0 {
		cfg := map[string]any{"average": p.RateAverage, "burst": max(p.RateBurst, p.RateAverage), "period": "1s"}
		// Traefik rejects the whole file for an empty sourceCriterion; without
		// one it limits by the connection's address.
		if p.IPDepth > 0 {
			cfg["sourceCriterion"] = strategy()
		}
		add("ratelimit", map[string]any{"rateLimit": cfg})
	}
	switch {
	case p.ForwardAuth != "":
		add("sso", map[string]any{"forwardAuth": map[string]any{"address": p.ForwardAuth, "authResponseHeaders": AuthResponseHeaders}})
	case len(p.BasicUsers) > 0:
		add("auth", map[string]any{"basicAuth": map[string]any{"users": p.BasicUsers, "removeHeader": true}})
	}
	return mws, chain
}

func (r AppRoutes) upstreams(d DomainRoute) []string {
	if d.Service != "" {
		if ups, ok := r.Services[d.Service]; ok {
			return ups
		}
	}
	return r.Upstreams
}

var hostRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9-]{2,63}$|^localhost$|^[a-z0-9-]+\.localhost$`)

// ValidHost reports whether h is a hostname Traefik can route.
func ValidHost(h string) bool { return len(h) <= 253 && hostRe.MatchString(h) }

func apex(host string) string { return strings.TrimPrefix(host, "www.") }

func hostsFor(d DomainRoute) []string {
	switch d.WWW {
	case "to-www", "to-apex":
		a := apex(d.Host)
		return []string{a, "www." + a}
	}
	return []string{d.Host}
}

func ruleFor(d DomainRoute) string {
	var hs []string
	for _, h := range hostsFor(d) {
		if strings.HasPrefix(h, "*.") {
			// Traefik v3 matches wildcards with HostRegexp (one label deep).
			hs = append(hs, "HostRegexp(`^[a-z0-9-]+\\."+regexp.QuoteMeta(strings.TrimPrefix(h, "*."))+"$`)")
			continue
		}
		hs = append(hs, "Host(`"+h+"`)")
	}
	r := strings.Join(hs, " || ")
	if len(hs) > 1 {
		r = "(" + r + ")"
	}
	if d.Path != "" && d.Path != "/" {
		r += " && PathPrefix(`" + d.Path + "`)"
	}
	return r
}

type router struct {
	Rule        string   `yaml:"rule"`
	EntryPoints []string `yaml:"entryPoints"`
	Service     string   `yaml:"service"`
	Middlewares []string `yaml:"middlewares,omitempty"`
	TLS         *tlsCfg  `yaml:"tls,omitempty"`
}

type tlsCfg struct {
	CertResolver string      `yaml:"certResolver,omitempty"`
	Domains      []tlsDomain `yaml:"domains,omitempty"`
}

type tlsDomain struct {
	Main string   `yaml:"main"`
	SANs []string `yaml:"sans,omitempty"`
}

// tlsFor picks the resolver for a route. Wildcards can only be issued over DNS-01
// and need explicit domains because HostRegexp rules carry no hostname.
func tlsFor(d DomainRoute) *tlsCfg {
	switch d.Cert {
	case "custom":
		return &tlsCfg{} // served from the uploaded certificates in the default store
	case "dns":
		t := &tlsCfg{CertResolver: DNSResolver}
		if strings.HasPrefix(d.Host, "*.") {
			t.Domains = []tlsDomain{{Main: d.Host}}
		}
		return t
	}
	return &tlsCfg{CertResolver: CertResolver}
}

type server struct {
	URL string `yaml:"url"`
}

type service struct {
	LoadBalancer struct {
		Servers     []server     `yaml:"servers"`
		HealthCheck *healthCheck `yaml:"healthCheck,omitempty"`
	} `yaml:"loadBalancer"`
}

type healthCheck struct {
	Path     string `yaml:"path"`
	Interval string `yaml:"interval"`
	Timeout  string `yaml:"timeout"`
}

// upstreamURL is the address of a container (name) or of a replica on
// another server (ip:port).
func upstreamURL(u string, port int) string {
	if strings.Contains(u, ":") {
		return "http://" + u
	}
	return fmt.Sprintf("http://%s:%d", u, port)
}

// RenderDynamic returns the Traefik file-provider YAML for r, or nil when
// there is nothing to route (no domains or no upstreams).
func RenderDynamic(r AppRoutes) []byte {
	if len(r.Domains) == 0 {
		return nil
	}
	n := SafeName(r.Name)
	routers := map[string]router{}
	services := map[string]any{}
	mws := map[string]any{
		n + "-https":   map[string]any{"redirectScheme": map[string]any{"scheme": "https", "permanent": true}},
		n + "-gzip":    map[string]any{"compress": map[string]any{}},
		n + "-noindex": map[string]any{"headers": map[string]any{"customResponseHeaders": map[string]string{"X-Robots-Tag": "noindex, nofollow"}}},
	}
	protMws, protChain := r.Protection.middlewares(n)
	for k, v := range protMws {
		mws[k] = v
	}
	for i, d := range r.Domains {
		ups := append([]string{}, r.upstreams(d)...)
		if len(ups) == 0 {
			continue // nothing running to route to yet
		}
		sort.Strings(ups)
		svc := n + "-p" + strconv.Itoa(d.Port)
		if d.Service != "" {
			svc = n + "-" + SafeName(d.Service) + "-p" + strconv.Itoa(d.Port)
		}
		if _, ok := services[svc]; !ok {
			lb := func(names []string) service {
				var s service
				remote := false
				for _, u := range names {
					s.LoadBalancer.Servers = append(s.LoadBalancer.Servers, server{URL: upstreamURL(u, d.Port)})
					remote = remote || strings.Contains(u, ":")
				}
				if remote && r.HealthPath != "" && d.Service == "" {
					s.LoadBalancer.HealthCheck = &healthCheck{Path: r.HealthPath, Interval: "10s", Timeout: "3s"}
				}
				return s
			}
			if c := r.Canary; c != nil && d.Service == "" && len(c.Upstreams) > 0 && c.Percent > 0 && c.Percent < 100 {
				canary := append([]string{}, c.Upstreams...)
				sort.Strings(canary)
				_, stable, canaryName := CanaryService(r.Name, d.Port, c.Tag)
				services[stable], services[canaryName] = lb(ups), lb(canary)
				services[svc] = map[string]any{"weighted": map[string]any{"services": []map[string]any{
					{"name": stable, "weight": 100 - c.Percent},
					{"name": canaryName, "weight": c.Percent},
				}}}
			} else {
				services[svc] = lb(ups)
			}
		}
		chain := append([]string{}, protChain...)
		if d.WWW == "to-www" || d.WWW == "to-apex" {
			a := regexp.QuoteMeta(apex(d.Host))
			from, to := `^(https?)://`+a+`(.*)$`, "${1}://www."+apex(d.Host)+"${2}"
			if d.WWW == "to-apex" {
				from, to = `^(https?)://www\.`+a+`(.*)$`, "${1}://"+apex(d.Host)+"${2}"
			}
			name := fmt.Sprintf("%s-d%d-www", n, i)
			mws[name] = map[string]any{"redirectRegex": map[string]any{"regex": from, "replacement": to, "permanent": true}}
			chain = append(chain, name)
		}
		if !d.Indexing {
			chain = append(chain, n+"-noindex")
		}
		chain = append(chain, n+"-gzip")
		base := fmt.Sprintf("%s-d%d", n, i)
		rule := ruleFor(d)
		if d.HTTPS {
			routers[base+"-http"] = router{Rule: rule, EntryPoints: []string{"web"}, Service: svc, Middlewares: []string{n + "-https"}}
			routers[base+"-https"] = router{Rule: rule, EntryPoints: []string{"websecure"}, Service: svc, Middlewares: chain, TLS: tlsFor(d)}
		} else {
			routers[base+"-http"] = router{Rule: rule, EntryPoints: []string{"web"}, Service: svc, Middlewares: chain}
		}
	}
	if len(routers) == 0 {
		return nil
	}
	out, _ := yaml.Marshal(map[string]any{"http": map[string]any{"routers": routers, "services": services, "middlewares": mws}})
	return append([]byte("# Managed by PortDock. Manual edits are overwritten.\n"), out...)
}

// DashboardUpstream is the alias under which the PortDock container joins the
// proxy network when it serves its own dashboard domain.
const DashboardUpstream = "portdock-dashboard"

// RenderDashboard routes the instance domain to the PortDock container itself.
func RenderDashboard(host string, https bool, port int) []byte {
	return RenderDynamic(AppRoutes{
		Name:      "portdock-dashboard",
		Upstreams: []string{DashboardUpstream},
		Domains:   []DomainRoute{{Host: host, Port: port, HTTPS: https, Indexing: false}},
	})
}

// CustomCertFile is one uploaded certificate deployed to the proxy.
type CustomCertFile struct {
	CertFile, KeyFile string // paths inside the proxy container
}

// RenderCustomCerts lists uploaded certificates for Traefik's default store.
func RenderCustomCerts(files []CustomCertFile) []byte {
	if len(files) == 0 {
		return nil
	}
	certs := make([]map[string]string, 0, len(files))
	for _, f := range files {
		certs = append(certs, map[string]string{"certFile": f.CertFile, "keyFile": f.KeyFile})
	}
	out, _ := yaml.Marshal(map[string]any{"tls": map[string]any{"certificates": certs}})
	return append([]byte("# Managed by PortDock: uploaded certificates.\n"), out...)
}
