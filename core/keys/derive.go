package keys

import (
	"crypto/rand"
	"fmt"

	"crypto/sha256"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// Domain separation labels for HKDF.
const (
	LabelClientToServer = "nvp/1/c2s"
	LabelServerToClient = "nvp/1/s2c"
	LabelBootstrap      = "nvp/1/bootstrap"
)

// SessionKeys holds directional AEAD keys for a session epoch.
type SessionKeys struct {
	Epoch          uint32
	ClientToServer []byte
	ServerToClient []byte
}

// EphemeralKeypair holds an X25519 ephemeral keypair.
type EphemeralKeypair struct {
	Private [32]byte
	Public  [32]byte
}

// GenerateEphemeral generates a fresh X25519 keypair.
func GenerateEphemeral() (*EphemeralKeypair, error) {
	var kp EphemeralKeypair
	if _, err := rand.Read(kp.Private[:]); err != nil {
		return nil, fmt.Errorf("generate ephemeral private key: %w", err)
	}
	curve25519.ScalarBaseMult(&kp.Public, &kp.Private)
	return &kp, nil
}

// SharedSecret computes X25519 shared secret from our private key and peer public key.
func SharedSecret(private *[32]byte, peerPublic *[32]byte) ([32]byte, error) {
	var shared [32]byte
	curve25519.ScalarMult(&shared, private, peerPublic)
	// All-zero shared secret indicates invalid peer key.
	var zero [32]byte
	if shared == zero {
		return shared, fmt.Errorf("invalid peer public key: low-order point")
	}
	return shared, nil
}

// DeriveSessionKeys derives directional AEAD keys from shared secret and transcript.
func DeriveSessionKeys(sharedSecret [32]byte, transcript []byte, epoch uint32) (*SessionKeys, error) {
	epochBytes := []byte{
		byte(epoch >> 24), byte(epoch >> 16), byte(epoch >> 8), byte(epoch),
	}
	base := append(append([]byte(nil), sharedSecret[:]...), transcript...)
	base = append(base, epochBytes...)

	c2s, err := deriveKey(base, LabelClientToServer)
	if err != nil {
		return nil, err
	}
	s2c, err := deriveKey(base, LabelServerToClient)
	if err != nil {
		return nil, err
	}
	return &SessionKeys{
		Epoch:          epoch,
		ClientToServer: c2s,
		ServerToClient: s2c,
	}, nil
}

func deriveKey(base []byte, label string) ([]byte, error) {
	r := hkdf.New(sha256.New, base, nil, []byte(label))
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := r.Read(key); err != nil {
		return nil, fmt.Errorf("hkdf derive %s: %w", label, err)
	}
	return key, nil
}

// DeriveBootstrapKey derives a temporary key from TLS exporter material for initial handshake protection.
func DeriveBootstrapKey(exporterSecret []byte, label string) ([]byte, error) {
	r := hkdf.New(sha256.New, exporterSecret, nil, []byte(label))
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := r.Read(key); err != nil {
		return nil, fmt.Errorf("hkdf bootstrap: %w", err)
	}
	return key, nil
}
