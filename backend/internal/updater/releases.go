// Package updater keeps Shipyard up to date: it discovers releases on GitHub and
// replaces the running Shipyard container with a new image, rolling back
// automatically when the new version doesn't become healthy.
package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Release is a published Shipyard version.
type Release struct {
	Tag         string `json:"tag"`
	Name        string `json:"name"`
	Notes       string `json:"notes"`
	URL         string `json:"url"`
	PublishedAt string `json:"published_at"`
	Prerelease  bool   `json:"prerelease"`
}

var httpClient = &http.Client{Timeout: 20 * time.Second}

// apiBase is overridable for tests and GitHub Enterprise (SHIPYARD_UPDATE_API).
func apiBase() string {
	if v := strings.TrimRight(os.Getenv("SHIPYARD_UPDATE_API"), "/"); v != "" {
		return v
	}
	return "https://api.github.com"
}

var repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// ValidRepo checks the owner/name form.
func ValidRepo(repo string) bool { return repoRe.MatchString(repo) }

// LatestRelease returns the newest release of repo (optionally including pre-releases).
func LatestRelease(ctx context.Context, repo, token string, prerelease bool) (Release, error) {
	if !ValidRepo(repo) {
		return Release{}, fmt.Errorf("repository must look like owner/name")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase()+"/repos/"+repo+"/releases?per_page=30", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Shipyard-Updater")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("GitHub unreachable: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	switch res.StatusCode {
	case 200:
	case 404:
		return Release{}, fmt.Errorf("repository %s not found (for private repositories add a token)", repo)
	case 401, 403:
		return Release{}, fmt.Errorf("GitHub refused the request (HTTP %d): check the token or the API rate limit", res.StatusCode)
	default:
		return Release{}, fmt.Errorf("GitHub returned HTTP %d", res.StatusCode)
	}
	var list []struct {
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		Body        string `json:"body"`
		HTMLURL     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
		Prerelease  bool   `json:"prerelease"`
		Draft       bool   `json:"draft"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return Release{}, fmt.Errorf("unexpected response from GitHub: %w", err)
	}
	var best *Release
	for _, r := range list {
		if r.Draft || (r.Prerelease && !prerelease) {
			continue
		}
		if _, ok := ParseVersion(r.TagName); !ok {
			continue
		}
		rel := Release{Tag: r.TagName, Name: r.Name, Notes: r.Body, URL: r.HTMLURL, PublishedAt: r.PublishedAt, Prerelease: r.Prerelease}
		if best == nil || Compare(rel.Tag, best.Tag) > 0 {
			best = &rel
		}
	}
	if best == nil {
		return Release{}, fmt.Errorf("no releases published in %s yet", repo)
	}
	return *best, nil
}

// Version is a parsed semantic version.
type Version struct {
	Major, Minor, Patch int
	Pre                 string
}

var semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

// ParseVersion parses "v1.2.3", "1.2.3-rc.1".
func ParseVersion(s string) (Version, bool) {
	m := semverRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	c, _ := strconv.Atoi(m[3])
	return Version{a, b, c, m[4]}, true
}

// Compare returns -1, 0 or 1. Unparseable versions (e.g. "dev") sort before every release.
func Compare(a, b string) int {
	va, oka := ParseVersion(a)
	vb, okb := ParseVersion(b)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return -1
	case !okb:
		return 1
	}
	for _, d := range [][2]int{{va.Major, vb.Major}, {va.Minor, vb.Minor}, {va.Patch, vb.Patch}} {
		if d[0] != d[1] {
			if d[0] < d[1] {
				return -1
			}
			return 1
		}
	}
	return comparePre(va.Pre, vb.Pre)
}

// comparePre implements semver precedence for pre-release identifiers.
func comparePre(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1 // a release is newer than its pre-releases
	case b == "":
		return -1
	}
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		na, ea := strconv.Atoi(pa[i])
		nb, eb := strconv.Atoi(pb[i])
		switch {
		case ea == nil && eb == nil && na != nb:
			if na < nb {
				return -1
			}
			return 1
		case ea == nil && eb != nil:
			return -1
		case ea != nil && eb == nil:
			return 1
		case pa[i] != pb[i]:
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(pa) < len(pb):
		return -1
	case len(pa) > len(pb):
		return 1
	}
	return 0
}
