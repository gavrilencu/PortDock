package api

import "testing"

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"/applications/3?tab=storage": "/applications/3?tab=storage",
		"/":                           "/",
		"https://evil.example":        "/",
		"//evil.example":              "/",
		`/\evil.example`:              "/",
		"/\t/evil.example":            "/",
		"/\n/evil.example":            "/",
		"/ /evil.example":             "/",
		"javascript:alert(1)":         "/",
		"":                            "/",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}
