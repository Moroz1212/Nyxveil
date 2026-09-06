package catalogverify_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/controlplane/catalog"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/transport"
	"github.com/nyxveil/server/internal/catalogverify"
)

func signFixture(t *testing.T) (keysJSON, catalogJSON []byte, pub ed25519.PublicKey, priv ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer := catalog.Signer{KeyID: "k1", PrivateKey: priv}
	pin := make([]byte, 32)
	for i := range pin {
		pin[i] = byte(i + 1)
	}
	signed, err := signer.Sign(model.Catalog{
		Version: "1",
		Locations: []model.Location{{
			LocationID: "fi-helsinki", Country: "FI", City: "Helsinki", DisplayName: "Helsinki", Enabled: true,
		}},
		Nodes: []model.NodeRegistryEntry{{
			NodeID: "nv-test-227e939e", LocationID: "fi-helsinki", Enabled: true,
			ServerVersion: "1.1.0", ServerName: "fi-hel-01.nyxveil.ru", SPKIPin: pin,
			Endpoints: []transport.Endpoint{{Host: "46.8.218.27", Port: 443, Profiles: []transport.Profile{"quic-udp-443", "tls-tcp-443"}}},
		}},
		IssuedAt:  time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	catalogJSON, err = json.Marshal(signed)
	if err != nil {
		t.Fatal(err)
	}
	keys := catalogverify.CatalogKeysJSON{
		Issuer: "nyxveil",
		Keys:   map[string]string{"k1": base64.StdEncoding.EncodeToString(pub)},
	}
	keysJSON, err = json.Marshal(keys)
	if err != nil {
		t.Fatal(err)
	}
	return keysJSON, catalogJSON, pub, priv
}

func TestVerifySignedCatalogJSONPass(t *testing.T) {
	keys, cat, _, _ := signFixture(t)
	signed, err := catalogverify.VerifySignedCatalogJSON(keys, cat)
	if err != nil {
		t.Fatal(err)
	}
	n, ok := catalogverify.FindNode(signed, "nv-test-227e939e")
	if !ok || n.ServerName != "fi-hel-01.nyxveil.ru" {
		t.Fatalf("metadata missing: %+v", n)
	}
}

func TestTamperedCatalogFails(t *testing.T) {
	keys, cat, _, _ := signFixture(t)
	var signed model.SignedCatalog
	if err := json.Unmarshal(cat, &signed); err != nil {
		t.Fatal(err)
	}
	signed.Catalog.Version = "tampered"
	bad, _ := json.Marshal(signed)
	if _, err := catalogverify.VerifySignedCatalogJSON(keys, bad); err == nil {
		t.Fatal("expected fail")
	}
}

func TestWrongKeyFails(t *testing.T) {
	keys, cat, _, _ := signFixture(t)
	wrongPub, _, _ := ed25519.GenerateKey(nil)
	var kr catalogverify.CatalogKeysJSON
	_ = json.Unmarshal(keys, &kr)
	kr.Keys["k1"] = base64.StdEncoding.EncodeToString(wrongPub)
	badKeys, _ := json.Marshal(kr)
	if _, err := catalogverify.VerifySignedCatalogJSON(badKeys, cat); err == nil {
		t.Fatal("expected fail")
	}
}

func TestWrongKeyIDFails(t *testing.T) {
	keys, cat, _, _ := signFixture(t)
	var signed model.SignedCatalog
	_ = json.Unmarshal(cat, &signed)
	signed.KeyID = "unknown"
	bad, _ := json.Marshal(signed)
	if _, err := catalogverify.VerifySignedCatalogJSON(keys, bad); err == nil {
		t.Fatal("expected fail")
	}
}

func TestModifiedSPKIFails(t *testing.T) {
	keys, cat, _, _ := signFixture(t)
	var signed model.SignedCatalog
	_ = json.Unmarshal(cat, &signed)
	signed.Catalog.Nodes[0].SPKIPin[0] ^= 0xff
	bad, _ := json.Marshal(signed)
	if _, err := catalogverify.VerifySignedCatalogJSON(keys, bad); err == nil {
		t.Fatal("expected fail")
	}
}

func TestModifiedEndpointFails(t *testing.T) {
	keys, cat, _, _ := signFixture(t)
	var signed model.SignedCatalog
	_ = json.Unmarshal(cat, &signed)
	signed.Catalog.Nodes[0].Endpoints[0].Port = 444
	bad, _ := json.Marshal(signed)
	if _, err := catalogverify.VerifySignedCatalogJSON(keys, bad); err == nil {
		t.Fatal("expected fail")
	}
}

func TestModifiedServerVersionFails(t *testing.T) {
	keys, cat, _, _ := signFixture(t)
	var signed model.SignedCatalog
	_ = json.Unmarshal(cat, &signed)
	signed.Catalog.Nodes[0].ServerVersion = "9.9.9"
	bad, _ := json.Marshal(signed)
	if _, err := catalogverify.VerifySignedCatalogJSON(keys, bad); err == nil {
		t.Fatal("expected fail")
	}
}

func TestMalformedSignatureFails(t *testing.T) {
	keys, cat, _, _ := signFixture(t)
	var signed model.SignedCatalog
	_ = json.Unmarshal(cat, &signed)
	signed.Signature = []byte{1, 2, 3}
	bad, _ := json.Marshal(signed)
	if _, err := catalogverify.VerifySignedCatalogJSON(keys, bad); err == nil {
		t.Fatal("expected fail")
	}
}
