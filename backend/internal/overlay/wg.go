// Package overlay joins PortDock's servers in a private WireGuard network
// (pd0, 10.88.0.0/24 by default): an agent container on each server keeps the
// interface configured, so applications reach the databases of other servers
// and the primary server's proxy balances traffic across the replicas of an
// application on several servers, without exposing their ports publicly.
package overlay

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/curve25519"
)

// Interface is the WireGuard interface on every server.
const Interface = "pd0"

// Settings of the private network.
type Settings struct {
	Enabled bool
	Subnet  netip.Prefix
	Port    int // UDP port WireGuard listens on
}

// DefaultSubnet and DefaultPort apply when the settings are empty.
const (
	DefaultSubnet = "10.88.0.0/24"
	DefaultPort   = 51820
)

// ParseSettings reads the overlay_* settings.
func ParseSettings(get func(string) string) (Settings, error) {
	s := Settings{Enabled: get("overlay_enabled") == "true", Port: DefaultPort}
	subnet := strings.TrimSpace(get("overlay_subnet"))
	if subnet == "" {
		subnet = DefaultSubnet
	}
	p, err := netip.ParsePrefix(subnet)
	if err != nil || !p.Addr().Is4() || p.Bits() < 16 || p.Bits() > 28 || !p.Addr().IsPrivate() {
		return s, errors.New("the subnet must be a private IPv4 range between /16 and /28, e.g. 10.88.0.0/24")
	}
	s.Subnet = p.Masked()
	if v := strings.TrimSpace(get("overlay_port")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return s, errors.New("the WireGuard port must be between 1 and 65535")
		}
		s.Port = n
	}
	return s, nil
}

// NewKeys returns a WireGuard key pair, base64 encoded.
func NewKeys() (private, public string, err error) {
	var k [32]byte
	if _, err := rand.Read(k[:]); err != nil {
		return "", "", err
	}
	// Clamping as WireGuard does.
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	pub, err := curve25519.X25519(k[:], curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(k[:]), base64.StdEncoding.EncodeToString(pub), nil
}

// PublicKey returns the public key of a private one.
func PublicKey(private string) (string, error) {
	k, err := base64.StdEncoding.DecodeString(private)
	if err != nil || len(k) != 32 {
		return "", errors.New("invalid WireGuard private key")
	}
	pub, err := curve25519.X25519(k, curve25519.Basepoint)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(pub), nil
}

// NextIP returns the first free host address of subnet (the network and
// broadcast addresses excluded).
func NextIP(subnet netip.Prefix, used []string) (string, error) {
	taken := map[netip.Addr]bool{}
	for _, u := range used {
		if a, err := netip.ParseAddr(u); err == nil {
			taken[a] = true
		}
	}
	a := subnet.Masked().Addr().Next()
	for subnet.Contains(a) {
		next := a.Next()
		if !subnet.Contains(next) {
			break // the broadcast address
		}
		if !taken[a] {
			return a.String(), nil
		}
		a = next
	}
	return "", fmt.Errorf("no free address left in %s", subnet)
}

// Node is a server of the private network.
type Node struct {
	ID        int64
	Name      string
	IP        string
	PublicKey string
	Endpoint  string // host:port the others reach it at ("" = it connects out only)
}

// Config renders the WireGuard configuration of self (wg setconf format).
func Config(self Node, private string, port int, peers []Node) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Interface]\nPrivateKey = %s\nListenPort = %d\n", private, port)
	sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })
	for _, p := range peers {
		if p.ID == self.ID || p.PublicKey == "" || p.IP == "" {
			continue
		}
		fmt.Fprintf(&b, "\n# %s\n[Peer]\nPublicKey = %s\nAllowedIPs = %s/32\n", p.Name, p.PublicKey, p.IP)
		if p.Endpoint != "" {
			fmt.Fprintf(&b, "Endpoint = %s\n", p.Endpoint)
		}
		// Keeps NAT mappings open, so servers behind NAT stay reachable.
		b.WriteString("PersistentKeepalive = 25\n")
	}
	return b.String()
}

// Endpoint is host:port of a server for its peers.
func Endpoint(host, override string, port int) string {
	if o := strings.TrimSpace(override); o != "" {
		if _, _, err := splitHostPort(o); err == nil {
			return o
		}
		return joinHostPort(o, port)
	}
	h := strings.TrimSpace(host)
	if h == "" || h == "localhost" || strings.HasPrefix(h, "127.") || h == "::1" {
		return ""
	}
	return joinHostPort(h, port)
}

func splitHostPort(s string) (string, int, error) {
	i := strings.LastIndex(s, ":")
	if i < 0 || strings.HasSuffix(s, "]") {
		return "", 0, errors.New("no port")
	}
	p, err := strconv.Atoi(s[i+1:])
	if err != nil {
		return "", 0, err
	}
	return s[:i], p, nil
}

func joinHostPort(h string, port int) string {
	if strings.Contains(h, ":") && !strings.HasPrefix(h, "[") {
		h = "[" + h + "]"
	}
	return h + ":" + strconv.Itoa(port)
}

// PeerState is what WireGuard reports of a peer.
type PeerState struct {
	PublicKey     string
	Endpoint      string
	LastHandshake time.Time // zero: never
	RxBytes       int64
	TxBytes       int64
}

// ParseDump reads `wg show pd0 dump`: the interface line, then one line
// per peer (public key, preshared key, endpoint, allowed IPs, latest
// handshake, rx, tx, keepalive).
func ParseDump(out string) []PeerState {
	var peers []PeerState
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i, line := range lines {
		f := strings.Split(line, "\t")
		if i == 0 || len(f) < 8 {
			continue
		}
		p := PeerState{PublicKey: f[0], Endpoint: f[2]}
		if p.Endpoint == "(none)" {
			p.Endpoint = ""
		}
		if ts, _ := strconv.ParseInt(f[4], 10, 64); ts > 0 {
			p.LastHandshake = time.Unix(ts, 0)
		}
		p.RxBytes, _ = strconv.ParseInt(f[5], 10, 64)
		p.TxBytes, _ = strconv.ParseInt(f[6], 10, 64)
		peers = append(peers, p)
	}
	return peers
}

// Connected reports whether a peer completed a handshake recently: WireGuard
// renews it every two minutes while traffic flows, keepalives included.
func (p PeerState) Connected(now time.Time) bool {
	return !p.LastHandshake.IsZero() && now.Sub(p.LastHandshake) < 3*time.Minute
}

func mustAddr(s string) netip.Addr {
	a, _ := netip.ParseAddr(s)
	return a
}
