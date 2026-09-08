// Package nyxveilbridge is the gomobile-facing surface for the Android NVP engine.
package nyxveilbridge

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nyxveil/nvp/core/controlplane/catalog"
)

// Version returns the bridge version string.
func Version() string {
	return "1.0.0"
}

// Protocol returns the Frozen Core protocol id.
func Protocol() string {
	return "NVP/1"
}

// NativeReady is always true in the gomobile AAR (library loaded).
func NativeReady() bool {
	return true
}

// CatalogVerify validates signed catalog JSON using NVP controlplane/catalog.
// Matches Windows engine: wait up to 5m for IssuedAt not-before skew, then Frozen Core Verify.
func CatalogVerify(signedCatalogJSON, keysJSON []byte) error {
	signed, err := catalog.Parse(signedCatalogJSON)
	if err != nil {
		return err
	}
	var raw map[string]string
	if err := json.Unmarshal(keysJSON, &raw); err != nil {
		return fmt.Errorf("keys json: %w", err)
	}
	keys := catalog.VerifyKeys{Keys: make(map[string]ed25519.PublicKey, len(raw))}
	for kid, b64 := range raw {
		pub, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return fmt.Errorf("key %s: %w", kid, err)
		}
		if len(pub) != ed25519.PublicKeySize {
			return fmt.Errorf("key %s: want %d bytes", kid, ed25519.PublicKeySize)
		}
		keys.Keys[kid] = ed25519.PublicKey(pub)
	}
	waitCatalogNotBefore(signed.Catalog.IssuedAt, 5*time.Minute)
	return catalog.Verify(keys, signed)
}

// waitCatalogNotBefore sleeps until IssuedAt if the catalog is slightly not-yet-valid
// due to clock skew (within maxSkew). Does not alter expires_at or Frozen Core.
// Mirrors clients/windows/engine/internal/engine/session.go waitCatalogNotBefore.
func waitCatalogNotBefore(issued time.Time, maxSkew time.Duration) {
	now := time.Now().UTC()
	if !now.Before(issued) {
		return
	}
	d := issued.Sub(now)
	if d <= 0 || d > maxSkew {
		return
	}
	time.Sleep(d + time.Millisecond)
}
