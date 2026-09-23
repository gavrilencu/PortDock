package deploy

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/portdock/portdock/internal/store"
	"go.yaml.in/yaml/v3"
)

func TestVersionsToExpire(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	// v10 … v1, newest first; v3 is live (rolled back to), v9 is 20 days old.
	var vs []store.AppVersion
	for n := 10; n >= 1; n-- {
		age := time.Duration(11-n) * time.Hour
		if n == 9 {
			age = 20 * 24 * time.Hour
		}
		if n <= 2 {
			age = 30 * 24 * time.Hour
		}
		vs = append(vs, store.AppVersion{ID: int64(n), Number: n, CreatedAt: ago(age)})
	}
	got := map[int]bool{}
	for _, v := range versionsToExpire(vs, 3, now) {
		got[v.Number] = true
	}
	// Kept: live v3, and the 5 most recent previous versions younger than 15 days: v10, v8, v7, v6, v5.
	for _, n := range []int{10, 8, 7, 6, 5, 3} {
		if got[n] {
			t.Errorf("v%d must be kept", n)
		}
	}
	for _, n := range []int{9, 4, 2, 1} {
		if !got[n] {
			t.Errorf("v%d must expire", n)
		}
	}
	if len(versionsToExpire(vs[2:5], 8, now)) != 0 { // v8 (live), v7, v6
		t.Error("recent versions within the limit must all be kept")
	}
}

func TestVersionRepo(t *testing.T) {
	app := store.Application{UUID: "AbC123"}
	if versionRepo(app, "") != "portdock/abc123" || versionRepo(app, "Web_App") != "portdock/abc123-web-app" {
		t.Fatal(versionRepo(app, ""), versionRepo(app, "Web_App"))
	}
	if !versionTagRe.MatchString("portdock/abc:v12") || versionTagRe.MatchString("portdock/abc:1a2b3c4") {
		t.Fatal("version tag detection")
	}
}

func TestComposeVersionPinsImages(t *testing.T) {
	x := &fakeX{files: map[string]string{"/src/docker-compose.yml": monorepo}, written: map[string]string{}}
	var log bytes.Buffer
	_, err := RunComposeUp(context.Background(), ComposeUp{
		X: x, Project: "sy-app1", Dir: "/src", File: "/src/docker-compose.yml", Service: "frontend", Port: 80, FileRoutes: true,
		Images: map[string]string{"frontend": "portdock/app1-frontend:v3", "backend": "portdock/app1-backend:v3"},
		Kind:   "application", Resource: "app1", Network: "portdock", Log: printer{&log},
	})
	if err != nil {
		t.Fatal(err)
	}
	var override struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(x.written["/src/.portdock.override.yml"]), &override); err != nil {
		t.Fatal(err)
	}
	fe, db := override.Services["frontend"], override.Services["db"]
	if fe["image"] != "portdock/app1-frontend:v3" || fe["pull_policy"] != "never" {
		t.Errorf("frontend must run the version's image: %v", fe)
	}
	if _, ok := db["image"]; ok {
		t.Error("services without a saved image keep the compose file's")
	}
	for _, c := range x.cmds {
		if strings.Contains(c, "compose") && strings.Contains(c, " up ") && strings.Contains(c, "--build") {
			t.Errorf("a version deploy must not build: %s", c)
		}
	}
}
