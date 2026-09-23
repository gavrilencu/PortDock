// Package sso implements OpenID Connect single sign-on (authorization code flow
// with PKCE) against Microsoft Entra ID, Okta, Google, Keycloak, Authentik,
// GitLab or any standards-compliant provider.
package sso

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var httpClient = &http.Client{Timeout: 15 * time.Second}

// Metadata is the subset of the discovery document PortDock uses.
type Metadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
}

type cached struct {
	meta Metadata
	at   time.Time
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*cached{}
)

func getJSON(ctx context.Context, u string, bearer string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != 200 {
		return fmt.Errorf("GET %s: HTTP %d: %s", u, res.StatusCode, truncate(string(body), 200))
	}
	return json.Unmarshal(body, v)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// Discover fetches (and caches for an hour) the provider's discovery document.
func Discover(ctx context.Context, issuer string) (Metadata, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	cacheMu.Lock()
	c := cache[issuer]
	cacheMu.Unlock()
	if c != nil && time.Since(c.at) < time.Hour {
		return c.meta, nil
	}
	var m Metadata
	if err := getJSON(ctx, issuer+"/.well-known/openid-configuration", "", &m); err != nil {
		return Metadata{}, fmt.Errorf("discovery failed: %w", err)
	}
	if m.AuthorizationEndpoint == "" || m.TokenEndpoint == "" || m.JWKSURI == "" {
		return Metadata{}, errors.New("discovery document is missing required endpoints")
	}
	cacheMu.Lock()
	cache[issuer] = &cached{meta: m, at: time.Now()}
	cacheMu.Unlock()
	return m, nil
}

// PKCE returns a code verifier and its S256 challenge.
func PKCE(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthURL builds the authorization request URL.
func AuthURL(m Metadata, clientID, redirectURI, scopes, state, nonce, verifier string) string {
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", clientID)
	v.Set("redirect_uri", redirectURI)
	v.Set("scope", scopes)
	v.Set("state", state)
	v.Set("nonce", nonce)
	v.Set("code_challenge", PKCE(verifier))
	v.Set("code_challenge_method", "S256")
	sep := "?"
	if strings.Contains(m.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return m.AuthorizationEndpoint + sep + v.Encode()
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

// Claims are the identity attributes PortDock maps to a user.
type Claims struct {
	Subject       string
	Email         string
	EmailVerified *bool
	Name          string
	Groups        []string
	Raw           map[string]any
}

// Exchange redeems the authorization code, verifies the ID token and merges
// userinfo claims.
func Exchange(ctx context.Context, m Metadata, clientID, clientSecret, redirectURI, code, verifier, nonce, groupsClaim string) (Claims, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	form.Set("code_verifier", verifier)
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Claims{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := httpClient.Do(req)
	if err != nil {
		return Claims{}, fmt.Errorf("token request failed: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var tr tokenResponse
	_ = json.Unmarshal(body, &tr)
	if res.StatusCode != 200 || tr.IDToken == "" {
		msg := tr.Description
		if msg == "" {
			msg = tr.Error
		}
		if msg == "" {
			msg = truncate(string(body), 200)
		}
		return Claims{}, fmt.Errorf("token exchange failed (HTTP %d): %s", res.StatusCode, msg)
	}
	raw, err := VerifyIDToken(ctx, m, tr.IDToken, clientID, nonce, time.Now())
	if err != nil {
		return Claims{}, err
	}
	if m.UserinfoEndpoint != "" && tr.AccessToken != "" {
		var info map[string]any
		if err := getJSON(ctx, m.UserinfoEndpoint, tr.AccessToken, &info); err == nil && info["sub"] == raw["sub"] {
			for k, v := range info {
				if _, exists := raw[k]; !exists {
					raw[k] = v
				}
			}
		}
	}
	return ClaimsFrom(raw, groupsClaim), nil
}

// ClaimsFrom maps raw token claims; Entra ID uses preferred_username/upn when email is absent.
func ClaimsFrom(raw map[string]any, groupsClaim string) Claims {
	str := func(k string) string { s, _ := raw[k].(string); return s }
	c := Claims{Subject: str("sub"), Email: strings.ToLower(str("email")), Name: str("name"), Raw: raw}
	if c.Email == "" {
		for _, k := range []string{"preferred_username", "upn"} {
			if v := str(k); strings.Contains(v, "@") {
				c.Email = strings.ToLower(v)
				break
			}
		}
	}
	switch v := raw["email_verified"].(type) {
	case bool:
		c.EmailVerified = &v
	case string: // some providers (AWS Cognito) send "true"/"false"
		b := v == "true"
		c.EmailVerified = &b
	}
	if c.Name == "" {
		c.Name = strings.TrimSpace(str("given_name") + " " + str("family_name"))
	}
	if groupsClaim == "" {
		groupsClaim = "groups"
	}
	switch g := raw[groupsClaim].(type) {
	case []any:
		for _, x := range g {
			if s, ok := x.(string); ok {
				c.Groups = append(c.Groups, s)
			}
		}
	case string:
		for _, s := range strings.FieldsFunc(g, func(r rune) bool { return r == ',' || r == ' ' }) {
			c.Groups = append(c.Groups, s)
		}
	}
	return c
}

// VerifyIDToken checks the JWS signature (RS256/384/512, PS256, ES256/384) with
// the provider's JWKS plus iss, aud, exp and nonce. It returns the claims.
func VerifyIDToken(ctx context.Context, m Metadata, token, clientID, nonce string, now time.Time) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed ID token")
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeSegment(parts[0], &hdr); err != nil {
		return nil, fmt.Errorf("ID token header: %w", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("ID token signature is not base64url")
	}
	key, err := publicKey(ctx, m, hdr.Kid)
	if err != nil {
		return nil, err
	}
	if err := verifySignature(hdr.Alg, key, []byte(parts[0]+"."+parts[1]), sig); err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := decodeSegment(parts[1], &claims); err != nil {
		return nil, fmt.Errorf("ID token claims: %w", err)
	}
	if iss, _ := claims["iss"].(string); m.Issuer != "" && !issuerMatches(m.Issuer, iss, claims) {
		return nil, fmt.Errorf("ID token issuer %q does not match %q", iss, m.Issuer)
	}
	if !audienceContains(claims["aud"], clientID) {
		return nil, errors.New("ID token audience does not include this client ID")
	}
	exp, _ := claims["exp"].(float64)
	if exp == 0 || now.After(time.Unix(int64(exp), 0).Add(2*time.Minute)) {
		return nil, errors.New("ID token has expired — check the server clock")
	}
	if n, _ := claims["nonce"].(string); n != nonce {
		return nil, errors.New("ID token nonce mismatch")
	}
	if sub, _ := claims["sub"].(string); sub == "" {
		return nil, errors.New("ID token has no subject")
	}
	return claims, nil
}

// issuerMatches handles Entra ID multi-tenant metadata, whose issuer contains a {tenantid} template.
func issuerMatches(expected, got string, claims map[string]any) bool {
	if expected == got {
		return true
	}
	if tid, _ := claims["tid"].(string); tid != "" && strings.Contains(expected, "{tenantid}") {
		return strings.ReplaceAll(expected, "{tenantid}", tid) == got
	}
	return false
}

func audienceContains(aud any, clientID string) bool {
	switch a := aud.(type) {
	case string:
		return a == clientID
	case []any:
		for _, x := range a {
			if s, _ := x.(string); s == clientID {
				return true
			}
		}
	}
	return false
}

func decodeSegment(seg string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(seg, "="))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func verifySignature(alg string, key crypto.PublicKey, signed, sig []byte) error {
	if len(alg) != 5 {
		return fmt.Errorf("unsupported ID token algorithm %q", alg)
	}
	var h hash.Hash
	var ch crypto.Hash
	switch alg[2:] {
	case "256":
		h, ch = sha256.New(), crypto.SHA256
	case "384":
		h, ch = sha512.New384(), crypto.SHA384
	case "512":
		h, ch = sha512.New(), crypto.SHA512
	default:
		return fmt.Errorf("unsupported ID token algorithm %q", alg)
	}
	h.Write(signed)
	digest := h.Sum(nil)
	switch k := key.(type) {
	case *rsa.PublicKey:
		switch {
		case strings.HasPrefix(alg, "RS"):
			if rsa.VerifyPKCS1v15(k, ch, digest, sig) == nil {
				return nil
			}
		case strings.HasPrefix(alg, "PS"):
			if rsa.VerifyPSS(k, ch, digest, sig, nil) == nil {
				return nil
			}
		default:
			return fmt.Errorf("algorithm %s does not match RSA key", alg)
		}
	case *ecdsa.PublicKey:
		if !strings.HasPrefix(alg, "ES") || len(sig)%2 != 0 {
			return fmt.Errorf("algorithm %s does not match EC key", alg)
		}
		r := new(big.Int).SetBytes(sig[:len(sig)/2])
		s := new(big.Int).SetBytes(sig[len(sig)/2:])
		if ecdsa.Verify(k, digest, r, s) {
			return nil
		}
	default:
		return errors.New("unsupported signing key type")
	}
	return errors.New("ID token signature is invalid")
}

type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// publicKey returns the JWKS key for kid, refetching the set on a miss (key
// rotation) at most once a minute.
func publicKey(ctx context.Context, m Metadata, kid string) (crypto.PublicKey, error) {
	pick := func(keys map[string]crypto.PublicKey) crypto.PublicKey {
		if k, ok := keys[kid]; ok {
			return k
		}
		if kid == "" && len(keys) == 1 {
			for _, k := range keys {
				return k
			}
		}
		return nil
	}
	jwksMu.Lock()
	c := jwksCache[m.JWKSURI]
	jwksMu.Unlock()
	if c != nil {
		if k := pick(c.keys); k != nil {
			return k, nil
		}
		if time.Since(c.at) < time.Minute {
			return nil, fmt.Errorf("signing key %q not found in the provider's JWKS", kid)
		}
	}
	fresh, err := fetchJWKS(ctx, m.JWKSURI)
	if err != nil {
		return nil, err
	}
	jwksMu.Lock()
	jwksCache[m.JWKSURI] = &keySet{keys: fresh, at: time.Now()}
	jwksMu.Unlock()
	if k := pick(fresh); k != nil {
		return k, nil
	}
	return nil, fmt.Errorf("signing key %q not found in the provider's JWKS", kid)
}

type keySet struct {
	keys map[string]crypto.PublicKey
	at   time.Time
}

var (
	jwksMu    sync.Mutex
	jwksCache = map[string]*keySet{}
)

func fetchJWKS(ctx context.Context, uri string) (map[string]crypto.PublicKey, error) {
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := getJSON(ctx, uri, "", &set); err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}
	out := map[string]crypto.PublicKey{}
	for _, k := range set.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		switch k.Kty {
		case "RSA":
			n, err1 := base64.RawURLEncoding.DecodeString(k.N)
			e, err2 := base64.RawURLEncoding.DecodeString(k.E)
			if err1 != nil || err2 != nil {
				continue
			}
			out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		case "EC":
			var curve elliptic.Curve
			switch k.Crv {
			case "P-256":
				curve = elliptic.P256()
			case "P-384":
				curve = elliptic.P384()
			default:
				continue
			}
			x, err1 := base64.RawURLEncoding.DecodeString(k.X)
			y, err2 := base64.RawURLEncoding.DecodeString(k.Y)
			if err1 != nil || err2 != nil {
				continue
			}
			out[k.Kid] = &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("JWKS contains no usable signing keys")
	}
	return out, nil
}

// Preset describes a well-known provider for the setup form.
type Preset struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Issuer      string `json:"issuer"` // may contain {tenant} / {domain} placeholders
	Scopes      string `json:"scopes"`
	GroupsClaim string `json:"groups_claim"`
	Docs        string `json:"docs"`
}

var Presets = []Preset{
	{Kind: "entra", Name: "Microsoft Entra ID", Issuer: "https://login.microsoftonline.com/{tenant}/v2.0", Scopes: "openid email profile", GroupsClaim: "groups",
		Docs: "App registrations → New registration → Web redirect URI. Add the 'groups' optional claim to map admins."},
	{Kind: "google", Name: "Google Workspace", Issuer: "https://accounts.google.com", Scopes: "openid email profile", GroupsClaim: "groups",
		Docs: "Google Cloud console → APIs & Services → Credentials → OAuth client ID (Web application)."},
	{Kind: "okta", Name: "Okta", Issuer: "https://{domain}.okta.com", Scopes: "openid email profile groups", GroupsClaim: "groups",
		Docs: "Applications → Create App Integration → OIDC → Web Application."},
	{Kind: "keycloak", Name: "Keycloak", Issuer: "https://{domain}/realms/{realm}", Scopes: "openid email profile", GroupsClaim: "groups",
		Docs: "Clients → Create client → OpenID Connect, Client authentication on. Add a 'groups' mapper to the client scope."},
	{Kind: "authentik", Name: "Authentik", Issuer: "https://{domain}/application/o/{slug}/", Scopes: "openid email profile", GroupsClaim: "groups",
		Docs: "Applications → Providers → OAuth2/OpenID Provider (confidential)."},
	{Kind: "gitlab", Name: "GitLab", Issuer: "https://gitlab.com", Scopes: "openid email profile", GroupsClaim: "groups_direct",
		Docs: "User or group settings → Applications, scopes openid, email, profile."},
	{Kind: "oidc", Name: "OpenID Connect", Issuer: "", Scopes: "openid email profile", GroupsClaim: "groups",
		Docs: "Any OpenID Connect provider supporting the authorization code flow."},
}
