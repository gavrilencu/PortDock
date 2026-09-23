// Package registry talks to OCI/Docker container registries: credential checks
// over the v2 API and short-lived AWS ECR tokens (SigV4, no SDK).
package registry

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/shipyard/shipyard/internal/store"
)

var httpClient = &http.Client{Timeout: 20 * time.Second}

// Kind describes a registry flavour for the setup form.
type Kind struct {
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	Host          string `json:"host"` // default host; empty = user supplied
	UsernameLabel string `json:"username_label"`
	PasswordLabel string `json:"password_label"`
	Hint          string `json:"hint"`
}

var Kinds = []Kind{
	{Kind: "dockerhub", Name: "Docker Hub", Host: "docker.io", UsernameLabel: "Username", PasswordLabel: "Access token",
		Hint: "Account settings → Personal access tokens (Read & Write). The namespace is your username or organization."},
	{Kind: "ghcr", Name: "GitHub Container Registry", Host: "ghcr.io", UsernameLabel: "GitHub username", PasswordLabel: "Personal access token",
		Hint: "Classic token with write:packages. The namespace is your user or organization, e.g. acme."},
	{Kind: "gitlab", Name: "GitLab Container Registry", Host: "registry.gitlab.com", UsernameLabel: "Username or deploy token user", PasswordLabel: "Token",
		Hint: "Deploy token with read_registry + write_registry. The namespace is group/project."},
	{Kind: "ecr", Name: "Amazon ECR", Host: "", UsernameLabel: "Access key ID", PasswordLabel: "Secret access key",
		Hint: "Host is <account>.dkr.ecr.<region>.amazonaws.com. The IAM user needs ecr:GetAuthorizationToken and push/pull permissions; tokens are refreshed on every deploy. Create the repository in ECR first."},
	{Kind: "harbor", Name: "Harbor", Host: "", UsernameLabel: "Username or robot account", PasswordLabel: "Password or secret",
		Hint: "Use a robot account with push and pull permissions. The namespace is the Harbor project."},
	{Kind: "quay", Name: "Quay.io", Host: "quay.io", UsernameLabel: "Robot account", PasswordLabel: "Token", Hint: "The namespace is your organization."},
	{Kind: "generic", Name: "Other registry", Host: "", UsernameLabel: "Username", PasswordLabel: "Password or token",
		Hint: "Any registry implementing the Docker Registry HTTP API v2 (Azure ACR, Google Artifact Registry, Nexus, self-hosted)."},
}

func KindOf(kind string) (Kind, bool) {
	for _, k := range Kinds {
		if k.Kind == kind {
			return k, true
		}
	}
	return Kind{}, false
}

var hostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$`)

// NormalizeHost strips schemes and paths ("https://ghcr.io/" → "ghcr.io").
func NormalizeHost(h string) (string, error) {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
	h = strings.TrimSuffix(h, "/")
	if i := strings.Index(h, "/"); i >= 0 {
		h = h[:i]
	}
	if !hostRe.MatchString(h) {
		return "", fmt.Errorf("%q is not a valid registry host", h)
	}
	return h, nil
}

var repoSegment = regexp.MustCompile(`[^a-z0-9._-]+`)

// Slug turns an application name into a repository name component.
func Slug(name string) string {
	s := strings.Trim(repoSegment.ReplaceAllString(strings.ToLower(name), "-"), "-._")
	if s == "" {
		s = "app"
	}
	return s
}

// Repository is the image reference without tag: host/namespace/name.
func Repository(reg store.Registry, override, appName string) string {
	repo := strings.Trim(strings.ToLower(override), "/")
	if repo == "" {
		repo = path.Join(strings.Trim(strings.ToLower(reg.Namespace), "/"), Slug(appName))
	}
	return reg.Host + "/" + repo
}

// Credentials returns the username/password for `docker login`. ECR exchanges
// the IAM access key for a 12-hour registry token.
func Credentials(ctx context.Context, reg store.Registry, secret string) (string, string, error) {
	if reg.Kind == "ecr" {
		return ecrToken(ctx, reg.Host, reg.Username, secret, time.Now())
	}
	return reg.Username, secret, nil
}

// apiHost maps the logical Docker Hub name to its API endpoint.
func apiHost(host string) string {
	if host == "docker.io" || host == "index.docker.io" {
		return "registry-1.docker.io"
	}
	return host
}

// Ping verifies the credentials against the registry's /v2/ endpoint,
// following the bearer-token challenge when the registry uses one.
func Ping(ctx context.Context, reg store.Registry, secret string) error {
	user, pass, err := Credentials(ctx, reg, secret)
	if err != nil {
		return err
	}
	scheme := "https://"
	if h := strings.Split(reg.Host, ":")[0]; h == "localhost" || h == "127.0.0.1" {
		scheme = "http://" // Docker treats local registries as insecure (plain HTTP)
	}
	base := scheme + apiHost(reg.Host) + "/v2/"
	res, err := get(ctx, base, user, pass, "")
	if err != nil {
		return fmt.Errorf("registry unreachable: %w", err)
	}
	res.Body.Close()
	switch res.StatusCode {
	case 200:
		return nil
	case 401:
	default:
		return fmt.Errorf("unexpected response from %s: HTTP %d", base, res.StatusCode)
	}
	challenge := res.Header.Get("Www-Authenticate")
	authScheme, params := parseChallenge(challenge)
	switch authScheme {
	case "basic":
		return errors.New("the registry rejected these credentials")
	case "bearer":
		realm := params["realm"]
		if realm == "" {
			return errors.New("registry sent a bearer challenge without a realm")
		}
		u, err := url.Parse(realm)
		if err != nil {
			return err
		}
		qv := u.Query()
		if s := params["service"]; s != "" {
			qv.Set("service", s)
		}
		if reg.Namespace != "" && reg.Kind != "ecr" {
			// Asking for a scope makes GHCR/Docker Hub validate the account, not just issue an anonymous token.
			qv.Set("scope", "repository:"+strings.Trim(reg.Namespace, "/")+"/shipyard-check:pull")
		}
		u.RawQuery = qv.Encode()
		tr, err := get(ctx, u.String(), user, pass, "")
		if err != nil {
			return fmt.Errorf("token endpoint unreachable: %w", err)
		}
		defer tr.Body.Close()
		if tr.StatusCode == 401 || tr.StatusCode == 403 {
			return errors.New("the registry rejected these credentials")
		}
		if tr.StatusCode != 200 {
			body, _ := io.ReadAll(io.LimitReader(tr.Body, 300))
			return fmt.Errorf("token request failed: HTTP %d %s", tr.StatusCode, strings.TrimSpace(string(body)))
		}
		var tok struct {
			Token       string `json:"token"`
			AccessToken string `json:"access_token"`
		}
		_ = json.NewDecoder(tr.Body).Decode(&tok)
		bearer := tok.Token
		if bearer == "" {
			bearer = tok.AccessToken
		}
		if bearer == "" {
			return errors.New("the registry did not issue a token")
		}
		vr, err := get(ctx, base, "", "", bearer)
		if err != nil {
			return err
		}
		vr.Body.Close()
		if vr.StatusCode == 200 || (vr.StatusCode == 401 && reg.Namespace != "") {
			// Some registries (GHCR) only accept scoped tokens on repository endpoints.
			return nil
		}
		return fmt.Errorf("registry refused the issued token: HTTP %d", vr.StatusCode)
	}
	return fmt.Errorf("unsupported authentication challenge %q", challenge)
}

func get(ctx context.Context, u, user, pass, bearer string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	} else if user != "" || pass != "" {
		req.SetBasicAuth(user, pass)
	}
	return httpClient.Do(req)
}

// parseChallenge parses `Bearer realm="…",service="…"`.
func parseChallenge(h string) (string, map[string]string) {
	scheme, rest, _ := strings.Cut(strings.TrimSpace(h), " ")
	params := map[string]string{}
	for _, m := range regexp.MustCompile(`(\w+)="([^"]*)"`).FindAllStringSubmatch(rest, -1) {
		params[strings.ToLower(m[1])] = m[2]
	}
	return strings.ToLower(scheme), params
}

// ---- AWS ECR ----

var ecrHostRe = regexp.MustCompile(`^(\d{12})\.dkr\.ecr(-fips)?\.([a-z0-9-]+)\.amazonaws\.com(\.cn)?$`)

// ECRRegion extracts the region from an ECR registry host.
func ECRRegion(host string) (string, error) {
	m := ecrHostRe.FindStringSubmatch(host)
	if m == nil {
		return "", fmt.Errorf("%s is not an ECR host (<account>.dkr.ecr.<region>.amazonaws.com)", host)
	}
	return m[3], nil
}

// ecrEndpoint is overridable in tests.
var ecrEndpoint = func(region string) string { return "https://api.ecr." + region + ".amazonaws.com/" }

func ecrToken(ctx context.Context, host, accessKey, secretKey string, now time.Time) (string, string, error) {
	region, err := ECRRegion(host)
	if err != nil {
		return "", "", err
	}
	endpoint := ecrEndpoint(region)
	body := []byte("{}")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AmazonEC2ContainerRegistry_V20150921.GetAuthorizationToken")
	SignV4(req, body, accessKey, secretKey, region, "ecr", now)
	res, err := httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("ECR unreachable: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	if res.StatusCode != 200 {
		var e struct {
			Message string `json:"message"`
			Type    string `json:"__type"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Message == "" {
			e.Message = strings.TrimSpace(string(raw))
		}
		return "", "", fmt.Errorf("ECR rejected the credentials: %s", e.Message)
	}
	var out struct {
		AuthorizationData []struct {
			AuthorizationToken string `json:"authorizationToken"`
		} `json:"authorizationData"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.AuthorizationData) == 0 {
		return "", "", errors.New("ECR returned no authorization data")
	}
	dec, err := base64.StdEncoding.DecodeString(out.AuthorizationData[0].AuthorizationToken)
	if err != nil {
		return "", "", errors.New("ECR returned a malformed token")
	}
	user, pass, ok := strings.Cut(string(dec), ":")
	if !ok {
		return "", "", errors.New("ECR returned a malformed token")
	}
	return user, pass, nil
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// SignV4 adds AWS Signature Version 4 headers to req (single-chunk payload).
func SignV4(req *http.Request, body []byte, accessKey, secretKey, region, service string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	host := req.URL.Host
	payload := sha256Hex(body)

	names := []string{"content-type", "host", "x-amz-date", "x-amz-target"}
	values := map[string]string{
		"content-type": req.Header.Get("Content-Type"),
		"host":         host,
		"x-amz-date":   amzDate,
		"x-amz-target": req.Header.Get("X-Amz-Target"),
	}
	var canonHeaders strings.Builder
	var signed []string
	for _, n := range names {
		if values[n] == "" {
			continue
		}
		canonHeaders.WriteString(n + ":" + strings.TrimSpace(values[n]) + "\n")
		signed = append(signed, n)
	}
	signedHeaders := strings.Join(signed, ";")
	uri := req.URL.EscapedPath()
	if uri == "" {
		uri = "/"
	}
	canonical := strings.Join([]string{req.Method, uri, req.URL.RawQuery, canonHeaders.String(), signedHeaders, payload}, "\n")
	scope := date + "/" + region + "/" + service + "/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonical))
	key := hmacSHA256([]byte("AWS4"+secretKey), date)
	key = hmacSHA256(key, region)
	key = hmacSHA256(key, service)
	key = hmacSHA256(key, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(key, toSign))
	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", accessKey, scope, signedHeaders, sig))
}
