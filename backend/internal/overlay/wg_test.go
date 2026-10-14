package overlay

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestKeys(t *testing.T) {
	priv, pub, err := NewKeys()
	if err != nil || len(priv) != 44 || len(pub) != 44 {
		t.Fatalf("keys: %q %q %v", priv, pub, err)
	}
	if got, err := PublicKey(priv); err != nil || got != pub {
		t.Errorf("public key: %q %v", got, err)
	}
	// The vector of RFC 7748 (6.1): Alice's private and public keys.
	if got, _ := PublicKey("dwdtCnMYpX08FsFyUbJmRd9ML4frwJkqsXf7pR25LCo="); got != "hSDwCYkwp1R0i33ctD73Wg2/Og0mOBr066SpjqqbTmo=" {
		t.Errorf("RFC 7748 vector: %s", got)
	}
	if _, err := PublicKey("short"); err == nil {
		t.Error("invalid key accepted")
	}
}

func TestNextIP(t *testing.T) {
	sub := netip.MustParsePrefix("10.88.0.0/29")
	if ip, _ := NextIP(sub, nil); ip != "10.88.0.1" {
		t.Errorf("first: %s", ip)
	}
	if ip, _ := NextIP(sub, []string{"10.88.0.1", "10.88.0.3"}); ip != "10.88.0.2" {
		t.Errorf("gap: %s", ip)
	}
	if _, err := NextIP(sub, []string{"10.88.0.1", "10.88.0.2", "10.88.0.3", "10.88.0.4", "10.88.0.5", "10.88.0.6"}); err == nil {
		t.Error("the broadcast address was given")
	}
}

func TestSettings(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	s, err := ParseSettings(get(map[string]string{"overlay_enabled": "true"}))
	if err != nil || !s.Enabled || s.Subnet.String() != DefaultSubnet || s.Port != DefaultPort {
		t.Fatalf("defaults: %+v %v", s, err)
	}
	for _, bad := range []map[string]string{{"overlay_subnet": "8.8.8.0/24"}, {"overlay_subnet": "10.0.0.0/8"}, {"overlay_subnet": "nope"}, {"overlay_port": "70000"}} {
		if _, err := ParseSettings(get(bad)); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	if s, _ := ParseSettings(get(map[string]string{"overlay_subnet": "172.30.5.9/24"})); s.Subnet.String() != "172.30.5.0/24" {
		t.Errorf("masked: %s", s.Subnet)
	}
}

func TestConfig(t *testing.T) {
	peers := []Node{
		{ID: 2, Name: "b", IP: "10.88.0.2", PublicKey: "KB", Endpoint: "203.0.113.2:51820"},
		{ID: 1, Name: "a", IP: "10.88.0.1", PublicKey: "KA"},
		{ID: 3, Name: "c", IP: "10.88.0.3", PublicKey: "KC"},
	}
	c := Config(Node{ID: 1}, "PRIV", 51820, peers)
	if strings.Contains(c, "KA") || !strings.Contains(c, "PrivateKey = PRIV\nListenPort = 51820") ||
		!strings.Contains(c, "# b\n[Peer]\nPublicKey = KB\nAllowedIPs = 10.88.0.2/32\nEndpoint = 203.0.113.2:51820\nPersistentKeepalive = 25") ||
		!strings.Contains(c, "PublicKey = KC\nAllowedIPs = 10.88.0.3/32\nPersistentKeepalive = 25") {
		t.Errorf("config:\n%s", c)
	}
	for in, want := range map[[2]string]string{{"203.0.113.9", ""}: "203.0.113.9:51820", {"localhost", ""}: "", {"127.0.0.1", ""}: "",
		{"localhost", "vpn.example.com"}: "vpn.example.com:51820", {"x", "vpn.example.com:4500"}: "vpn.example.com:4500", {"2001:db8::1", ""}: "[2001:db8::1]:51820"} {
		if got := Endpoint(in[0], in[1], 51820); got != want {
			t.Errorf("endpoint %v: %q", in, got)
		}
	}
}

func TestParseDump(t *testing.T) {
	now := time.Now()
	dump := "PRIV\tPUB\t51820\toff\n" +
		"KB\t(none)\t203.0.113.2:51820\t10.88.0.2/32\t" + itoa(now.Add(-30*time.Second).Unix()) + "\t1200\t3400\t25\n" +
		"KC\t(none)\t(none)\t10.88.0.3/32\t0\t0\t0\t25\n"
	peers := ParseDump(dump)
	if len(peers) != 2 || peers[0].PublicKey != "KB" || !peers[0].Connected(now) || peers[0].RxBytes != 1200 || peers[1].Endpoint != "" || peers[1].Connected(now) {
		t.Errorf("dump: %+v", peers)
	}
	if subnetOf("10.88.0.7/24") != "10.88.0.0/24" || subnetOf("bad") != "" {
		t.Error("subnetOf")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
