package store

import "testing"

func TestSafeRelPath(t *testing.T) {
	good := map[string]string{
		"": "", "/": "", ".": "", "./": "", "apps/web": "apps/web", "/apps/web/": "apps/web",
		"Dockerfile": "Dockerfile", "./docker/Dockerfile.prod": "docker/Dockerfile.prod", "a//b": "a/b", "dist": "dist",
	}
	for in, want := range good {
		if got, err := SafeRelPath(in); err != nil || got != want {
			t.Errorf("SafeRelPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"..", "../", "../../data", "apps/../../secret.key", "/apps/../..", "a/../../b", "..\\x", "a\x00b"} {
		if got, err := SafeRelPath(in); err == nil {
			t.Errorf("SafeRelPath(%q) = %q; want an error", in, got)
		}
	}
}
