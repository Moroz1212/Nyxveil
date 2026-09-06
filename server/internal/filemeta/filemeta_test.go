package filemeta_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nyxveil/server/internal/filemeta"
)

func TestSnapshotRestoreContentsAndMode(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "tls.key")
	if err := os.WriteFile(live, []byte("SECRET-KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapDir := filepath.Join(dir, "snap")
	s, err := filemeta.SnapshotFile(live, snapDir, "tls.key")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte("CORRUPT"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Meta.Path = live
	if err := filemeta.Restore(s); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(live)
	if string(got) != "SECRET-KEY" {
		t.Fatalf("contents=%q", got)
	}
	if runtime.GOOS == "windows" {
		return
	}
	st, _ := os.Stat(live)
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("key must not be group/world accessible: %o", st.Mode().Perm())
	}
}

func TestSnapshotRestoreAbsentFile(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "missing.key")
	snapDir := filepath.Join(dir, "snap")
	s, err := filemeta.SnapshotFile(live, snapDir, "missing.key")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte("should-go"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Meta.Path = live
	if err := filemeta.Restore(s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(live); !os.IsNotExist(err) {
		t.Fatal("expected absent after restore")
	}
}

func TestAtomicWriteAppliesMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits not enforced on windows")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "tls.key")
	if err := filemeta.AtomicWrite(p, []byte("k"), 0o600, -1, -1); err != nil {
		t.Fatal(err)
	}
	bad, err := filemeta.KeyWorldReadable(p)
	if err != nil || bad {
		t.Fatalf("world readable=%v err=%v", bad, err)
	}
}

func TestRestoreCallsChownWithSavedIDs(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "tls.key")
	_ = os.WriteFile(live, []byte("k"), 0o600)

	old := filemeta.Chown
	defer func() { filemeta.Chown = old }()
	var sawUID, sawGID int
	var calls int
	filemeta.Chown = func(name string, uid, gid int) error {
		calls++
		sawUID, sawGID = uid, gid
		return nil
	}

	snapDir := filepath.Join(dir, "snap")
	s, err := filemeta.SnapshotFile(live, snapDir, "tls.key")
	if err != nil {
		t.Fatal(err)
	}
	s.Meta.UID = 4242
	s.Meta.GID = 4242
	s.Meta.Path = live
	s.Meta.Exists = true
	s.Meta.Mode = 0o600
	raw, _ := json.Marshal(s.Meta)
	_ = os.WriteFile(s.MetaFile, raw, 0o600)

	if err := os.WriteFile(live, []byte("root-owned-rewrite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := filemeta.Restore(s); err != nil {
		t.Fatal(err)
	}
	if calls == 0 || sawUID != 4242 || sawGID != 4242 {
		t.Fatalf("chown not applied with saved ids: calls=%d uid=%d gid=%d", calls, sawUID, sawGID)
	}
	got, _ := os.ReadFile(live)
	if string(got) != "k" {
		t.Fatalf("content=%q", got)
	}
}

func TestEnforceRuntimeTLSModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits not enforced on windows")
	}
	dir := t.TempDir()
	cert := filepath.Join(dir, "tls.crt")
	key := filepath.Join(dir, "tls.key")
	_ = os.WriteFile(cert, []byte("c"), 0o666)
	_ = os.WriteFile(key, []byte("k"), 0o666)
	_ = filemeta.EnforceRuntimeTLS(dir)
	st, _ := os.Stat(key)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %o", st.Mode().Perm())
	}
	st, _ = os.Stat(cert)
	if st.Mode().Perm() != 0o644 {
		t.Fatalf("cert mode %o", st.Mode().Perm())
	}
}
