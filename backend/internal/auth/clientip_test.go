package auth

import (
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	t.Cleanup(func() { _ = SetTrustedProxies("") })
	req := func(remote, xff, real string) string {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		if real != "" {
			r.Header.Set("X-Real-Ip", real)
		}
		return ClientIP(r)
	}
	// Default: the proxy on the private docker network is trusted, the internet is not.
	if got := req("172.18.0.2:4321", "203.0.113.9", ""); got != "203.0.113.9" {
		t.Errorf("via docker proxy: %q", got)
	}
	if got := req("172.18.0.2:4321", "198.51.100.7, 203.0.113.9, 10.0.0.5", ""); got != "203.0.113.9" {
		t.Errorf("last untrusted hop: %q", got)
	}
	if got := req("172.18.0.2:4321", "", "203.0.113.9"); got != "203.0.113.9" {
		t.Errorf("X-Real-Ip: %q", got)
	}
	if got := req("198.51.100.7:1234", "1.2.3.4", "5.6.7.8"); got != "198.51.100.7" {
		t.Errorf("spoofed headers from the internet were believed: %q", got)
	}
	if got := req("[2001:db8::1]:443", "1.2.3.4", ""); got != "2001:db8::1" {
		t.Errorf("ipv6 direct: %q", got)
	}
	// An explicit list: only that proxy is believed.
	if err := SetTrustedProxies("203.0.113.1, 10.1.0.0/16"); err != nil {
		t.Fatal(err)
	}
	if got := req("203.0.113.1:80", "9.9.9.9", ""); got != "9.9.9.9" {
		t.Errorf("listed proxy: %q", got)
	}
	if got := req("172.18.0.2:80", "9.9.9.9", ""); got != "172.18.0.2" {
		t.Errorf("unlisted private proxy: %q", got)
	}
	if err := SetTrustedProxies("none"); err != nil {
		t.Fatal(err)
	}
	if got := req("127.0.0.1:80", "9.9.9.9", ""); got != "127.0.0.1" {
		t.Errorf("none: %q", got)
	}
	if err := SetTrustedProxies("not-an-ip"); err == nil {
		t.Error("invalid list accepted")
	}
}
