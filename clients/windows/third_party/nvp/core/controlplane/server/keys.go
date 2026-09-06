package server

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/controlplane/catalog"
)

// KeyMaterial holds persistent signing keys for Control Plane.
type KeyMaterial struct {
	IssuerKeyID   string
	IssuerPriv    ed25519.PrivateKey
	CatalogKeyID  string
	CatalogSigner catalog.Signer
}

// LoadOrGenerateKeys loads Ed25519 keys from directory or creates new ones.
func LoadOrGenerateKeys(dir string) (KeyMaterial, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return KeyMaterial{}, err
	}
	issuerPriv, err := loadOrCreateKey(filepath.Join(dir, "issuer.key"))
	if err != nil {
		return KeyMaterial{}, fmt.Errorf("issuer key: %w", err)
	}
	catalogPriv, err := loadOrCreateKey(filepath.Join(dir, "catalog.key"))
	if err != nil {
		return KeyMaterial{}, fmt.Errorf("catalog key: %w", err)
	}
	return KeyMaterial{
		IssuerKeyID:  "cp-key-1",
		IssuerPriv:   issuerPriv,
		CatalogKeyID: "cat-key-1",
		CatalogSigner: catalog.Signer{
			KeyID:      "cat-key-1",
			PrivateKey: catalogPriv,
		},
	}, nil
}

// BuildIssuerConfig creates ticket issuer from key material.
func (k KeyMaterial) BuildIssuerConfig(issuerURL, audience string) ticket.IssuerConfig {
	return ticket.IssuerConfig{
		Issuer:     issuerURL,
		Audience:   audience,
		KeyID:      k.IssuerKeyID,
		PrivateKey: k.IssuerPriv,
		TTL:        15 * time.Minute,
	}
}

func loadOrCreateKey(path string) (ed25519.PrivateKey, error) {
	if data, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(data)
		if block == nil || block.Type != "PRIVATE KEY" {
			return nil, fmt.Errorf("invalid pem in %s", path)
		}
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		priv, ok := key.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("not ed25519 key")
		}
		return priv, nil
	}
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	return priv, nil
}
