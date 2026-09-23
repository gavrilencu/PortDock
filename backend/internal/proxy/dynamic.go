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
	HTTPS    bool
	WWW      string // none | to-www | to-apex
	Indexing bool
	// Cert selects the certificate source: "" (HTTP-01), "dns" (DNS-01) or "custom" (uploaded).
	Cert string
}

// AppRoutes describes every route of one workload. Upstreams are container
// names reachable on the proxy network; traffic only moves to a new version
// once its routes file lists the new containers.
type AppRoutes struct {
	Name      string
	Upstreams []string
	Domains   []DomainRoute
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
		Servers []server `yaml:"servers"`
	} `yaml:"loadBalancer"`
}

// RenderDynamic returns the Traefik file-provider YAML for r, or nil when
// there is nothing to route (no domains or no upstreams).
func RenderDynamic(r AppRoutes) []byte {
	if len(r.Domains) == 0 || len(r.Upstreams) == 0 {
		return nil
	}
	n := SafeName(r.Name)
	routers := map[string]router{}
	services := map[string]service{}
	mws := map[string]any{
		n + "-https":   map[string]any{"redirectScheme": map[string]any{"scheme": "https", "permanent": true}},
		n + "-gzip":    map[string]any{"compress": map[string]any{}},
		n + "-noindex": map[string]any{"headers": map[string]any{"customResponseHeaders": map[string]string{"X-Robots-Tag": "noindex, nofollow"}}},
	}
	ups := append([]string{}, r.Upstreams...)
	sort.Strings(ups)
	for i, d := range r.Domains {
		svc := n + "-p" + strconv.Itoa(d.Port)
		if _, ok := services[svc]; !ok {
			var s service
			for _, u := range ups {
				s.LoadBalancer.Servers = append(s.LoadBalancer.Servers, server{URL: fmt.Sprintf("http://%s:%d", u, d.Port)})
			}
			services[svc] = s
		}
		var chain []string
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
