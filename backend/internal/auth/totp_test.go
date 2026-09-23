package auth

import (
	"strings"
	"testing"
	"time"
)

func TestTOTPRFC6238(t *testing.T) {
	// RFC 6238 appendix B, SHA-1 seed "12345678901234567890" (8-digit vectors truncated to 6).
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	for ts, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		if got := TOTPCode(secret, time.Unix(ts, 0)); got != want {
			t.Errorf("t=%d: got %s want %s", ts, got, want)
		}
	}
}

func TestVerifyTOTP(t *testing.T) {
	s := NewTOTPSecret()
	now := time.Now()
	step, ok := VerifyTOTP(s, TOTPCode(s, now.Add(-30*time.Second)), now)
	if !ok || step != now.Unix()/30-1 {
		t.Fatalf("previous-step code should verify, got %v %d", ok, step)
	}
	if _, ok := VerifyTOTP(s, TOTPCode(s, now.Add(-90*time.Second)), now); ok {
		t.Fatal("stale code accepted")
	}
	if _, ok := VerifyTOTP(s, "abc", now); ok {
		t.Fatal("garbage accepted")
	}
	if !strings.HasPrefix(TOTPURL("Shipyard", "a@b.c", s), "otpauth://totp/Shipyard:a@b.c?") {
		t.Fatal(TOTPURL("Shipyard", "a@b.c", s))
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, stored := NewRecoveryCodes(10)
	if len(codes) != 10 || RecoveryCodesLeft(stored) != 10 {
		t.Fatal("expected 10 codes")
	}
	rest, ok := UseRecoveryCode(stored, strings.ToUpper(strings.ReplaceAll(codes[3], "-", "")))
	if !ok || RecoveryCodesLeft(rest) != 9 {
		t.Fatal("code should be consumed once")
	}
	if _, ok := UseRecoveryCode(rest, codes[3]); ok {
		t.Fatal("code reused")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(3, time.Minute)
	for range 3 {
		l.Fail("k")
	}
	if _, blocked := l.Blocked("k"); !blocked {
		t.Fatal("should be blocked")
	}
	l.Reset("k")
	if _, blocked := l.Blocked("k"); blocked {
		t.Fatal("should be reset")
	}
}
