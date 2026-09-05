package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// NodeKey holds the local Ed25519 credential used for Control Plane management auth.
// The private key never leaves the node.
type NodeKey struct {
	Private ed25519.PrivateKey
	Public  ed25519.PublicKey
}

func Generate() (*NodeKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &NodeKey{Private: priv, Public: pub}, nil
}

func LoadOrCreate(path string) (*NodeKey, bool, error) {
	if b, err := os.ReadFile(path); err == nil {
		k, err := ParsePEM(b)
		return k, false, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	k, err := Generate()
	if err != nil {
		return nil, false, err
	}
	if err := k.Save(path); err != nil {
		return nil, false, err
	}
	return k, true, nil
}

func (k *NodeKey) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	block := &pem.Block{Type: "NYXVEIL NODE PRIVATE KEY", Bytes: k.Private}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, pem.EncodeToMemory(block), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ParsePEM(data []byte) (*NodeKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("identity: missing PEM block")
	}
	if len(block.Bytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("identity: unexpected key size %d", len(block.Bytes))
	}
	priv := ed25519.PrivateKey(append([]byte(nil), block.Bytes...))
	return &NodeKey{Private: priv, Public: priv.Public().(ed25519.PublicKey)}, nil
}

// PublicIdentity returns a 32-byte stable public identity (same as credential public key for 1.0.0).
func (k *NodeKey) PublicIdentity() []byte {
	out := make([]byte, ed25519.PublicKeySize)
	copy(out, k.Public)
	return out
}
