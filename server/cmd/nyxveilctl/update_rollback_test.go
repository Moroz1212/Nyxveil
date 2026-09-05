package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

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

	var restarts atomic.Int32
	oldRestart := restartUnit
	oldActive := serviceActive
	oldHealth := ctlHealthJSON
	defer func() {
		restartUnit = oldRestart
		serviceActive = oldActive
		ctlHealthJSON = oldHealth
	}()

	restartUnit = func(unit string) error {
		restarts.Add(1)
		return nil
	}
	serviceActive = func(unit string) bool { return true }

	healthPhase := 0
	ctlHealthJSON = func() ([]byte, error) {
		healthPhase++
		if healthPhase == 1 {
			return []byte(`{"healthy":false}`), nil
		}
		return []byte(`{"healthy":true}`), nil
	}

	u := updater.New(server, prev, filepath.Join(dir, "marker"))
	u.ExtraBinaries = map[string]string{"nyxveilctl": ctl}
	u.ExtraPrev = map[string]string{"nyxveilctl": ctlPrev}

	health := func() bool {
		_ = restartUnit("nyxveil-server")
		out, err := ctlHealthJSON()
		if err != nil {
			return false
		}
		return strings.Contains(string(out), `"healthy":true`)
	}

	err := u.Apply(m, health)
	if err == nil {
		t.Fatal("expected apply failure")
	}
	if !isUpdateRollback(err) {
		t.Fatalf("expected rollback err, got %v", err)
	}

	// Mirror runUpdate post-rollback restart path.
	_ = restartUnit("nyxveil-server")
	out, err := ctlHealthJSON()
	if err != nil || !strings.Contains(string(out), `"healthy":true`) {
		t.Fatal("expected old health after rollback restart")
	}

	got, _ := os.ReadFile(server)
	if string(got) != "OLD-SERVER" {
		t.Fatalf("server=%q", got)
	}
	if restarts.Load() < 2 {
		t.Fatalf("expected >=2 restarts (new health + rollback), got %d", restarts.Load())
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

func sha256SumBytes(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}
