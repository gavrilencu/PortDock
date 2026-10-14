// Package secrets encrypts sensitive values (env vars, SSH keys, tokens) at rest
// using AES-256-GCM.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const prefix = "enc:v1:"

type Box struct {
	aead cipher.AEAD
	key  []byte
}

// LoadOrCreate returns a Box using the explicit key, or a key persisted in dataDir.
func LoadOrCreate(explicit, dataDir string) (*Box, error) {
	var key []byte
	if explicit != "" {
		k, err := base64.StdEncoding.DecodeString(explicit)
		if err != nil || len(k) != 32 {
			// Accept arbitrary passphrases by hashing them.
			h := sha256.Sum256([]byte(explicit))
			k = h[:]
		}
		key = k
	} else {
		path := filepath.Join(dataDir, "secret.key")
		if b, err := os.ReadFile(path); err == nil {
			k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
			if err != nil || len(k) != 32 {
				return nil, errors.New("secrets: invalid key file " + path)
			}
			key = k
		} else {
			key = make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return nil, err
			}
			if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
				return nil, err
			}
		}
	}
	return NewBox(key)
}

// NewBox returns a Box for a 32-byte key.
func NewBox(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("secrets: the key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead, key: append([]byte(nil), key...)}, nil
}

// Key returns the key, for the instance backup (which carries it encrypted
// with the backup passphrase, so the backup can be restored on a new server).
func (b *Box) Key() []byte { return append([]byte(nil), b.key...) }

// KeyFile renders a key the way secret.key stores it.
func KeyFile(key []byte) []byte { return []byte(base64.StdEncoding.EncodeToString(key)) }

// ParseKeyFile reads the contents of a secret.key file.
func ParseKeyFile(b []byte) ([]byte, error) {
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(k) != 32 {
		return nil, errors.New("secrets: invalid key file")
	}
	return k, nil
}

func (b *Box) Encrypt(plain string) string {
	if plain == "" {
		return ""
	}
	nonce := make([]byte, b.aead.NonceSize())
	_, _ = rand.Read(nonce)
	out := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.RawStdEncoding.EncodeToString(out)
}

func (b *Box) Decrypt(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	if !strings.HasPrefix(enc, prefix) {
		return enc, nil // legacy / plain value
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(enc, prefix))
	if err != nil {
		return "", err
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("secrets: ciphertext too short")
	}
	plain, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// MustDecrypt returns an empty string when decryption fails.
func (b *Box) MustDecrypt(enc string) string {
	s, _ := b.Decrypt(enc)
	return s
}

// RandomToken returns n random bytes hex-encoded.
func RandomToken(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// RandomPassword returns a URL/shell-safe password of the given length.
func RandomPassword(length int) string {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, length)
	_, _ = rand.Read(buf)
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(buf)
}

// Hash returns the hex SHA-256 of s (used for storing API/session tokens).
func Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
