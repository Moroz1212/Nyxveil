package transport

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"fmt"
)

// VerifySPKIPin checks optional SHA-256 pin of peer certificate SPKI.
func VerifySPKIPin(state tls.ConnectionState, pin []byte) error {
	if len(pin) == 0 {
		return nil
	}
	for _, cert := range state.PeerCertificates {
		sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
		if subtle.ConstantTimeCompare(sum[:], pin) == 1 {
			return nil
		}
	}
	return fmt.Errorf("certificate SPKI pin mismatch")
}
