package configure

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nyxveil/server/internal/filemeta"
)

// StagingTLSPaths returns secure next-cert paths under stateDir.
// Live tls.crt/tls.key stay untouched until AtomicCommitTLS.
func StagingTLSPaths(stateDir string) (certPath, keyPath string) {
	return filepath.Join(stateDir, "tls.next.crt"), filepath.Join(stateDir, "tls.next.key")
}

// SeedStagingKeyFromLive copies the live leaf private key into staging when present
// so ACME CSR can reuse SPKI across self-signed → publicly trusted transitions.
func SeedStagingKeyFromLive(liveKey, stageKey string) error {
	b, err := os.ReadFile(liveKey)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("configure: read live TLS key for staging seed: %w", err)
	}
	meta, _ := filemeta.CaptureMeta(liveKey)
	uid, gid := meta.UID, meta.GID
	return filemeta.AtomicWrite(stageKey, b, filemeta.RuntimeTLSKeyMode, uid, gid)
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
	if err := filemeta.AtomicWrite(liveCert, certPEM, filemeta.RuntimeTLSCertMode, uid, gid); err != nil {
		return err
	}
	if err := filemeta.AtomicWrite(liveKey, keyPEM, filemeta.RuntimeTLSKeyMode, uid, gid); err != nil {
		return err
	}
	return filemeta.EnforceRuntimeTLS(filepath.Dir(liveKey))
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
