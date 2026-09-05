package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	secretPrefixEnc  = "nvp1:"  // legacy reversible (read/match only)
	secretPrefixHMAC = "hmac1:" // production verifier — secret not recoverable
)

func parseLicenseKEK(raw string) []byte {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if len(raw) == 64 {
		b, err := hex.DecodeString(raw)
		if err == nil && len(b) == chacha20poly1305.KeySize {
			return b
		}
	}
	if len(raw) == chacha20poly1305.KeySize {
		return []byte(raw)
	}
	return nil
}

// wrapSecret stores a high-entropy license token as an HMAC verifier when KEK is set.
// Raw secret is not recoverable from the verifier.
func (s *Store) wrapSecret(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if s == nil {
		return "", fmt.Errorf("store is nil")
	}
	if len(s.kek) != chacha20poly1305.KeySize {
		if s.requireKEK {
			return "", fmt.Errorf("refusing to store plaintext license secret: production store requires KEK")
		}
		return plain, nil
	}
	mac := hmac.New(sha256.New, s.kek)
	_, _ = mac.Write([]byte(plain))
	return secretPrefixHMAC + hex.EncodeToString(mac.Sum(nil)), nil
}

// unwrapSecret returns stored verifier/ciphertext material (never recovers HMAC plaintext).
func (s *Store) unwrapSecret(stored string) (string, error) {
	if strings.HasPrefix(stored, secretPrefixHMAC) || strings.HasPrefix(stored, secretPrefixEnc) {
		return stored, nil
	}
	return stored, nil
}

// MatchSecret verifies a candidate license secret against stored verifier/ciphertext/plaintext.
func (s *Store) MatchSecret(stored, candidate string) (bool, error) {
	if stored == "" || candidate == "" {
		return false, nil
	}
	if strings.HasPrefix(stored, secretPrefixHMAC) {
		if s == nil || len(s.kek) != chacha20poly1305.KeySize {
			return false, fmt.Errorf("HMAC license verifier requires NVP_LICENSE_KEK")
		}
		want, err := hex.DecodeString(strings.TrimPrefix(stored, secretPrefixHMAC))
		if err != nil {
			return false, fmt.Errorf("corrupt license verifier")
		}
		mac := hmac.New(sha256.New, s.kek)
		_, _ = mac.Write([]byte(candidate))
		return subtle.ConstantTimeCompare(want, mac.Sum(nil)) == 1, nil
	}
	if strings.HasPrefix(stored, secretPrefixEnc) {
		plain, err := s.decryptLegacySecret(stored)
		if err != nil {
			return false, err
		}
		return subtle.ConstantTimeCompare([]byte(plain), []byte(candidate)) == 1, nil
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(candidate)) == 1, nil
}

func (s *Store) decryptLegacySecret(stored string) (string, error) {
	if s == nil || len(s.kek) != chacha20poly1305.KeySize {
		return "", fmt.Errorf("license secret is encrypted but NVP_LICENSE_KEK is not set")
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(stored, secretPrefixEnc))
	if err != nil {
		return "", fmt.Errorf("corrupt license secret")
	}
	aead, err := chacha20poly1305.New(s.kek)
	if err != nil {
		return "", err
	}
	ns := aead.NonceSize()
	if len(raw) < ns+aead.Overhead() {
		return "", fmt.Errorf("corrupt license secret")
	}
	pt, err := aead.Open(nil, raw[:ns], raw[ns:], []byte("nvp/1/license-secret"))
	if err != nil {
		return "", fmt.Errorf("license secret decrypt failed")
	}
	return string(pt), nil
}
