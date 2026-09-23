package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T) ed25519.PrivateKey {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	old := publicKeyB64
	SetPublicKeyForTesting(pub)
	t.Cleanup(func() { publicKeyB64 = old })
	return priv
}

func issue(t *testing.T, priv ed25519.PrivateKey, l License) string {
	k, err := Sign(l, priv)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestLicenseLifecycle(t *testing.T) {
	priv := testKey(t)
	now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	exp := now.Add(365 * 24 * time.Hour)
	key := issue(t, priv, License{ID: "l1", Customer: "Acme", Plan: "pro", MaxUsers: 50, Features: []string{FeatureSSO}, IssuedAt: now.Format(time.RFC3339), ExpiresAt: exp.Format(time.RFC3339)})

	st := Resolve(key, "", now)
	if st.Source != "license" || st.Plan.ID != "pro" || st.MaxServers != 10 || st.MaxUsers != 50 || !st.Has(FeatureSSO) || !st.Has(FeatureRegistries) || st.Has(FeatureApprovals) {
		t.Fatalf("pro license %+v", st)
	}
	// Line breaks from copy/paste are tolerated.
	if _, err := Parse(key[:20] + "\n  " + key[20:]); err != nil {
		t.Fatal(err)
	}
	// Grace period, then fallback to Community (never an error).
	if st := Resolve(key, "", exp.Add(24*time.Hour)); !st.InGrace || st.Plan.ID != "pro" {
		t.Fatalf("grace %+v", st)
	}
	if st := Resolve(key, "", exp.Add(GracePeriod+time.Hour)); !st.Expired || st.Plan.ID != "community" || !strings.Contains(st.Message, "expired") {
		t.Fatalf("expired %+v", st)
	}
	// Tampering with the payload breaks the signature.
	parts := strings.Split(key, ".")
	forged := issue(t, priv, License{Plan: "enterprise", Customer: "Acme", ExpiresAt: exp.Format(time.RFC3339)})
	if _, err := Parse(parts[0] + "." + strings.Split(forged, ".")[1] + "." + parts[2]); err != ErrSignature {
		t.Fatalf("forged payload: %v", err)
	}
	// A key signed by someone else is rejected.
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := Parse(issue(t, other, License{Plan: "enterprise", ExpiresAt: exp.Format(time.RFC3339)})); err != ErrSignature {
		t.Fatalf("foreign key: %v", err)
	}
	if st := Resolve("garbage", "", now); st.Plan.ID != "community" || st.Message == "" {
		t.Fatalf("invalid key %+v", st)
	}
}

func TestTrial(t *testing.T) {
	now := time.Now()
	if st := Resolve("", now.Add(-24*time.Hour).Format(time.RFC3339), now); st.Source != "trial" || !st.Has(FeatureApprovals) || st.MaxServers != 0 {
		t.Fatalf("trial %+v", st)
	}
	if st := Resolve("", now.Add(-31*24*time.Hour).Format(time.RFC3339), now); st.Source != "community" {
		t.Fatalf("ended trial %+v", st)
	}
	if st := Resolve("", "", now); st.MaxServers != 2 || st.MaxUsers != 5 || len(st.Features) != 0 {
		t.Fatalf("community %+v", st)
	}
}

func TestLegacyPrefix(t *testing.T) {
	priv := testKey(t)
	key := issue(t, priv, License{Customer: "Old", Plan: "enterprise", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)})
	if !strings.HasPrefix(key, "PD1.") {
		t.Fatal(key[:4])
	}
	// Re-sign the same payload the way pre-rename keys were built.
	body := strings.Split(strings.TrimPrefix(key, "PD1."), ".")[0]
	sig := ed25519.Sign(priv, []byte("SY1."+body))
	old := "SY1." + body + "." + base64.RawURLEncoding.EncodeToString(sig)
	if l, err := Parse(old); err != nil || l.Customer != "Old" {
		t.Fatalf("legacy key rejected: %v", err)
	}
	if _, err := Parse("SY1." + body + "." + strings.Split(key, ".")[2]); err != ErrSignature {
		t.Fatal("prefix is part of the signature")
	}
}
