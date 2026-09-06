package keys

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
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

// Zero overwrites key material slices. Go GC may retain copies; this is best-effort.
func (sk *SessionKeys) Zero() {
	if sk == nil {
		return
	}
	for i := range sk.ClientToServer {
		sk.ClientToServer[i] = 0
	}
	for i := range sk.ServerToClient {
		sk.ServerToClient[i] = 0
	}
	sk.ClientToServer = nil
	sk.ServerToClient = nil
}

// EphemeralKeypair holds an X25519 ephemeral keypair.
type EphemeralKeypair struct {
	Private [32]byte
	Public  [32]byte
	priv    *ecdh.PrivateKey
}

// GenerateEphemeral generates a fresh X25519 keypair.
func GenerateEphemeral() (*EphemeralKeypair, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ephemeral private key: %w", err)
	}
	var kp EphemeralKeypair
	copy(kp.Private[:], priv.Bytes())
	copy(kp.Public[:], priv.PublicKey().Bytes())
	kp.priv = priv
	return &kp, nil
}

// SharedSecret computes X25519 shared secret from our private key and peer public key.
func SharedSecret(private *[32]byte, peerPublic *[32]byte) ([32]byte, error) {
	curve := ecdh.X25519()
	priv, err := curve.NewPrivateKey(private[:])
	if err != nil {
		return [32]byte{}, fmt.Errorf("invalid private key: %w", err)
	}
	pub, err := curve.NewPublicKey(peerPublic[:])
	if err != nil {
		return [32]byte{}, fmt.Errorf("invalid peer public key: %w", err)
	}
	raw, err := priv.ECDH(pub)
	if err != nil {
		return [32]byte{}, err
	}
	var shared [32]byte
	copy(shared[:], raw)
	var zero [32]byte
	if shared == zero {
		return shared, fmt.Errorf("invalid peer public key: low-order point")
	}
	return shared, nil
}

// ZeroPrivate overwrites ephemeral private key material.
func (k *EphemeralKeypair) ZeroPrivate() {
	if k == nil {
		return
	}
	for i := range k.Private {
		k.Private[i] = 0
	}
	k.priv = nil
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
