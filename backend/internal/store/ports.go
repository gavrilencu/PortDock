package store

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// NormalizePortMappings validates docker -p style mappings and expands a bare
// port ("4000") to "4000:4000" so it is reachable on the same host port.
// Accepted forms: PORT, HOST:CONTAINER, IP:HOST:CONTAINER, each with /tcp|/udp.
func NormalizePortMappings(s string) (string, error) {
	var out []string
	for _, m := range SplitList(s) {
		proto := ""
		if i := strings.LastIndex(m, "/"); i >= 0 {
			proto = m[i:]
			if proto != "/tcp" && proto != "/udp" {
				return "", fmt.Errorf("invalid protocol in %q (use /tcp or /udp)", m)
			}
			m = m[:i]
		}
		parts := strings.Split(m, ":")
		if strings.HasPrefix(m, "[") { // [ipv6]:host:container
			end := strings.Index(m, "]:")
			if end < 0 {
				return "", fmt.Errorf("invalid port mapping %q", m)
			}
			parts = append([]string{m[:end+1]}, strings.Split(m[end+2:], ":")...)
		}
		valid := func(p string) bool { n, err := strconv.Atoi(p); return err == nil && n > 0 && n <= 65535 }
		switch len(parts) {
		case 1:
			if !valid(parts[0]) {
				return "", fmt.Errorf("invalid port %q", m)
			}
			parts = []string{parts[0], parts[0]}
		case 2:
			if !valid(parts[0]) || !valid(parts[1]) {
				return "", fmt.Errorf("invalid port mapping %q (expected HOST:CONTAINER)", m)
			}
		case 3:
			if net.ParseIP(strings.Trim(parts[0], "[]")) == nil || !valid(parts[1]) || !valid(parts[2]) {
				return "", fmt.Errorf("invalid port mapping %q (expected IP:HOST:CONTAINER)", m)
			}
		default:
			return "", fmt.Errorf("invalid port mapping %q", m)
		}
		out = append(out, strings.Join(parts, ":")+proto)
	}
	return strings.Join(out, ","), nil
}
