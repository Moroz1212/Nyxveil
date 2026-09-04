package catalog

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nyxveil/nvp/controlplane/model"
)

var (
	ErrInvalidSignature = errors.New("invalid catalog signature")
	ErrCatalogExpired   = errors.New("catalog expired")
)

// Signer signs node catalogs for distribution to clients.
type Signer struct {
	KeyID      string
	PrivateKey ed25519.PrivateKey
}

// VerifyKeys holds Control Plane verification keys (current + next).
type VerifyKeys struct {
	Keys map[string]ed25519.PublicKey
}

// Sign creates a signed catalog.
func (s *Signer) Sign(cat model.Catalog) (model.SignedCatalog, error) {
	cat.IssuedAt = time.Now().UTC()
	if cat.ExpiresAt.IsZero() {
		cat.ExpiresAt = cat.IssuedAt.Add(1 * time.Hour)
	}
	payload, err := canonicalPayload(cat)
	if err != nil {
		return model.SignedCatalog{}, err
	}
	sig := ed25519.Sign(s.PrivateKey, payload)
	return model.SignedCatalog{
		Catalog:   cat,
		KeyID:     s.KeyID,
		Signature: sig,
	}, nil
}

// Verify validates signed catalog integrity and freshness.
func Verify(v VerifyKeys, signed model.SignedCatalog) error {
	pub, ok := v.Keys[signed.KeyID]
	if !ok {
		return fmt.Errorf("unknown catalog signing key: %s", signed.KeyID)
	}
	payload, err := canonicalPayload(signed.Catalog)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, payload, signed.Signature) {
		return ErrInvalidSignature
	}
	now := time.Now().UTC()
	if now.After(signed.Catalog.ExpiresAt) {
		return ErrCatalogExpired
	}
	if now.Before(signed.Catalog.IssuedAt) {
		return ErrCatalogExpired
	}
	return nil
}

func canonicalPayload(cat model.Catalog) ([]byte, error) {
	// Deterministic JSON for signature verification.
	type canon struct {
		Version   string                    `json:"version"`
		Locations []model.Location          `json:"locations"`
		Nodes     []model.NodeRegistryEntry `json:"nodes"`
		IssuedAt  time.Time                 `json:"issued_at"`
		ExpiresAt time.Time                 `json:"expires_at"`
	}
	return json.Marshal(canon{
		Version:   cat.Version,
		Locations: cat.Locations,
		Nodes:     cat.Nodes,
		IssuedAt:  cat.IssuedAt.UTC(),
		ExpiresAt: cat.ExpiresAt.UTC(),
	})
}
