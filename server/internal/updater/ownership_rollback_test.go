package updater_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nyxveil/server/internal/filemeta"
	"github.com/nyxveil/server/internal/updater"
)

func TestUpdaterRollbackRestoresTLSContents(t *testing.T) {
	h := setupUpdateTLSHarness(t)
	err := h.applyFailingHealth()
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("err=%v", err)
	}
	got, _ := os.ReadFile(h.tlsKey)
	if string(got) != "GOOD-KEY" {
		t.Fatalf("tls.key contents=%q", got)
	}
	got, _ = os.ReadFile(h.tlsCert)
	if string(got) != "GOOD-CERT" {
		t.Fatalf("tls.crt contents=%q", got)
	}
}

func TestUpdaterRollbackRestoresTLSMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits not enforced on windows")
	}
	h := setupUpdateTLSHarness(t)
	_ = os.Chmod(h.tlsKey, 0o600)
	_ = h.applyFailingHealth()
	st, err := os.Stat(h.tlsKey)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", st.Mode().Perm())
	}
	bad, _ := filemeta.KeyWorldReadable(h.tlsKey)
	if bad {
		t.Fatal("key world readable after rollback")
	}
}

func TestUpdaterRollbackRestoresTLSOwner(t *testing.T) {
	h := setupUpdateTLSHarness(t)
	old := filemeta.Chown
	defer func() { filemeta.Chown = old }()
	var sawUID int
	filemeta.Chown = func(name string, uid, gid int) error {
		if strings.Contains(name, "tls.key") {
			sawUID = uid
		}
		return nil
	}
	h.u.EnforceOwnership = func(stateDir string) error {
		return filemeta.ApplyOwnerMode(filepath.Join(stateDir, "tls.key"), 4242, 4242, 0o600)
	}
	if err := h.applyFailingHealth(); err == nil {
		t.Fatal("expected rollback")
	}
	if sawUID != 4242 {
		t.Fatalf("expected chown uid 4242 after rollback enforce, got %d", sawUID)
	}
}

func TestUpdaterRollbackRestoresTLSGroup(t *testing.T) {
	h := setupUpdateTLSHarness(t)
	var lastGID int
	old := filemeta.Chown
	defer func() { filemeta.Chown = old }()
	filemeta.Chown = func(name string, uid, gid int) error {
		lastGID = gid
		return nil
	}
	h.u.EnforceOwnership = func(stateDir string) error {
		return filemeta.ApplyOwnerMode(filepath.Join(stateDir, "tls.key"), 111, 222, 0o600)
	}
	_ = h.applyFailingHealth()
	if lastGID != 222 {
		t.Fatalf("gid=%d", lastGID)
	}
}

func TestUpdaterRollbackRestoresAbsentFileState(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(state, 0o700)
	server := filepath.Join(dir, "nyxveil-server")
	prev := filepath.Join(dir, "nyxveil-server.prev")
	_ = os.WriteFile(server, []byte("OLD"), 0o755)

	newB := []byte("NEW")
	sum := hex.EncodeToString(sha256Sum(newB))
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(newB) }))
	defer hs.Close()

	u := updater.New(server, prev, filepath.Join(state, "marker"))
	u.StateDir = state
	u.EnforceOwnership = func(string) error { return nil }

	m := &updater.Manifest{
		Version: "9.9.9", Arch: updater.ArchString(), MinCore: "0.0.1", MinProtocol: 1,
		Assets: []updater.Asset{{Name: "nyxveil-server", SHA256: sum, URL: hs.URL}},
	}
	err := u.Apply(m, func() bool {
		// Create a TLS key only during the failed new version window.
		_ = os.WriteFile(filepath.Join(state, "tls.key"), []byte("ephemeral"), 0o600)
		return false
	})
	if err == nil {
		t.Fatal("expected fail")
	}
	if _, err := os.Stat(filepath.Join(state, "tls.key")); !os.IsNotExist(err) {
		t.Fatal("ephemeral tls.key must be removed on rollback to absent")
	}
}

func TestUpdaterRollbackRestoresServiceHealth(t *testing.T) {
	h := setupUpdateTLSHarness(t)
	healthCalls := 0
	err := h.u.Apply(h.manifest(), func() bool {
		healthCalls++
		// Corrupt TLS as root would during a bad write.
		_ = os.WriteFile(h.tlsKey, []byte("BAD"), 0o644)
		return false
	})
	if err == nil {
		t.Fatal("expected rollback")
	}
	got, _ := os.ReadFile(h.tlsKey)
	if string(got) != "GOOD-KEY" {
		t.Fatal("TLS not restored for service health")
	}
	if healthCalls != 1 {
		t.Fatalf("healthCalls=%d", healthCalls)
	}
	srv, _ := os.ReadFile(h.server)
	if string(srv) != "OLD-SERVER" {
		t.Fatal("binary not restored")
	}
}

func TestSuccessfulUpdateLeavesRuntimeTLSReadableByNyxveil(t *testing.T) {
	h := setupUpdateTLSHarness(t)
	_ = os.WriteFile(h.tlsKey, []byte("GOOD-KEY"), 0o666)
	enforced := false
	h.u.EnforceOwnership = func(stateDir string) error {
		enforced = true
		return filemeta.EnforceRuntimeTLS(stateDir)
	}
	if err := h.u.Apply(h.manifest(), func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	if !enforced {
		t.Fatal("ownership enforce not called on success")
	}
	if runtime.GOOS == "windows" {
		return
	}
	bad, _ := filemeta.KeyWorldReadable(h.tlsKey)
	if bad {
		t.Fatal("key still world readable")
	}
}

func TestTLSPrivateKeyNotWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits not enforced on windows")
	}
	h := setupUpdateTLSHarness(t)
	_ = h.applyFailingHealth()
	bad, err := filemeta.KeyWorldReadable(h.tlsKey)
	if err != nil || bad {
		t.Fatalf("world=%v err=%v", bad, err)
	}
}

type updateTLSHarness struct {
	dir, state, server, prev, tlsCert, tlsKey string
	u                                         *updater.Updater
	hs                                        *httptest.Server
	sum                                       string
}

func setupUpdateTLSHarness(t *testing.T) *updateTLSHarness {
	t.Helper()
	dir, err := os.MkdirTemp("", "nyxveil-upd-own-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(state, 0o700)
	h := &updateTLSHarness{
		dir:     dir,
		state:   state,
		server:  filepath.Join(dir, "nyxveil-server"),
		prev:    filepath.Join(dir, "nyxveil-server.prev"),
		tlsCert: filepath.Join(state, "tls.crt"),
		tlsKey:  filepath.Join(state, "tls.key"),
	}
	_ = os.WriteFile(h.server, []byte("OLD-SERVER"), 0o755)
	_ = os.WriteFile(h.tlsCert, []byte("GOOD-CERT"), 0o644)
	_ = os.WriteFile(h.tlsKey, []byte("GOOD-KEY"), 0o600)

	newB := []byte("NEW-SERVER")
	h.sum = hex.EncodeToString(sha256Sum(newB))
	h.hs = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(newB) }))
	t.Cleanup(h.hs.Close)

	h.u = updater.New(h.server, h.prev, filepath.Join(state, "marker"))
	h.u.StateDir = state
	h.u.EnforceOwnership = func(stateDir string) error {
		return filemeta.EnforceRuntimeTLS(stateDir)
	}
	return h
}

func (h *updateTLSHarness) manifest() *updater.Manifest {
	return &updater.Manifest{
		Version: "9.9.9", Arch: updater.ArchString(), MinCore: "0.0.1", MinProtocol: 1,
		Assets: []updater.Asset{{Name: "nyxveil-server", SHA256: h.sum, URL: h.hs.URL}},
	}
}

func (h *updateTLSHarness) applyFailingHealth() error {
	return h.u.Apply(h.manifest(), func() bool {
		// Simulate root-owned replacement during failed update window.
		_ = os.WriteFile(h.tlsKey, []byte("ROOT-KEY"), 0o600)
		_ = os.WriteFile(h.tlsCert, []byte("ROOT-CERT"), 0o644)
		return false
	})
}

func sha256Sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}
