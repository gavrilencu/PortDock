package store

import (
	"fmt"
	"path"
	"strings"
)

// SafeRelPath normalizes a path typed by a user that must stay inside a
// checkout (base directory, Dockerfile, compose file, publish directory): it
// returns a clean relative path, or an error when the path climbs out with
// "..". The empty string, "." and "/" all mean the root and come back as "".
func SafeRelPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if strings.ContainsAny(p, "\x00\\") {
		return "", fmt.Errorf("invalid path %q", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", fmt.Errorf("the path %q leaves the repository", p)
		}
	}
	clean := path.Clean("/" + p)
	return strings.TrimPrefix(clean, "/"), nil
}
