package configure

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nyxveil/server/internal/filemeta"
	"github.com/nyxveil/server/internal/nodetls"
)

// StagingTLSPaths returns secure next-cert paths under stateDir.
// Live tls.crt/tls.key stay untouched until AtomicCommitTLS.
func StagingTLSPaths(stateDir string) (certPath, keyPath string) {
	return filepath.Join(stateDir, "tls.next.crt"), filepath.Join(stateDir, "tls.next.key")
}

// SeedStagingKeyFromLive copies the live leaf private key into staging when it is
// ACME/WebPKI compatible (ECDSA). Incompatible keys (e.g. legacy Ed25519 self-signed)
// are NOT copied — staging stays empty so ACME generates a new ECDSA P-256 key.
// Live tls.key is never modified.
func SeedStagingKeyFromLive(liveKey, stageKey string) error {
	if _, err := os.Stat(liveKey); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("configure: stat live TLS key: %w", err)
	}
	if !nodetls.KeyFileACMECompatible(liveKey) {
		_ = os.Remove(stageKey)
		return nil
	}
	raw, err := os.ReadFile(liveKey)
	if err != nil {
		return fmt.Errorf("configure: read live TLS key for staging seed: %w", err)
	}
	meta, _ := filemeta.CaptureMeta(liveKey)
	return filemeta.AtomicWrite(stageKey, raw, filemeta.RuntimeTLSKeyMode, meta.UID, meta.GID)
}

// CleanStaging removes staged next-cert material (never logs contents).
func CleanStaging(stageCert, stageKey string) {
	_ = os.Remove(stageCert)
	_ = os.Remove(stageKey)
	_ = os.Remove(stageCert + ".tmp")
	_ = os.Remove(stageKey + ".tmp")
	_ = os.Remove(stageKey + ".seed.tmp")
	_ = os.Remove(stageCert + ".filemeta.tmp")
	_ = os.Remove(stageKey + ".filemeta.tmp")
}

// AtomicCommitTLS writes validated staged cert/key onto live paths with nyxveil ownership.
func AtomicCommitTLS(stageCert, stageKey, liveCert, liveKey string) error {
	if _, err := nodetls.Load(nodetls.Paths{CertFile: stageCert, KeyFile: stageKey}); err != nil {
		return fmt.Errorf("configure: staged TLS pair invalid: %w", err)
	}
	certPEM, err := os.ReadFile(stageCert)
	if err != nil {
		return fmt.Errorf("configure: read staged cert: %w", err)
	}
	keyPEM, err := os.ReadFile(stageKey)
	if err != nil {
		return fmt.Errorf("configure: read staged key: %w", err)
	}
	uid, gid, _ := filemeta.LookupServiceIDs()
	if prev, err := filemeta.CaptureMeta(liveKey); err == nil && prev.Exists && prev.UID >= 0 {
		uid, gid = prev.UID, prev.GID
	}
	oldCert, oldCertErr := os.ReadFile(liveCert)
	oldKey, oldKeyErr := os.ReadFile(liveKey)
	rollback := func() {
		if oldCertErr == nil {
			_ = filemeta.AtomicWrite(liveCert, oldCert, filemeta.RuntimeTLSCertMode, uid, gid)
		} else if os.IsNotExist(oldCertErr) {
			_ = os.Remove(liveCert)
		}
		if oldKeyErr == nil {
			_ = filemeta.AtomicWrite(liveKey, oldKey, filemeta.RuntimeTLSKeyMode, uid, gid)
		} else if os.IsNotExist(oldKeyErr) {
			_ = os.Remove(liveKey)
		}
	}
	if err := filemeta.AtomicWrite(liveCert, certPEM, filemeta.RuntimeTLSCertMode, uid, gid); err != nil {
		return err
	}
	if err := filemeta.AtomicWrite(liveKey, keyPEM, filemeta.RuntimeTLSKeyMode, uid, gid); err != nil {
		rollback()
		return err
	}
	if err := filemeta.EnforceRuntimeTLS(filepath.Dir(liveKey)); err != nil {
		rollback()
		return err
	}
	return nil
}

// FileBytesEqual reports whether two files have identical contents (missing = unequal unless both missing).
func FileBytesEqual(a, b string) bool {
	ba, ea := os.ReadFile(a)
	bb, eb := os.ReadFile(b)
	if ea != nil || eb != nil {
		return ea != nil && eb != nil && os.IsNotExist(ea) && os.IsNotExist(eb)
	}
	if len(ba) != len(bb) {
		return false
	}
	for i := range ba {
		if ba[i] != bb[i] {
			return false
		}
	}
	return true
}
