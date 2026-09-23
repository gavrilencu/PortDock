package updater

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "v1.2.4", -1}, {"1.10.0", "1.9.9", 1}, {"v2.0.0", "v2.0.0", 0},
		{"v2.0.0-rc.1", "v2.0.0", -1}, {"v2.0.0-rc.2", "v2.0.0-rc.10", -1}, {"v2.0.0-alpha", "v2.0.0-beta", -1},
		{"dev", "v0.0.1", -1}, {"v1.0.0", "dev", 1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestLatestRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/shipyard/releases" {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"tag_name": "v1.4.0-rc.1", "prerelease": true, "body": "rc"},
			{"tag_name": "v1.3.0", "name": "Shipyard 1.3", "body": "## Notes"},
			{"tag_name": "v1.5.0", "draft": true},
			{"tag_name": "nightly"},
			{"tag_name": "v1.2.9"},
		})
	}))
	defer srv.Close()
	t.Setenv("SHIPYARD_UPDATE_API", srv.URL)
	r, err := LatestRelease(context.Background(), "acme/shipyard", "", false)
	if err != nil || r.Tag != "v1.3.0" || r.Notes != "## Notes" {
		t.Fatalf("stable: %+v %v", r, err)
	}
	if r, _ := LatestRelease(context.Background(), "acme/shipyard", "", true); r.Tag != "v1.4.0-rc.1" {
		t.Fatalf("prerelease: %+v", r)
	}
	if _, err := LatestRelease(context.Background(), "acme/missing", "", false); err == nil {
		t.Fatal("missing repo")
	}
	if ValidRepo("not a repo") || !ValidRepo("acme/shipyard") {
		t.Fatal("repo validation")
	}
}

func TestCloneBody(t *testing.T) {
	inspect := `{
	  "Id": "0123456789abcdef0123",
	  "Name": "/shipyard",
	  "Config": {
	    "Hostname": "0123456789ab",
	    "Image": "shipyard:latest",
	    "Env": ["SHIPYARD_PUBLIC_URL=https://panel.corp", "PATH=/usr/local/sbin:/usr/bin", "NIXPACKS=v1"],
	    "Cmd": null,
	    "Entrypoint": ["shipyard"],
	    "Labels": {"com.docker.compose.project": "shipyard", "org.opencontainers.image.version": "v1.2.0"},
	    "Healthcheck": {"Test": ["CMD", "wget"]}
	  },
	  "HostConfig": {"Binds": ["/var/run/docker.sock:/var/run/docker.sock", "/data/shipyard:/data/shipyard"], "PortBindings": {"8000/tcp": [{"HostPort": "8000"}]}, "RestartPolicy": {"Name": "unless-stopped"}},
	  "NetworkSettings": {"Networks": {"shipyard_default": {"Aliases": ["shipyard", "0123456789ab"]}}}
	}`
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(inspect), &raw)
	var c Container
	_ = json.Unmarshal([]byte(inspect), &c)
	c.Raw = raw
	img := map[string]json.RawMessage{
		"Env":         json.RawMessage(`["PATH=/usr/local/sbin:/usr/bin","NIXPACKS=v1"]`),
		"Entrypoint":  json.RawMessage(`["shipyard"]`),
		"Labels":      json.RawMessage(`{"org.opencontainers.image.version":"v1.2.0"}`),
		"Healthcheck": json.RawMessage(`{"Test":["CMD","wget"]}`),
	}
	body, err := CloneBody(c, img, "ghcr.io/acme/shipyard:v1.3.0")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(body)
	var got struct {
		Image       string
		Hostname    string
		Env         []string
		Entrypoint  []string
		Healthcheck any
		Labels      map[string]string
		HostConfig  struct {
			Binds         []string
			RestartPolicy struct{ Name string }
		}
		NetworkingConfig struct {
			EndpointsConfig map[string]struct{ Aliases []string }
		}
	}
	_ = json.Unmarshal(b, &got)
	if got.Image != "ghcr.io/acme/shipyard:v1.3.0" || got.Hostname != "" {
		t.Fatalf("image/hostname: %s", b)
	}
	if len(got.Env) != 1 || got.Env[0] != "SHIPYARD_PUBLIC_URL=https://panel.corp" {
		t.Fatalf("env should keep only operator settings: %v", got.Env)
	}
	if got.Entrypoint != nil || got.Healthcheck != nil {
		t.Fatalf("image defaults must come from the new image: %s", b)
	}
	if got.Labels["com.docker.compose.project"] != "shipyard" || got.Labels["org.opencontainers.image.version"] != "" {
		t.Fatalf("labels: %v", got.Labels)
	}
	if len(got.HostConfig.Binds) != 2 || got.HostConfig.RestartPolicy.Name != "unless-stopped" {
		t.Fatalf("host config (volumes!) must be preserved: %s", b)
	}
	if a := got.NetworkingConfig.EndpointsConfig["shipyard_default"].Aliases; len(a) != 1 || a[0] != "shipyard" {
		t.Fatalf("aliases: %v", a)
	}
}
