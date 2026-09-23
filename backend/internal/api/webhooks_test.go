package api

import "testing"

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"src/**", "src/a/b/c.go", true},
		{"src/**", "docs/readme.md", false},
		{"**/*.go", "cmd/main.go", true},
		{"**/*.go", "main.go", true},
		{"*.md", "docs/readme.md", true}, // no slash: match basename
		{"apps/web/**", "apps/web/package.json", true},
		{"apps/web/**", "apps/api/main.go", false},
		{"/Dockerfile", "Dockerfile", true},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.name); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestMatchesWatchPaths(t *testing.T) {
	if !matchesWatchPaths("", []string{"anything"}) {
		t.Error("empty patterns must match everything")
	}
	if !matchesWatchPaths("apps/web/**", nil) {
		t.Error("unknown file list must deploy")
	}
	if matchesWatchPaths("apps/web/**\n!**/*.md", []string{"apps/web/README.md"}) {
		t.Error("negated pattern should exclude")
	}
	if !matchesWatchPaths("apps/web/**\n!**/*.md", []string{"apps/web/README.md", "apps/web/src/x.ts"}) {
		t.Error("one included file should trigger")
	}
}

func TestParseDotEnv(t *testing.T) {
	got, err := parseDotEnv("# comment\nexport A=1\nB=\"multi\\nline\"\nC='raw $x'\nA=2\n\n")
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]string{{"A", "2"}, {"B", "multi\nline"}, {"C", "raw $x"}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %v, want %v", i, got[i], want[i])
		}
	}
	if _, err := parseDotEnv("NOT VALID"); err == nil {
		t.Error("expected error for invalid line")
	}
}

func TestValidHMAC(t *testing.T) {
	// echo -n 'hello' | openssl dgst -sha256 -hmac secret
	sig := "sha256=88aab3ede8d3adf94d26ab90d3bafd4a2083070c3bcce9c014ee04a443847c0b"
	if !validHMAC("secret", []byte("hello"), sig) {
		t.Error("valid signature rejected")
	}
	if validHMAC("other", []byte("hello"), sig) {
		t.Error("invalid signature accepted")
	}
}
