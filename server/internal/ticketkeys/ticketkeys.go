package ticketkeys

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// File stores Control Plane Access Ticket verification public keys (never private).
type File struct {
	Issuer    string            `json:"issuer"`
	Keys      map[string]string `json:"keys"` // kid -> base64 (std) 32-byte public key
	UpdatedAt int64             `json:"updated_at,omitempty"`
}

func Load(path string) (issuer string, keys map[string]ed25519.PublicKey, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return "", nil, err
	}
	out, err := DecodeKeys(f.Keys)
	if err != nil {
		return "", nil, err
	}
	issuer = f.Issuer
	if issuer == "" {
		issuer = "nyxveil-control-plane"
	}
	return issuer, out, nil
}

// DecodeKeys parses kid → std/raw-url base64 Ed25519 public keys.
func DecodeKeys(enc map[string]string) (map[string]ed25519.PublicKey, error) {
	out := make(map[string]ed25519.PublicKey, len(enc))
	for kid, s := range enc {
		raw, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			raw, err = base64.RawURLEncoding.DecodeString(s)
		}
		if err != nil {
			return nil, fmt.Errorf("ticketkeys: key %q: %w", kid, err)
		}
		if len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("ticketkeys: key %q: want %d bytes", kid, ed25519.PublicKeySize)
		}
		out[kid] = ed25519.PublicKey(raw)
	}
	return out, nil
}

// EncodeKeys maps public keys to standard base64 for persistence/API.
func EncodeKeys(keys map[string]ed25519.PublicKey) map[string]string {
	out := make(map[string]string, len(keys))
	for kid, pub := range keys {
		out[kid] = base64.StdEncoding.EncodeToString(pub)
	}
	return out
}

// Save writes issuer + keys atomically (0600).
func Save(path, issuer string, keys map[string]ed25519.PublicKey, updatedAt int64) error {
	if issuer == "" {
		issuer = "nyxveil-control-plane"
	}
	if updatedAt == 0 {
		updatedAt = time.Now().Unix()
	}
	f := File{
		Issuer:    issuer,
		Keys:      EncodeKeys(keys),
		UpdatedAt: updatedAt,
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// PathBesideKey returns ticket-keys.json next to the node key file.
func PathBesideKey(nodeKeyPath string) string {
	return filepath.Join(filepath.Dir(nodeKeyPath), "ticket-keys.json")
}
