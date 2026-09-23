package store

import "testing"

func TestNormalizePortMappings(t *testing.T) {
	ok := map[string]string{
		"4000":                   "4000:4000",
		"8080:80, 9000:9000/udp": "8080:80,9000:9000/udp",
		"127.0.0.1:5432:5432":    "127.0.0.1:5432:5432",
		"[::1]:8080:80":          "[::1]:8080:80",
		"":                       "",
	}
	for in, want := range ok {
		got, err := NormalizePortMappings(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"abc", "70000", "80:", "1.2.3:80:80", "80/sctp", "a:b:c:d"} {
		if _, err := NormalizePortMappings(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
