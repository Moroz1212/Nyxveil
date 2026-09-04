package device

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// Identity represents a client device identity.
type Identity struct {
	DeviceID   string
	PublicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
}

// Generate creates a new device identity with Ed25519 keypair.
// Private key never leaves the device in production flows.
func Generate() (*Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate device key: %w", err)
	}
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, err
	}
	return &Identity{
		DeviceID:   "dev_" + hex.EncodeToString(idBytes),
		PublicKey:  pub,
		privateKey: priv,
	}, nil
}

// Sign signs data with device private key.
func (d *Identity) Sign(data []byte) []byte {
	return ed25519.Sign(d.privateKey, data)
}

// Verify verifies a signature with device public key.
func Verify(pub ed25519.PublicKey, data, sig []byte) bool {
	return ed25519.Verify(pub, data, sig)
}
