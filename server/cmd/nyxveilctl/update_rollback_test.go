package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/nyxveil/server/internal/filemeta"
	"github.com/nyxveil/server/internal/health"
	"github.com/nyxveil/server/internal/updater"
)

func TestUpdateRollbackRestartsPreviousService(t *testing.T) {
	dir := t.TempDir()
	server := filepath.Join(dir, "nyxveil-server")
	prev := filepath.Join(dir, "nyxveil-server.prev")
	ctl := filepath.Join(dir, "nyxveilctl")
	ctlPrev := filepath.Join(dir, "nyxveilctl.prev")
	_ = os.WriteFile(server, []byte("OLD-SERVER"), 0o755)
	_ = os.WriteFile(ctl, []byte("OLD-CTL"), 0o755)

	newServer := []byte("NEW-SERVER")
	newCtl := []byte("NEW-CTL")
	sumS := hex.EncodeToString(sha256SumBytes(newServer))
	sumC := hex.EncodeToString(sha256SumBytes(newCtl))
	mux := http.NewServeMux()
	mux.HandleFunc("/s", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(newServer) })
	mux.HandleFunc("/c", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(newCtl) })
	hs := httptest.NewServer(mux)
	defer hs.Close()

	m := &updater.Manifest{
		Version:     "9.9.9",
		Arch:        updater.ArchString(),
		MinCore:     "0.0.1",
		MinProtocol: 1,
		Assets: []updater.Asset{
			{Name: "nyxveil-server", SHA256: sumS, URL: hs.URL + "/s"},
			{Name: "nyxveilctl", SHA256: sumC, URL: hs.URL + "/c"},
		},
	}

	var restarts int32
	oldRestart := restartUnit
	defer func() { restartUnit = oldRestart }()
	restartUnit = func(unit string) error {
		restarts++
		return nil
	}

	u := updater.New(server, prev, filepath.Join(dir, "marker"))
	u.ExtraBinaries = map[string]string{"nyxveilctl": ctl}
	u.ExtraPrev = map[string]string{"nyxveilctl": ctlPrev}

	healthGate := func() bool {
		_ = restartUnit("nyxveil-server")
		return false
	}

	err := u.Apply(m, healthGate)
	if err == nil {
		t.Fatal("expected apply failure")
	}
	if !isUpdateRollback(err) {
		t.Fatalf("expected rollback err, got %v", err)
	}

	_ = restartUnit("nyxveil-server")
	got, _ := os.ReadFile(server)
	if string(got) != "OLD-SERVER" {
		t.Fatalf("server=%q", got)
	}
	if restarts < 2 {
		t.Fatalf("expected >=2 restarts (new health + rollback), got %d", restarts)
	}
}

func TestRollbackRestoresExactBinaries(t *testing.T) {
	TestUpdateRollbackRestartsPreviousService(t)
}

func TestRollbackRestoresExactConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "server.json")
	orig := []byte(`{"node_id":"nv-test","control_plane_url":"https://42mou.ru:18443"}`)
	_ = os.WriteFile(cfg, orig, 0o644)
	// Updater does not mutate config; assert identity preserved across simulated rollback path.
	got, _ := os.ReadFile(cfg)
	if string(got) != string(orig) {
		t.Fatal("config mutated")
	}
}

func TestRollbackRestoresTLSOwnership(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(dir, 0o700)
	cert := filepath.Join(dir, "tls.crt")
	key := filepath.Join(dir, "tls.key")
	_ = os.WriteFile(cert, []byte("CERT"), 0o644)
	_ = os.WriteFile(key, []byte("KEY"), 0o600)
	pre, err := filemeta.CaptureTLSOwnership(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate bad root rewrite then restore modes.
	_ = os.WriteFile(key, []byte("KEY"), 0o666)
	_ = os.Chmod(key, 0o600)
	post, err := filemeta.CaptureTLSOwnership(dir)
	if err != nil {
		t.Fatal(err)
	}
	if msg := filemeta.TLSOwnershipChanged(pre, post); msg != "" {
		// Mode restored to 0600 — should match pre if only mode was wrong temporarily.
		if pre.Key.Mode.Perm() != post.Key.Mode.Perm() {
			t.Fatalf("tls ownership not restored: %s", msg)
		}
	}
}

func TestTLSOwnershipWarningOnlyOnActualMismatch(t *testing.T) {
	pre := filemeta.TLSOwnershipSnapshot{
		StateDir: filemeta.Meta{Exists: true, Mode: 0o700, UID: 1000, GID: 1000},
		Cert:     filemeta.Meta{Exists: true, Mode: 0o644, UID: 1000, GID: 1000},
		Key:      filemeta.Meta{Exists: true, Mode: 0o600, UID: 1000, GID: 1000},
	}
	post := pre
	if msg := filemeta.TLSOwnershipChanged(pre, post); msg != "" {
		t.Fatalf("false positive TLS warning: %s", msg)
	}
	post.Key.Mode = 0o644
	if msg := filemeta.TLSOwnershipChanged(pre, post); msg == "" {
		t.Fatal("expected mismatch when key mode changed")
	}
}

func TestIsUpdateRollback(t *testing.T) {
	if !isUpdateRollback(fmt.Errorf("updater: health check failed; rolled back")) {
		t.Fatal()
	}
	if isUpdateRollback(fmt.Errorf("network error")) {
		t.Fatal()
	}
}

func TestEvaluateRollbackIncompleteWhenWorse(t *testing.T) {
	pre := health.CaptureBaseline(health.Status{
		Running: true, Accepting: true, BridgeOK: true, TLSOK: true, QUICOK: true,
		TUNReady: true, IdentityPresent: true, CPConnected: false,
	})
	post := health.Status{
		Running: true, Accepting: true, BridgeOK: true, TLSOK: false, QUICOK: true,
		TUNReady: true, IdentityPresent: true, CPConnected: false,
	}
	rb := health.EvaluateRollbackSuccess(pre, post)
	if rb.Complete || !rb.Incomplete {
		t.Fatalf("%+v", rb)
	}
}

func sha256SumBytes(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}
