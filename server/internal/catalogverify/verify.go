// Package catalogverify wraps Frozen Core catalog.Verify for production gates
// and SPKI rotation checks without re-implementing canonicalization.
package catalogverify

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nyxveil/nvp/core/controlplane/catalog"
	"github.com/nyxveil/nvp/core/controlplane/model"
)

// CatalogKeysJSON matches GET /api/v1/catalog-keys.
type CatalogKeysJSON struct {
	Issuer    string            `json:"issuer"`
	Keys      map[string]string `json:"keys"`
	UpdatedAt int64             `json:"updated_at"`
}

// VerifySignedCatalogJSON verifies Ed25519 signature over the exact Frozen Core
// canonical catalog payload. Fail-closed on unknown key_id / bad key / bad sig.
func VerifySignedCatalogJSON(keysJSON, catalogJSON []byte) (model.SignedCatalog, error) {
	var keysResp CatalogKeysJSON
	if err := json.Unmarshal(keysJSON, &keysResp); err != nil {
		return model.SignedCatalog{}, fmt.Errorf("malformed catalog-keys: %w", err)
	}
	if len(keysResp.Keys) == 0 {
		return model.SignedCatalog{}, fmt.Errorf("catalog-keys empty")
	}

	vk := catalog.VerifyKeys{Keys: make(map[string]ed25519.PublicKey, len(keysResp.Keys))}
	for kid, b64 := range keysResp.Keys {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
		if err != nil {
			return model.SignedCatalog{}, fmt.Errorf("malformed key %s: %w", kid, err)
		}
		if len(raw) != ed25519.PublicKeySize {
			return model.SignedCatalog{}, fmt.Errorf("malformed key %s: want %d bytes got %d", kid, ed25519.PublicKeySize, len(raw))
		}
		vk.Keys[kid] = ed25519.PublicKey(raw)
	}

	signed, err := catalog.Parse(catalogJSON)
	if err != nil {
		return model.SignedCatalog{}, fmt.Errorf("malformed catalog: %w", err)
	}
	if strings.TrimSpace(signed.KeyID) == "" {
		return model.SignedCatalog{}, fmt.Errorf("missing key_id")
	}
	if len(signed.Signature) == 0 {
		return model.SignedCatalog{}, fmt.Errorf("malformed signature: empty")
	}
	if err := catalog.Verify(vk, signed); err != nil {
		return model.SignedCatalog{}, err
	}
	return signed, nil
}

// FindNode returns a node registry entry by id.
func FindNode(signed model.SignedCatalog, nodeID string) (model.NodeRegistryEntry, bool) {
	for _, n := range signed.Catalog.Nodes {
		if n.NodeID == nodeID {
			return n, true
		}
	}
	return model.NodeRegistryEntry{}, false
}
