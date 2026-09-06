package nodetls_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/nyxveil/server/internal/nodetls"
)

func TestUnsupportedExistingLeafKeyFallsBackToNewStagedKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "tls.key")
	writeEd25519Key(t, keyPath)

	if nodetls.KeyFileACMECompatible(keyPath) {
		t.Fatal("Ed25519 must be incompatible")
	}
	before, _ := os.ReadFile(keyPath)
	signer, err := nodetls.LoadOrCreateStableKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	ec, ok := signer.(*ecdsa.PrivateKey)
	if !ok || ec.Curve != elliptic.P256() {
		t.Fatalf("want P-256 ECDSA, got %T", signer)
	}
	after, _ := os.ReadFile(keyPath)
	if string(before) == string(after) {
		t.Fatal("incompatible key must be replaced on disk")
	}
	if !nodetls.KeyFileACMECompatible(keyPath) {
		t.Fatal("replacement must be ACME compatible")
	}
}

func TestExistingCompatibleECDSAKeyIsReused(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "tls.key")
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	writeECDSAKey(t, keyPath, priv)
	before, _ := os.ReadFile(keyPath)

	signer, err := nodetls.LoadOrCreateStableKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := signer.(*ecdsa.PrivateKey)
	if !ok || !got.Equal(priv) {
		t.Fatal("compatible ECDSA key must be reused")
	}
	after, _ := os.ReadFile(keyPath)
	if string(before) != string(after) {
		t.Fatal("compatible key file must not be rewritten")
	}
}

func TestLoadOrCreateStableKeyCreatesP256WhenMissing(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "tls.key")
	signer, err := nodetls.LoadOrCreateStableKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := signer.(*ecdsa.PrivateKey); !ok {
		t.Fatalf("%T", signer)
	}
	if !nodetls.ACMECompatibleLeafKey(signer) {
		t.Fatal("new key must be ACME compatible")
	}
}

func TestSecondLoadReusesMigratedP256Key(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "tls.key")
	writeEd25519Key(t, keyPath)
	s1, err := nodetls.LoadOrCreateStableKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	pem1, _ := os.ReadFile(keyPath)
	s2, err := nodetls.LoadOrCreateStableKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	pem2, _ := os.ReadFile(keyPath)
	if string(pem1) != string(pem2) {
		t.Fatal("renewal must reuse migrated P-256 key bytes")
	}
	if !s1.Public().(*ecdsa.PublicKey).Equal(s2.Public().(*ecdsa.PublicKey)) {
		t.Fatal("public key changed on second load")
	}
}

func TestACMECompatibleLeafKeyTypes(t *testing.T) {
	edPub, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = edPub
	if nodetls.ACMECompatibleLeafKey(edPriv) {
		t.Fatal("Ed25519 must not be ACME compatible")
	}
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if !nodetls.ACMECompatibleLeafKey(ec) {
		t.Fatal("ECDSA must be compatible")
	}
}

func writeEd25519Key(t *testing.T, path string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeECDSAKey(t *testing.T, path string, priv *ecdsa.PrivateKey) {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}
