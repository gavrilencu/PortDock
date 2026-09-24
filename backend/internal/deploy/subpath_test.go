package deploy

import "testing"

func TestSubPath(t *testing.T) {
	root := "/data/portdock/builds/u/d/src"
	cases := []struct{ rels []string; want string }{
		{[]string{"/"}, root},
		{[]string{""}, root},
		{[]string{"apps/web"}, root + "/apps/web"},
		{[]string{"/apps/web/", "Dockerfile"}, root + "/apps/web/Dockerfile"},
		{[]string{"", "docker-compose.yml"}, root + "/docker-compose.yml"},
	}
	for _, c := range cases {
		if got, err := subPath(root, c.rels...); err != nil || got != c.want {
			t.Errorf("subPath(%v) = %q, %v; want %q", c.rels, got, err, c.want)
		}
	}
	for _, rels := range [][]string{{".."}, {"../../../"}, {"apps/../../../secret.key"}, {"/", "../../portdock.db"}, {"web", "../../x/Dockerfile"}} {
		if got, err := subPath(root, rels...); err == nil {
			t.Errorf("subPath(%v) = %q; want an error", rels, got)
		}
	}
}
