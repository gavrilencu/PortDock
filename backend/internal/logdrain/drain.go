// Package logdrain ships the logs of the containers PortDock runs to the
// organization's log platform: Loki, Elasticsearch / OpenSearch, Graylog
// (GELF), Splunk, syslog or any HTTP endpoint. A Vector agent on each server
// reads the logs from Docker, adds the project, environment and application
// of each line, and forwards them; PortDock writes its configuration.
package logdrain

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Types of destinations.
var Types = []string{"loki", "elasticsearch", "gelf", "splunk", "syslog", "http"}

// Config holds the credentials and options of a destination; it is stored
// encrypted.
type Config struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// Token is the bearer token (Loki, HTTP), the API key (Elasticsearch) or
	// the HTTP Event Collector token (Splunk).
	Token string `json:"token,omitempty"`
	// Tenant is Loki's X-Scope-OrgID.
	Tenant string `json:"tenant,omitempty"`
	// Index of Elasticsearch (default portdock-%Y.%m.%d) or Splunk.
	Index      string            `json:"index,omitempty"`
	SourceType string            `json:"sourcetype,omitempty"` // Splunk
	Headers    map[string]string `json:"headers,omitempty"`    // HTTP
	SkipVerify bool              `json:"skip_verify,omitempty"`
}

// Target is a destination ready to render.
type Target struct {
	ID           int64
	Type, URL    string
	Config       Config
	Projects     []int64 // none: every project
	IncludeProxy bool
}

var (
	indexRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9._%+-]{0,200}$`)
	tokenRe  = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+.^_|~-]{1,100}$`) // an HTTP header name
	simpleRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,100}$`)
)

// Validate checks a destination before it is saved.
func Validate(typ, rawURL string, c Config) error {
	if !slices.Contains(Types, typ) {
		return fmt.Errorf("unknown type %q", typ)
	}
	if typ == "gelf" || typ == "syslog" {
		if _, _, _, err := socketAddress(typ, rawURL); err != nil {
			return err
		}
	} else {
		u, err := url.Parse(rawURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("the address must be an http:// or https:// URL")
		}
		if u.User != nil {
			return fmt.Errorf("put the credentials in their fields, not in the address")
		}
	}
	for _, v := range []string{c.Username, c.Password, c.Token, c.Tenant, c.Index, c.SourceType} {
		if strings.ContainsAny(v, "\r\n\x00") {
			return fmt.Errorf("line breaks are not allowed")
		}
	}
	if c.Index != "" && !indexRe.MatchString(c.Index) {
		return fmt.Errorf("invalid index %q (lowercase letters, digits, . _ - + and %%Y %%m %%d)", c.Index)
	}
	if c.SourceType != "" && !simpleRe.MatchString(c.SourceType) {
		return fmt.Errorf("invalid source type %q", c.SourceType)
	}
	if c.Tenant != "" && !simpleRe.MatchString(c.Tenant) {
		return fmt.Errorf("invalid tenant %q", c.Tenant)
	}
	for k, v := range c.Headers {
		if !tokenRe.MatchString(k) || strings.ContainsAny(v, "\r\n\x00") {
			return fmt.Errorf("invalid header %q", k)
		}
	}
	if typ == "splunk" && c.Token == "" {
		return fmt.Errorf("the HTTP Event Collector token is required")
	}
	return nil
}

// socketAddress parses udp://host:port, tcp://host:port or tls://host:port.
func socketAddress(typ, raw string) (mode, addr string, tls bool, err error) {
	u, perr := url.Parse(raw)
	if perr != nil || u.Host == "" || (u.Path != "" && u.Path != "/") {
		return "", "", false, fmt.Errorf("the address must look like udp://host:port, tcp://host:port or tls://host:port")
	}
	switch u.Scheme {
	case "udp", "tcp":
		mode = u.Scheme
	case "tls":
		mode, tls = "tcp", true
	default:
		return "", "", false, fmt.Errorf("the address must start with udp://, tcp:// or tls://")
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"gelf": "12201", "syslog": "514"}[typ]
		if tls && typ == "syslog" {
			port = "6514"
		}
	}
	if n, perr := strconv.Atoi(port); perr != nil || n < 1 || n > 65535 {
		return "", "", false, fmt.Errorf("invalid port %q", port)
	}
	return mode, net.JoinHostPort(u.Hostname(), port), tls, nil
}
