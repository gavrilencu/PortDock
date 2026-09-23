package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/shipyard/shipyard/internal/secrets"
)

// TOTP (RFC 6238): 30-second steps, 6 digits, HMAC-SHA1 — the parameters every
// authenticator app (Google Authenticator, Microsoft Authenticator, 1Password…) supports.
const (
	totpPeriod = 30
	totpDigits = 6
	totpSkew   = 1 // accept one step before/after to tolerate clock drift
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random 160-bit base32 secret.
func NewTOTPSecret() string {
	buf := make([]byte, 20)
	_, _ = rand.Read(buf)
	return b32.EncodeToString(buf)
}

// TOTPURL is the otpauth:// URI encoded in the enrollment QR code.
func TOTPURL(issuer, account, secret string) string {
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", fmt.Sprint(totpDigits))
	v.Set("period", fmt.Sprint(totpPeriod))
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + v.Encode()
}

func totpAt(key []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, code%1_000_000)
}

// TOTPCode returns the code for secret at time t (used by tests).
func TOTPCode(secret string, t time.Time) string {
	key, _ := b32.DecodeString(strings.ToUpper(secret))
	return totpAt(key, t.Unix()/totpPeriod)
}

// VerifyTOTP checks code against secret around time t. It returns the matched
// time step; callers must reject steps <= the last accepted one to prevent replay.
func VerifyTOTP(secret, code string, t time.Time) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0, false
	}
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return 0, false
	}
	now := t.Unix() / totpPeriod
	for d := int64(-totpSkew); d <= totpSkew; d++ {
		if subtle.ConstantTimeCompare([]byte(totpAt(key, now+d)), []byte(code)) == 1 {
			return now + d, true
		}
	}
	return 0, false
}

// NewRecoveryCodes returns n one-time codes (xxxxx-xxxxx) and their stored form.
func NewRecoveryCodes(n int) (codes []string, stored string) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	hashes := make([]string, 0, n)
	for range n {
		buf := make([]byte, 10)
		_, _ = rand.Read(buf)
		for i := range buf {
			buf[i] = alphabet[int(buf[i])%len(alphabet)]
		}
		c := string(buf[:5]) + "-" + string(buf[5:])
		codes = append(codes, c)
		hashes = append(hashes, secrets.Hash(c))
	}
	return codes, strings.Join(hashes, ",")
}

func normalizeRecovery(code string) string {
	c := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), " ", ""))
	if len(c) == 10 && !strings.Contains(c, "-") {
		c = c[:5] + "-" + c[5:]
	}
	return c
}

// UseRecoveryCode consumes code from stored and returns the remaining set.
func UseRecoveryCode(stored, code string) (string, bool) {
	h := secrets.Hash(normalizeRecovery(code))
	var rest []string
	found := false
	for _, s := range strings.Split(stored, ",") {
		if s == "" {
			continue
		}
		if !found && subtle.ConstantTimeCompare([]byte(s), []byte(h)) == 1 {
			found = true
			continue
		}
		rest = append(rest, s)
	}
	return strings.Join(rest, ","), found
}

// RecoveryCodesLeft counts unused recovery codes.
func RecoveryCodesLeft(stored string) int {
	n := 0
	for _, s := range strings.Split(stored, ",") {
		if s != "" {
			n++
		}
	}
	return n
}
