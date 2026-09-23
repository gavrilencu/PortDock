// Package license verifies Ed25519-signed license keys and describes plans.
//
// Key format: "SY1.<base64url payload>.<base64url signature>". The payload is
// JSON (see License). Keys are issued with cmd/shipyard-license using the
// vendor's private key; instances only embed the public key, so keys can be
// verified offline and cannot be forged.
package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// publicKeyB64 is the vendor verification key; override at build time with
// -ldflags "-X github.com/shipyard/shipyard/internal/license.publicKeyB64=...".
var publicKeyB64 = "NWFAyNqhNkLqAAGJ+sqitbd+1OkfVzA0vfgq5ku19EE="

const prefix = "SY1."

// GracePeriod keeps an expired license working while the customer renews.
const GracePeriod = 14 * 24 * time.Hour

// TrialLength is the duration of the one-time Enterprise trial.
const TrialLength = 30 * 24 * time.Hour

// Features that plans unlock.
const (
	FeatureMultiServer    = "multi_server"    // deploy an application to several servers, build servers
	FeatureRegistries     = "registries"      // container registries
	FeatureProjectRoles   = "project_roles"   // project members, private projects, protected environments
	FeatureAuditExport    = "audit_export"    // CSV/JSON export of the audit log
	FeatureSSO            = "sso"             // OpenID Connect single sign-on
	FeatureApprovals      = "approvals"       // deployment approvals (four-eyes)
	FeatureAuditStreaming = "audit_streaming" // SIEM webhook / syslog
	FeatureLDAP           = "ldap"            // LDAP / Active Directory sign-in
)

// Plan describes an edition.
type Plan struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	MaxServers int      `json:"max_servers"` // 0 = unlimited
	MaxUsers   int      `json:"max_users"`
	Features   []string `json:"features"`
}

var Plans = map[string]Plan{
	"community": {ID: "community", Name: "Community", MaxServers: 2, MaxUsers: 5},
	"pro": {ID: "pro", Name: "Pro", MaxServers: 10, MaxUsers: 25,
		Features: []string{FeatureMultiServer, FeatureRegistries, FeatureProjectRoles, FeatureAuditExport}},
	"enterprise": {ID: "enterprise", Name: "Enterprise",
		Features: []string{FeatureMultiServer, FeatureRegistries, FeatureProjectRoles, FeatureAuditExport, FeatureSSO, FeatureApprovals, FeatureAuditStreaming, FeatureLDAP}},
}

// FeatureNames are human-readable names for error messages and the UI.
var FeatureNames = map[string]string{
	FeatureMultiServer: "Multi-server deployments", FeatureRegistries: "Container registries", FeatureProjectRoles: "Project roles & protected environments",
	FeatureAuditExport: "Audit log export", FeatureSSO: "Single sign-on", FeatureApprovals: "Deployment approvals", FeatureAuditStreaming: "Audit log streaming",
	FeatureLDAP: "LDAP / Active Directory",
}

// License is the signed payload.
type License struct {
	ID         string   `json:"id"`
	Customer   string   `json:"customer"`
	Email      string   `json:"email,omitempty"`
	Plan       string   `json:"plan"`
	MaxServers int      `json:"max_servers,omitempty"` // overrides the plan when > 0; -1 = unlimited
	MaxUsers   int      `json:"max_users,omitempty"`
	Features   []string `json:"features,omitempty"` // extra features on top of the plan
	IssuedAt   string   `json:"issued_at"`
	ExpiresAt  string   `json:"expires_at"`
}

var (
	ErrMalformed = errors.New("this is not a Shipyard license key")
	ErrSignature = errors.New("the license key signature is invalid")
)

// SetPublicKeyForTesting replaces the verification key (tests only).
func SetPublicKeyForTesting(pub ed25519.PublicKey) {
	publicKeyB64 = base64.StdEncoding.EncodeToString(pub)
}

func publicKey() (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("invalid embedded license public key")
	}
	return raw, nil
}

// Sign creates a key (vendor side).
func Sign(l License, priv ed25519.PrivateKey) (string, error) {
	payload, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding.EncodeToString(payload)
	sig := ed25519.Sign(priv, []byte(prefix+enc))
	return prefix + enc + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Parse verifies the signature and decodes a key. It does not check expiry.
func Parse(key string) (License, error) {
	return parseWith(key, nil)
}

func parseWith(key string, pub ed25519.PublicKey) (License, error) {
	key = strings.Join(strings.Fields(key), "") // tolerate line breaks from copy/paste
	if !strings.HasPrefix(key, prefix) {
		return License{}, ErrMalformed
	}
	body, sigB64, ok := strings.Cut(strings.TrimPrefix(key, prefix), ".")
	if !ok {
		return License{}, ErrMalformed
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return License{}, ErrMalformed
	}
	if pub == nil {
		if pub, err = publicKey(); err != nil {
			return License{}, err
		}
	}
	if !ed25519.Verify(pub, []byte(prefix+body), sig) {
		return License{}, ErrSignature
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return License{}, ErrMalformed
	}
	var l License
	if err := json.Unmarshal(payload, &l); err != nil {
		return License{}, ErrMalformed
	}
	if _, ok := Plans[l.Plan]; !ok {
		return License{}, fmt.Errorf("unknown plan %q", l.Plan)
	}
	if _, err := time.Parse(time.RFC3339, l.ExpiresAt); err != nil {
		return License{}, errors.New("license has no valid expiry date")
	}
	return l, nil
}

// State is the effective entitlement of an instance.
type State struct {
	Plan       Plan      `json:"plan"`
	Source     string    `json:"source"` // license | trial | community
	License    *License  `json:"license,omitempty"`
	ExpiresAt  time.Time `json:"expires_at"`
	InGrace    bool      `json:"in_grace"` // expired, still honoured
	Expired    bool      `json:"expired"`  // expired beyond grace: fell back to Community
	MaxServers int       `json:"max_servers"`
	MaxUsers   int       `json:"max_users"`
	Features   []string  `json:"features"`
	Message    string    `json:"message,omitempty"`
}

func (s State) Has(feature string) bool {
	for _, f := range s.Features {
		if f == feature {
			return true
		}
	}
	return false
}

func community() State {
	p := Plans["community"]
	return State{Plan: p, Source: "community", MaxServers: p.MaxServers, MaxUsers: p.MaxUsers, Features: []string{}}
}

func fromPlan(p Plan, source string) State {
	return State{Plan: p, Source: source, MaxServers: p.MaxServers, MaxUsers: p.MaxUsers, Features: append([]string{}, p.Features...)}
}

// Resolve computes the state from a stored key and trial start (either may be empty).
func Resolve(key, trialStarted string, now time.Time) State {
	if key != "" {
		l, err := Parse(key)
		if err != nil {
			st := community()
			st.Message = "Installed license is invalid: " + err.Error()
			return st
		}
		exp, _ := time.Parse(time.RFC3339, l.ExpiresAt)
		st := fromPlan(Plans[l.Plan], "license")
		st.License, st.ExpiresAt = &l, exp
		if l.MaxServers != 0 {
			st.MaxServers = max(l.MaxServers, 0) // -1 → 0 (unlimited)
		}
		if l.MaxUsers != 0 {
			st.MaxUsers = max(l.MaxUsers, 0)
		}
		for _, f := range l.Features {
			if !st.Has(f) {
				st.Features = append(st.Features, f)
			}
		}
		switch {
		case now.After(exp.Add(GracePeriod)):
			fallback := community()
			fallback.License, fallback.ExpiresAt, fallback.Expired = &l, exp, true
			fallback.Message = fmt.Sprintf("The %s license of %s expired on %s. Renew it to restore %s features.", st.Plan.Name, l.Customer, exp.Format("2 Jan 2006"), st.Plan.Name)
			return fallback
		case now.After(exp):
			st.InGrace = true
			st.Message = fmt.Sprintf("The license expired on %s. It keeps working until %s — renew it now.", exp.Format("2 Jan 2006"), exp.Add(GracePeriod).Format("2 Jan 2006"))
		case exp.Sub(now) < 30*24*time.Hour:
			st.Message = fmt.Sprintf("The license expires on %s.", exp.Format("2 Jan 2006"))
		}
		return st
	}
	if trialStarted != "" {
		start, err := time.Parse(time.RFC3339, trialStarted)
		if err == nil && now.Before(start.Add(TrialLength)) {
			st := fromPlan(Plans["enterprise"], "trial")
			st.ExpiresAt = start.Add(TrialLength)
			st.Message = fmt.Sprintf("Enterprise trial — %d day(s) left.", int(st.ExpiresAt.Sub(now).Hours()/24)+1)
			return st
		}
		st := community()
		st.Message = "The Enterprise trial has ended."
		return st
	}
	return community()
}
