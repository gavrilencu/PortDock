package templates

import (
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Coolify's one-click services (github.com/coollabsio/coolify, templates/)
// are compose files with header comments and "magic" variables. Convert
// turns one into a PortDock template:
//
//	SERVICE_URL_<ID> / SERVICE_FQDN_<ID>[_<PORT>]   → ${PORTDOCK_URL} or ${PORTDOCK_FQDN}; a bare
//	                                                 declaration names the public service and port
//
// Coolify's catalog uses SERVICE_FQDN both for URLs and for host names, so
// the variable it is assigned to decides: a host (HOST, DOMAIN…) or a value
// with its own scheme (wss://${SERVICE_FQDN_X}) gets the host name, anything
// else the URL. References may be written ${SERVICE_…} or $SERVICE_….
//	SERVICE_USER_<ID>, SERVICE_LOWERCASEUSER_<ID>   → ${PORTDOCK_USER_<ID>}
//	SERVICE_PASSWORD[_64]_<ID> (also WITHSYMBOLS)   → ${PORTDOCK_PASSWORD[64]_<ID>}
//	SERVICE_BASE64[_64|_128]_<ID> (alphanumeric)    → ${PORTDOCK_RANDOM32|64|128_<ID>}
//	SERVICE_REALBASE64[_64|_128]_<ID>               → ${PORTDOCK_BASE64[x64|x128]_<ID>}
//	SERVICE_HEX_32|64|128_<ID>                      → ${PORTDOCK_HEX32|64|128_<ID>}
//
// Bind mounts next to the compose file become named volumes, files written
// from `content:` become configs, named volumes are declared (Coolify does it
// itself) and `exclude_from_hc` is dropped.

// CoolifyEntry is one service of Coolify's templates/service-templates.json.
type CoolifyEntry struct {
	Documentation string   `json:"documentation"`
	Slogan        string   `json:"slogan"`
	Compose       string   `json:"compose"` // base64
	Tags          []string `json:"tags"`
	Category      string   `json:"category"`
	Logo          string   `json:"logo"`
	Port          string   `json:"port"`
}

var (
	domainDecl = regexp.MustCompile(`^SERVICE_(FQDN|URL)_([A-Z0-9_]+?)(?:_(\d+))?$`)
	// ${NAME}, ${NAME:-default} (Coolify generates the value anyway) or $NAME.
	domainRefs = []*regexp.Regexp{
		regexp.MustCompile(`\$\{SERVICE_(FQDN|URL)_[A-Z0-9_]+(?::?[-?=][^}]*)?\}`),
		regexp.MustCompile(`\$SERVICE_(FQDN|URL)_[A-Z0-9_]+`),
	}
	credRefs = []*regexp.Regexp{
		regexp.MustCompile(`\$\{SERVICE_(USER|LOWERCASEUSER|PASSWORDWITHSYMBOLS|PASSWORD|REALBASE64|BASE64|HEX)(?:_(32|64|128))?_([A-Z0-9_]+?)(?::?[-?=][^}]*)?\}`),
		regexp.MustCompile(`\$SERVICE_(USER|LOWERCASEUSER|PASSWORDWITHSYMBOLS|PASSWORD|REALBASE64|BASE64|HEX)(?:_(32|64|128))?_([A-Z0-9_]+)`),
	}
	hostKey    = regexp.MustCompile(`HOST|DOMAIN|FQDN|SERVER_NAME|COOKIE`)
	headerLine = regexp.MustCompile(`^#\s*([a-z_]+)\s*:\s*(.*?)\s*$`)
	coolifyCat = map[string]string{
		"ai": "AI & Machine Learning", "analytics": "Analytics & BI", "automation": "Low-code & Automation", "cms": "CMS & Websites",
		"database": "Databases", "databases": "Databases", "development": "Developer Tools", "devtools": "Developer Tools", "documentation": "Collaboration & Productivity",
		"email": "Collaboration & Productivity", "finance": "Collaboration & Productivity", "media": "Media", "monitoring": "Monitoring & Observability",
		"productivity": "Collaboration & Productivity", "security": "Security & Identity", "auth": "Security & Identity", "storage": "Storage & Files",
		"communication": "Collaboration & Productivity", "messaging": "Messaging & Streaming", "search": "Search & Vector", "cicd": "DevOps & CI/CD",
		"git": "DevOps & CI/CD", "dashboard": "Dashboards & Home", "home": "Dashboards & Home", "proxy": "Web Servers", "web": "Web Servers",
		"backend": "Developer Tools", "api": "Developer Tools", "developer-tools": "Developer Tools", "mcp": "AI & Machine Learning", "ci": "DevOps & CI/CD",
		"rss": "Media", "games": "Media", "family": "Dashboards & Home", "health": "Dashboards & Home", "mail": "Collaboration & Productivity",
		"helpdesk": "Collaboration & Productivity", "networking": "Security & Identity", "vpn": "Security & Identity",
	}
)

// ConvertCoolifyEntry converts an entry of service-templates.json; logoBase
// (when set) makes its relative logo path absolute.
func ConvertCoolifyEntry(slug string, e CoolifyEntry, logoBase string) (ServiceTemplate, error) {
	raw, err := base64.StdEncoding.DecodeString(e.Compose)
	if err != nil {
		return ServiceTemplate{Slug: slug}, fmt.Errorf("template %s: compose is not base64", slug)
	}
	meta := map[string]string{"documentation": e.Documentation, "slogan": e.Slogan, "category": e.Category, "logo": e.Logo, "port": e.Port, "tags": strings.Join(e.Tags, ",")}
	return convertCoolify(slug, string(raw), meta, logoBase)
}

// ConvertCoolify converts a template file of Coolify's templates/compose
// directory, described by its header comments.
func ConvertCoolify(slug, compose string) (ServiceTemplate, error) {
	return convertCoolify(slug, compose, nil, "")
}

func convertCoolify(slug, raw string, meta map[string]string, logoBase string) (ServiceTemplate, error) {
	if meta == nil {
		meta = map[string]string{}
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := headerLine.FindStringSubmatch(line)
		if m == nil {
			break
		}
		if meta[m[1]] == "" {
			meta[m[1]] = m[2]
		}
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return ServiceTemplate{Slug: slug}, fmt.Errorf("template %s: invalid compose file", slug)
	}
	root := doc.Content[0]
	services := mapValue(root, "services")
	if services == nil || services.Kind != yaml.MappingNode || len(services.Content) == 0 {
		return ServiceTemplate{Slug: slug}, fmt.Errorf("template %s: no services", slug)
	}
	c := &conversion{root: root, volumes: map[string]bool{}, ids: map[string]string{}}
	for i := 0; i < len(services.Content); i += 2 {
		name := services.Content[i].Value
		c.ids[serviceID(name)] = name
	}
	for i := 0; i < len(services.Content); i += 2 {
		c.service(services.Content[i].Value, services.Content[i+1])
	}

	// The public service: the first declaration with a port, else the
	// first declaration with the template's port.
	headerPort, _ := strconv.Atoi(meta["port"])
	var pub *exposure
	for i := range c.exposed {
		if c.exposed[i].port > 0 {
			pub = &c.exposed[i]
			break
		}
	}
	if pub == nil && len(c.exposed) > 0 && headerPort > 0 {
		pub = &c.exposed[0]
		pub.port = headerPort
	}
	if pub == nil {
		if len(c.exposed) > 0 {
			return ServiceTemplate{Slug: slug}, fmt.Errorf("template %s: the public service has no port", slug)
		}
		c.note("no public address: the service runs without a domain")
	} else {
		others := map[string]bool{}
		for _, e := range c.exposed {
			if e.service != pub.service {
				others[e.service] = true
			}
		}
		if len(others) > 0 {
			c.note("%s also had a public address in Coolify; here the domain goes to %s only", strings.Join(sortedSet(others), ", "), pub.service)
		}
	}

	name := meta["name"]
	if name == "" {
		name = displayName(slug)
	}
	// Some entries list several categories: the first known one wins.
	var category string
	for _, c := range strings.Split(strings.ToLower(meta["category"]), ",") {
		if category = coolifyCat[strings.TrimSpace(c)]; category != "" {
			break
		}
	}
	if category == "" {
		category = "Other"
	}
	logo := meta["logo"]
	if logo != "" && !strings.Contains(logo, "://") {
		logo = ""
		if logoBase != "" {
			logo = strings.TrimRight(logoBase, "/") + "/" + strings.TrimLeft(meta["logo"], "/")
		}
	}
	var tags []string
	for _, t := range strings.Split(meta["tags"], ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	website := strings.Split(meta["documentation"], "?utm_source")[0]
	x := &yaml.Node{Kind: yaml.MappingNode}
	add := func(k string, v *yaml.Node) {
		x.Content = append(x.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, v)
	}
	str := func(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: s} }
	add("name", str(name))
	add("description", str(strings.TrimSpace(meta["slogan"])))
	add("category", str(category))
	if website != "" {
		add("website", str(website))
	}
	if logo != "" {
		add("logo", str(logo))
	}
	if len(tags) > 0 {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
		for _, t := range tags {
			seq.Content = append(seq.Content, str(t))
		}
		add("tags", seq)
	}
	if pub != nil {
		add("service", str(pub.service))
		add("port", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(pub.port)})
	}
	deleteKey(root, "x-portdock")
	root.Content = append([]*yaml.Node{str("x-portdock"), x}, root.Content...)
	c.declareVolumes()

	var sb strings.Builder
	enc := yaml.NewEncoder(&sb)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return ServiceTemplate{Slug: slug}, err
	}
	lines := strings.Split(sb.String(), "\n")
	for i, line := range lines {
		lines[i] = replaceMagic(line)
	}
	out := escapeDollars(strings.Join(lines, "\n"))
	if strings.Contains(out, "${SERVICE_") || strings.Contains(out, "${COOLIFY_") {
		c.note("some Coolify variables have no equivalent and are left empty")
	}
	t, err := Parse(slug, out)
	if err != nil {
		return t, err
	}
	t.Notes = c.notes
	return t, nil
}

// requiredWithDefault is Coolify's ${VAR:?default}: required in its form,
// prefilled with the default. Docker Compose reads it as an error message.
var requiredWithDefault = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*):\?([^}]+)\}`)

// escapeDollars turns Coolify's ${VAR:?default} into ${VAR:-default}, and
// doubles the $ of ${…} that are not variables (shell or JavaScript inside
// a command), which Docker Compose would refuse.
func escapeDollars(s string) string {
	s = requiredWithDefault.ReplaceAllString(s, "$${$1:-$2}")
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '$' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 < len(s) && s[i+1] == '$' { // already escaped
			b.WriteString("$$")
			i++
			continue
		}
		if i+1 < len(s) && s[i+1] == '{' && !validRef(s[i+2:]) {
			b.WriteString("$$")
			continue
		}
		b.WriteByte('$')
	}
	return b.String()
}

// validRef reports whether s (what follows "${") starts a compose variable:
// a name, then }, or a modifier (:- - :? ? :+ +).
func validRef(s string) bool {
	n := 0
	for n < len(s) && (s[n] == '_' || s[n] >= 'A' && s[n] <= 'Z' || s[n] >= 'a' && s[n] <= 'z' || n > 0 && s[n] >= '0' && s[n] <= '9') {
		n++
	}
	if n == 0 || n == len(s) {
		return false
	}
	return strings.ContainsRune("}:-?+", rune(s[n]))
}

// replaceMagic replaces Coolify's variables in one line of the compose file.
func replaceMagic(line string) string {
	key := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
	key = strings.Trim(key, `'"`)
	if i := strings.IndexAny(key, "=:"); i > 0 {
		key = key[:i]
	}
	host := hostKey.MatchString(strings.ToUpper(key))
	for _, re := range domainRefs {
		line = replaceRefs(re, line, func(_ []string, before string) string {
			if host || strings.HasSuffix(before, "://") {
				return "${PORTDOCK_FQDN}"
			}
			return "${PORTDOCK_URL}"
		})
	}
	for _, re := range credRefs {
		line = replaceRefs(re, line, func(p []string, _ string) string {
			return "${PORTDOCK_" + credKind(p[1], p[2]) + "_" + p[3] + "}"
		})
	}
	return line
}

// replaceRefs replaces the matches of re; repl gets the groups and what
// precedes the match on the line.
func replaceRefs(re *regexp.Regexp, line string, repl func(groups []string, before string) string) string {
	var b strings.Builder
	last := 0
	for _, loc := range re.FindAllStringSubmatchIndex(line, -1) {
		groups := make([]string, len(loc)/2)
		for g := range groups {
			if loc[2*g] >= 0 {
				groups[g] = line[loc[2*g]:loc[2*g+1]]
			}
		}
		b.WriteString(line[last:loc[0]])
		b.WriteString(repl(groups, line[:loc[0]]))
		last = loc[1]
	}
	b.WriteString(line[last:])
	return b.String()
}

func credKind(kind, size string) string {
	switch kind {
	case "USER", "LOWERCASEUSER":
		return "USER"
	case "PASSWORD", "PASSWORDWITHSYMBOLS":
		if size == "64" || size == "128" {
			return "PASSWORD64"
		}
		return "PASSWORD"
	case "BASE64":
		return "RANDOM" + map[string]string{"": "32", "32": "32", "64": "64", "128": "128"}[size]
	case "REALBASE64":
		return map[string]string{"": "BASE64", "32": "BASE64", "64": "BASE64x64", "128": "BASE64x128"}[size]
	}
	return "HEX" + map[string]string{"": "32", "32": "32", "64": "64", "128": "128"}[size]
}

type exposure struct {
	service string
	port    int
}

type conversion struct {
	root    *yaml.Node
	exposed []exposure
	volumes map[string]bool   // named volumes to declare
	configs []*yaml.Node      // key, value pairs of top-level configs
	ids     map[string]string // SERVICE_*_<ID> → compose service
	notes   []string
}

func (c *conversion) note(format string, args ...any) {
	n := fmt.Sprintf(format, args...)
	for _, old := range c.notes {
		if old == n {
			return
		}
	}
	c.notes = append(c.notes, n)
}

// serviceID is the <ID> of a compose service in Coolify's variables.
func serviceID(name string) string {
	return strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name))
}

func (c *conversion) declare(service, key string) bool {
	m := domainDecl.FindStringSubmatch(key)
	if m == nil {
		return false
	}
	target := c.ids[m[2]]
	if target == "" {
		target = service
	}
	port, _ := strconv.Atoi(m[3])
	for i, e := range c.exposed {
		if e.service == target {
			if e.port == 0 {
				c.exposed[i].port = port
			}
			return true
		}
	}
	c.exposed = append(c.exposed, exposure{service: target, port: port})
	return true
}

func (c *conversion) service(name string, svc *yaml.Node) {
	if svc.Kind != yaml.MappingNode {
		return
	}
	deleteKey(svc, "exclude_from_hc")
	deleteKey(svc, "container_name")
	if mapValue(svc, "restart") == nil {
		svc.Content = append(svc.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "restart"}, &yaml.Node{Kind: yaml.ScalarNode, Value: "unless-stopped"})
	}
	if env := mapValue(svc, "environment"); env != nil {
		switch env.Kind {
		case yaml.SequenceNode:
			kept := env.Content[:0]
			for _, item := range env.Content {
				key, val, _ := strings.Cut(item.Value, "=")
				if (val == "" || strings.HasPrefix(val, "/")) && c.declare(name, strings.TrimSpace(key)) {
					continue
				}
				kept = append(kept, item)
			}
			env.Content = kept
		case yaml.MappingNode:
			kept := env.Content[:0]
			for i := 0; i+1 < len(env.Content); i += 2 {
				k, v := env.Content[i], env.Content[i+1]
				if (v.Value == "" || strings.HasPrefix(v.Value, "/")) && c.declare(name, k.Value) {
					continue
				}
				kept = append(kept, k, v)
			}
			env.Content = kept
		}
		if len(env.Content) == 0 {
			deleteKey(svc, "environment")
		}
	}
	vols := mapValue(svc, "volumes")
	if vols == nil || vols.Kind != yaml.SequenceNode {
		return
	}
	var configs []*yaml.Node
	kept := vols.Content[:0]
	for _, v := range vols.Content {
		switch v.Kind {
		case yaml.ScalarNode:
			src, rest, found := strings.Cut(v.Value, ":")
			switch {
			case found && isRelative(src):
				vol := volumeName(name, src)
				c.volumes[vol] = true
				v.Value = vol + ":" + rest
			case found && namedVolume(src):
				c.volumes[src] = true
			}
			kept = append(kept, v)
		case yaml.MappingNode:
			typ, source, target := scalar(v, "type"), scalar(v, "source"), scalar(v, "target")
			if (typ == "volume" || typ == "") && namedVolume(source) {
				c.volumes[source] = true
			}
			if typ != "bind" || !isRelative(source) {
				deleteKey(v, "is_directory")
				deleteKey(v, "isDirectory")
				deleteKey(v, "content")
				kept = append(kept, v)
				continue
			}
			if content := mapValue(v, "content"); content != nil && scalar(v, "isDirectory") != "true" && scalar(v, "is_directory") != "true" {
				cfg := volumeName(name, source)
				c.configs = append(c.configs, &yaml.Node{Kind: yaml.ScalarNode, Value: cfg},
					&yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{{Kind: yaml.ScalarNode, Value: "content"}, content}})
				configs = append(configs, &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
					{Kind: yaml.ScalarNode, Value: "source"}, {Kind: yaml.ScalarNode, Value: cfg},
					{Kind: yaml.ScalarNode, Value: "target"}, {Kind: yaml.ScalarNode, Value: target}}})
				continue
			}
			vol := volumeName(name, source)
			c.volumes[vol] = true
			kept = append(kept, &yaml.Node{Kind: yaml.ScalarNode, Value: vol + ":" + target})
		default:
			kept = append(kept, v)
		}
	}
	vols.Content = kept
	if len(kept) == 0 {
		deleteKey(svc, "volumes")
	}
	if len(configs) > 0 {
		existing := mapValue(svc, "configs")
		if existing == nil {
			existing = &yaml.Node{Kind: yaml.SequenceNode}
			svc.Content = append(svc.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "configs"}, existing)
		}
		existing.Content = append(existing.Content, configs...)
	}
}

// declareVolumes adds the named volumes and configs the conversion created.
func (c *conversion) declareVolumes() {
	if len(c.volumes) > 0 {
		vols := mapValue(c.root, "volumes")
		if vols == nil || vols.Kind != yaml.MappingNode {
			deleteKey(c.root, "volumes")
			vols = &yaml.Node{Kind: yaml.MappingNode}
			c.root.Content = append(c.root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "volumes"}, vols)
		}
		for _, name := range sortedSet(c.volumes) {
			if mapValue(vols, name) == nil {
				vols.Content = append(vols.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: name}, &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle})
			}
		}
	}
	if len(c.configs) > 0 {
		cfgs := mapValue(c.root, "configs")
		if cfgs == nil {
			cfgs = &yaml.Node{Kind: yaml.MappingNode}
			c.root.Content = append(c.root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "configs"}, cfgs)
		}
		cfgs.Content = append(cfgs.Content, c.configs...)
	}
}

// namedVolume tells a volume name from a path or a variable.
func namedVolume(src string) bool {
	return src != "" && !strings.ContainsAny(src, `/\$~`) && !strings.HasPrefix(src, ".")
}

func isRelative(src string) bool {
	return src == "." || strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../")
}

var nameJunk = regexp.MustCompile(`[^a-z0-9]+`)

// volumeName names the volume (or config) replacing a relative bind mount.
func volumeName(service, src string) string {
	base := strings.Trim(nameJunk.ReplaceAllString(strings.ToLower(path.Base(strings.TrimRight(src, "/"))), "-"), "-")
	if base == "" || base == "." {
		base = "data"
	}
	return strings.Trim(nameJunk.ReplaceAllString(strings.ToLower(service), "-"), "-") + "-" + base
}

func displayName(slug string) string {
	words := strings.Split(slug, "-")
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func scalar(m *yaml.Node, key string) string {
	if v := mapValue(m, key); v != nil && v.Kind == yaml.ScalarNode {
		return v.Value
	}
	return ""
}

func deleteKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var errNoTemplates = errors.New("the catalog has no templates")
