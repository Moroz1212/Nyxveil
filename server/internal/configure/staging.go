package configure

import (
	"fmt"
	"os"
	"path/filepath"
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
	if err := os.MkdirAll(filepath.Dir(stageKey), 0o700); err != nil {
		return err
	}
	tmp := stageKey + ".seed.tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, stageKey)
}

// CleanStaging removes staged next-cert material (never logs contents).
func CleanStaging(stageCert, stageKey string) {
	_ = os.Remove(stageCert)
	_ = os.Remove(stageKey)
	_ = os.Remove(stageCert + ".tmp")
	_ = os.Remove(stageKey + ".tmp")
	_ = os.Remove(stageKey + ".seed.tmp")
}

// AtomicCommitTLS renames validated staged cert/key onto live paths.
func AtomicCommitTLS(stageCert, stageKey, liveCert, liveKey string) error {
	certPEM, err := os.ReadFile(stageCert)
	if err != nil {
		return fmt.Errorf("configure: read staged cert: %w", err)
	}
	keyPEM, err := os.ReadFile(stageKey)
	if err != nil {
		return fmt.Errorf("configure: read staged key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(liveCert), 0o700); err != nil {
		return err
	}
	tmpC := liveCert + ".commit.tmp"
	tmpK := liveKey + ".commit.tmp"
	if err := os.WriteFile(tmpC, certPEM, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(tmpK, keyPEM, 0o600); err != nil {
		_ = os.Remove(tmpC)
		return err
	}
	_ = os.Chmod(tmpC, 0o644)
	_ = os.Chmod(tmpK, 0o600)
	if err := os.Rename(tmpC, liveCert); err != nil {
		_ = os.Remove(tmpC)
		_ = os.Remove(tmpK)
		return err
	}
	if err := os.Rename(tmpK, liveKey); err != nil {
		_ = os.Remove(tmpK)
		return fmt.Errorf("configure: commit live key after cert: %w", err)
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
