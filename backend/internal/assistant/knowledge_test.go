package assistant

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchDocs(t *testing.T) {
	cases := []struct{ query, wantPath, wantText string }{
		{"canary rollback percentage of traffic", "Canary", "weighted"},
		{"point in time recovery postgres wal", "Database backups", "pg_basebackup"},
		{"x-portdock placeholder generated password", "Service templates", "PORTDOCK_PASSWORD"},
		{"exit code 137 OOMKilled", "Assistant guide", "137"},
		{"traefik 502 bad gateway", "Assistant guide", "502"},
		{"project roles viewer developer maintainer", "", "maintainer"},
		{"restore the instance backup on a new server", "Backup and restore of PortDock itself", "age"},
	}
	for _, c := range cases {
		res := SearchDocs(c.query, 3)
		if len(res) == 0 {
			t.Errorf("%q: no result", c.query)
			continue
		}
		found := false
		for _, r := range res {
			if (c.wantPath == "" || strings.Contains(r.Path, c.wantPath)) && strings.Contains(r.Text, c.wantText) {
				found = true
			}
		}
		if !found {
			var paths []string
			for _, r := range res {
				paths = append(paths, r.Path)
			}
			t.Errorf("%q: expected a section %q containing %q, got %v", c.query, c.wantPath, c.wantText, paths)
		}
		for _, r := range res {
			if len([]rune(r.Text)) > SectionMaxChars+120 {
				t.Errorf("%q: section %q not clipped (%d chars)", c.query, r.Path, len(r.Text))
			}
		}
	}
	if res := SearchDocs("", 3); res != nil {
		t.Errorf("empty query: %v", res)
	}
	if res := SearchDocs("the of and", 3); res != nil {
		t.Errorf("stop words only: %v", res)
	}
}

func TestSplitSections(t *testing.T) {
	secs := SplitSections("README.md", "<div align=\"center\">\n<img src=\"x\" />\n</div>\n\n## Why\n\n- **Everything.** text\n\n<details>\n<summary><b>Remote servers</b></summary>\n\n1. Add.\n\n</details>\n\n**🚀 Deployments**\n- From GitHub\n\n```yaml\n# not a heading\nkey: v\n```\n")
	var paths []string
	for _, s := range secs {
		paths = append(paths, s.Path)
	}
	want := []string{"PortDock documentation › Why", "PortDock documentation › Why › Remote servers", "PortDock documentation › Why › 🚀 Deployments"}
	if strings.Join(paths, "|") != strings.Join(want, "|") {
		t.Fatalf("paths: %v", paths)
	}
	if !strings.Contains(secs[2].Text, "# not a heading") || strings.Contains(secs[0].Text, "<img") {
		t.Errorf("text: %q / %q", secs[2].Text, secs[0].Text)
	}
}

func TestTokenize(t *testing.T) {
	got := strings.Join(Tokenize("Deployments of docker-compose.yml and pg_dump via /api/metrics, HTTPS!"), " ")
	for _, w := range []string{"deployment", "docker", "compose", "yml", "docker-compose.yml", "pg_dump", "pg", "dump", "api", "metric", "http"} {
		if !strings.Contains(" "+got+" ", " "+w+" ") {
			t.Errorf("missing %q in %q", w, got)
		}
	}
	if strings.Contains(" "+got+" ", " of ") || strings.Contains(" "+got+" ", " and ") {
		t.Errorf("stop words kept: %q", got)
	}
}

// The embedded copies must match the repository's documentation.
func TestDocsInSync(t *testing.T) {
	pairs := map[string]string{
		"docs/README.md":            filepath.Join("..", "..", "..", "README.md"),
		"docs/service-templates.md": filepath.Join("..", "templates", "services", "README.md"),
	}
	for embedded, src := range pairs {
		want, err := os.ReadFile(src)
		if err != nil {
			t.Skipf("%s not available: %v", src, err)
		}
		got, err := docsFS.ReadFile(embedded)
		if err != nil {
			t.Fatal(err)
		}
		norm := func(b []byte) string { return strings.ReplaceAll(string(b), "\r\n", "\n") }
		if norm(got) != norm(want) {
			t.Errorf("%s is out of date with %s: run `make docs-sync`", embedded, src)
		}
	}
}
