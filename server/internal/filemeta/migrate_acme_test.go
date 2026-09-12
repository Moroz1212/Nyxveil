package filemeta_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nyxveil/server/internal/filemeta"
)

func TestMigrateACMEState_LegacyRootOwned(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix ownership semantics")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to simulate root-owned ACME then migrate")
	}
	dir := t.TempDir()
	acme := filepath.Join(dir, "acme")
	if err := os.MkdirAll(acme, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(acme, "acme-account.key")
	if err := os.WriteFile(key, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Simulate legacy root ownership (already root in this test).
	if err := os.Chown(acme, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := filemeta.MigrateACMEState(dir); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(acme)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("acme mode %o", st.Mode().Perm())
	}
	if err := filemeta.ValidateRuntimeACME(dir); err != nil {
		t.Fatalf("validate after migrate: %v", err)
	}
}

func TestMigrateACMEState_Idempotent(t *testing.T) {
	dir := t.TempDir()
	if err := filemeta.MigrateACMEState(dir); err != nil {
		t.Fatal(err)
	}
	if err := filemeta.MigrateACMEState(dir); err != nil {
		t.Fatal(err)
	}
	acme := filepath.Join(dir, "acme")
	st, err := os.Stat(acme)
	if err != nil || !st.IsDir() {
		t.Fatalf("acme dir: %v", err)
	}
}

func TestMigrateACMEState_RejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ")
	}
	dir := t.TempDir()
	outside := t.TempDir()
	acme := filepath.Join(dir, "acme")
	if err := os.Symlink(outside, acme); err != nil {
		t.Fatal(err)
	}
	if err := filemeta.MigrateACMEState(dir); err == nil {
		t.Fatal("expected fail closed on ACME symlink")
	}
}

func TestValidateRuntimeACME_CreatesWhenWritable(t *testing.T) {
	dir := t.TempDir()
	if err := filemeta.ValidateRuntimeACME(dir); err != nil {
		t.Fatal(err)
	}
	acme := filepath.Join(dir, "acme")
	if st, err := os.Stat(acme); err != nil || !st.IsDir() {
		t.Fatalf("expected acme dir created: %v", err)
	}
}

func TestValidateRuntimeACME_Writable(t *testing.T) {
	dir := t.TempDir()
	if err := filemeta.MigrateACMEState(dir); err != nil {
		t.Fatal(err)
	}
	if err := filemeta.ValidateRuntimeACME(dir); err != nil {
		t.Fatal(err)
	}
}
