package catalog_test

import (
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/controlplane/catalog"
	"github.com/nyxveil/nvp/core/controlplane/model"
)

func TestCatalogSignAndVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "k1", PrivateKey: priv}
	cat := model.Catalog{
		Version:   "1",
		Locations: []model.Location{{LocationID: "fi", Enabled: true}},
		Nodes:     []model.NodeRegistryEntry{{NodeID: "n1", Enabled: true}},
	}
	signed, err := signer.Sign(cat)
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Verify(catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"k1": pub}}, signed); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogTamperedRejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "k1", PrivateKey: priv}
	signed, _ := signer.Sign(model.Catalog{Version: "1"})
	signed.Catalog.Version = "2"
	if err := catalog.Verify(catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"k1": pub}}, signed); err != catalog.ErrInvalidSignature {
		t.Fatalf("expected invalid signature, got %v", err)
	}
}

func TestCatalogExpiredRejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "k1", PrivateKey: priv}
	cat := model.Catalog{Version: "1", ExpiresAt: time.Now().Add(-time.Hour)}
	signed, _ := signer.Sign(cat)
	if err := catalog.Verify(catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"k1": pub}}, signed); err != catalog.ErrCatalogExpired {
		t.Fatalf("expected expired, got %v", err)
	}
}

func TestCatalogWrongKeyRejected(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	wrongPub, _, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "k1", PrivateKey: priv}
	signed, _ := signer.Sign(model.Catalog{Version: "1"})
	if err := catalog.Verify(catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"k1": wrongPub}}, signed); err != catalog.ErrInvalidSignature {
		t.Fatalf("expected invalid signature, got %v", err)
	}
}
