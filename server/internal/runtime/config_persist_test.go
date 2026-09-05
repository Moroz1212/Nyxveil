package runtime_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/controlplane"
	"github.com/nyxveil/server/internal/identity"
	"github.com/nyxveil/server/internal/localconfig"
	rt "github.com/nyxveil/server/internal/runtime"
)

func writeMinimalServerJSON(t *testing.T, dir, cpURL string) string {
	t.Helper()
	cfgPath := filepath.Join(dir, "server.json")
	cfg := localconfig.Default()
	cfg.ControlPlaneURL = cpURL
	cfg.NodeID = "n1"
	cfg.LocationID = "loc-bootstrap"
	cfg.DisplayName = "t"
	cfg.PublicHost = "127.0.0.1"
	cfg.TLSListen = "127.0.0.1:0"
	cfg.QUICListen = "127.0.0.1:0"
	cfg.ConfigVersion = 0
	raw, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(cfgPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

func TestServiceDoesNotWriteEtcConfig(t *testing.T) {
	dir := t.TempDir()
	etc := filepath.Join(dir, "etc")
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(etc, 0o755)
	_ = os.MkdirAll(state, 0o700)
	cfgPath := writeMinimalServerJSON(t, etc, "http://127.0.0.1:9")
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	infoBefore, _ := os.Stat(cfgPath)

	node, err := rt.New(rt.Options{
		ConfigPath:  cfgPath,
		KeyPath:     filepath.Join(state, "node.key"),
		AppliedPath: filepath.Join(state, "applied-config.json"),
		ControlHTTP: "127.0.0.1:0",
		SkipTUN:     true,
		TestMode:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := node.ApplyConfig(controlplane.NodeConfig{
		NodeID:        "n1",
		LocationID:    "loc-de",
		Enabled:       true,
		Capacity:      10,
		ConfigVersion: 7,
	}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("daemon must not modify /etc server.json")
	}
	infoAfter, _ := os.Stat(cfgPath)
	if infoAfter.ModTime() != infoBefore.ModTime() {
		t.Fatal("server.json mtime changed")
	}
	if _, err := os.Stat(filepath.Join(state, "applied-config.json")); err != nil {
		t.Fatal("expected applied-config.json")
	}
}

func TestAppliedConfigPersistenceAsServiceUser(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(state, 0o700)
	cfgPath := writeMinimalServerJSON(t, dir, "http://127.0.0.1:9")
	appliedPath := filepath.Join(state, "applied-config.json")

	node, err := rt.New(rt.Options{
		ConfigPath:  cfgPath,
		KeyPath:     filepath.Join(state, "node.key"),
		AppliedPath: appliedPath,
		SkipTUN:     true,
		TestMode:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := node.ApplyConfig(controlplane.NodeConfig{
		NodeID:        "n1",
		LocationID:    "loc-fi",
		Enabled:       true,
		Capacity:      42,
		ConfigVersion: 3,
	}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(appliedPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if st.Mode().Perm()&0o077 != 0 {
			t.Fatalf("applied-config world/group bits set: %v", st.Mode())
		}
	}
	snap, err := localconfig.LoadApplied(appliedPath)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Config.LocationID != "loc-fi" || snap.Config.ConfigVersion != 3 {
		t.Fatalf("%+v", snap.Config)
	}
}

func TestLocationChangeSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(state, 0o700)
	cfgPath := writeMinimalServerJSON(t, dir, "http://127.0.0.1:9")
	appliedPath := filepath.Join(state, "applied-config.json")
	keyPath := filepath.Join(state, "node.key")

	node1, err := rt.New(rt.Options{
		ConfigPath: cfgPath, KeyPath: keyPath, AppliedPath: appliedPath,
		SkipTUN: true, TestMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := node1.ApplyConfig(controlplane.NodeConfig{
		NodeID: "n1", LocationID: "loc-de", Enabled: true, Capacity: 5, ConfigVersion: 9,
	}); err != nil {
		t.Fatal(err)
	}

	node2, err := rt.New(rt.Options{
		ConfigPath: cfgPath, KeyPath: keyPath, AppliedPath: appliedPath,
		SkipTUN: true, TestMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	st := node2.Status()
	if st.LocationID != "loc-de" {
		t.Fatalf("location after restart = %q", st.LocationID)
	}
	// Bootstrap file still says loc-bootstrap
	raw, _ := os.ReadFile(cfgPath)
	if !containsBytes(raw, []byte(`"location_id": "loc-bootstrap"`)) && !containsBytes(raw, []byte(`"location_id":"loc-bootstrap"`)) {
		t.Fatalf("server.json should keep bootstrap location, got %s", raw)
	}
}

func TestConfigVersionSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(state, 0o700)
	cfgPath := writeMinimalServerJSON(t, dir, "http://127.0.0.1:9")
	appliedPath := filepath.Join(state, "applied-config.json")
	keyPath := filepath.Join(state, "node.key")

	node1, err := rt.New(rt.Options{
		ConfigPath: cfgPath, KeyPath: keyPath, AppliedPath: appliedPath,
		SkipTUN: true, TestMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := node1.ApplyConfig(controlplane.NodeConfig{
		NodeID: "n1", LocationID: "loc-fi", Enabled: true, Capacity: 5, ConfigVersion: 11,
	}); err != nil {
		t.Fatal(err)
	}
	node2, err := rt.New(rt.Options{
		ConfigPath: cfgPath, KeyPath: keyPath, AppliedPath: appliedPath,
		SkipTUN: true, TestMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if node2.Status().ConfigVersion != 11 {
		t.Fatalf("config_version=%d", node2.Status().ConfigVersion)
	}
}

func TestPersistenceFailureDoesNotAdvanceConfigVersion(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeMinimalServerJSON(t, dir, "http://127.0.0.1:9")
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(state, 0o700)
	appliedPath := filepath.Join(state, "applied-config.json")
	node, err := rt.New(rt.Options{
		ConfigPath: cfgPath, KeyPath: filepath.Join(state, "node.key"), AppliedPath: appliedPath,
		SkipTUN: true, TestMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := node.ApplyConfig(controlplane.NodeConfig{
		NodeID: "n1", LocationID: "loc-a", Enabled: true, Capacity: 1, ConfigVersion: 2,
	}); err != nil {
		t.Fatal(err)
	}
	// Make applied path unwritable by pointing at a directory as the file path.
	badPath := filepath.Join(state, "not-a-file")
	if err := os.MkdirAll(badPath, 0o700); err != nil {
		t.Fatal(err)
	}
	nodeBad, err := rt.New(rt.Options{
		ConfigPath: cfgPath, KeyPath: filepath.Join(state, "node.key"), AppliedPath: badPath,
		SkipTUN: true, TestMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Seed in-memory from good applied by loading good path first — recreate with good then swap?
	// Instead: new node with good applied, then force ApplyConfig to bad path via Options only at New.
	// nodeBad starts with no applied (badPath is dir). ConfigVersion 0.
	err = nodeBad.ApplyConfig(controlplane.NodeConfig{
		NodeID: "n1", LocationID: "loc-b", Enabled: true, Capacity: 1, ConfigVersion: 99,
	})
	if err == nil {
		t.Fatal("expected persistence failure")
	}
	if nodeBad.Status().ConfigVersion == 99 {
		t.Fatal("ConfigVersion must not advance on persistence failure")
	}
}

func TestPersistenceFailureLeavesOldSnapshot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory writability bits not reliable on Windows")
	}
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(state, 0o700)
	cfgPath := writeMinimalServerJSON(t, dir, "http://127.0.0.1:9")
	appliedPath := filepath.Join(state, "applied-config.json")

	if err := localconfig.SaveApplied(appliedPath, controlplane.NodeConfig{
		NodeID: "n1", LocationID: "loc-old", Enabled: true, Capacity: 1, ConfigVersion: 4,
	}); err != nil {
		t.Fatal(err)
	}
	oldRaw, _ := os.ReadFile(appliedPath)

	node, err := rt.New(rt.Options{
		ConfigPath: cfgPath, KeyPath: filepath.Join(dir, "node.key"), AppliedPath: appliedPath,
		SkipTUN: true, TestMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state, 0o555); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(state, 0o700) }()

	err = node.ApplyConfig(controlplane.NodeConfig{
		NodeID: "n1", LocationID: "loc-new", Enabled: true, Capacity: 1, ConfigVersion: 5,
	})
	if err == nil {
		t.Fatal("expected write failure")
	}
	_ = os.Chmod(state, 0o700)
	newRaw, err := os.ReadFile(appliedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(newRaw) != string(oldRaw) {
		t.Fatal("old applied snapshot must remain on disk")
	}
	if node.Status().ConfigVersion != 4 || node.Status().LocationID != "loc-old" {
		t.Fatalf("status=%+v", node.Status())
	}
}

func mustGenKey(t *testing.T) (*identity.NodeKey, error) {
	t.Helper()
	return identity.Generate()
}

func TestInitialRegistrationConfigPersisted(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(state, 0o700)
	pub, _, _ := ed25519.GenerateKey(nil)
	keysJSON := `{"issuer":"nyxveil-control-plane","keys":{"k1":"` + base64.StdEncoding.EncodeToString(pub) + `"},"updated_at":1}`

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/nodes/register", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(controlplane.RegisterResponse{
			NodeID:        "n1",
			Registered:    true,
			ConfigVersion: 2,
			Config: &controlplane.NodeConfig{
				NodeID:        "n1",
				LocationID:    "loc-canonical",
				Enabled:       true,
				Capacity:      50,
				ConfigVersion: 2,
			},
		})
	})
	mux.HandleFunc("/api/v1/node/ticket-keys", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(keysJSON))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfgPath := writeMinimalServerJSON(t, dir, srv.URL)
	appliedPath := filepath.Join(state, "applied-config.json")
	node, err := rt.New(rt.Options{
		ConfigPath:  cfgPath,
		KeyPath:     filepath.Join(state, "node.key"),
		AppliedPath: appliedPath,
		SkipTUN:     true,
		TestMode:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := node.Register(context.Background(), "boot-token")
	if err != nil {
		t.Fatal(err)
	}
	if resp.ConfigVersion != 2 {
		t.Fatal(resp.ConfigVersion)
	}
	snap, err := localconfig.LoadApplied(appliedPath)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Config.LocationID != "loc-canonical" || snap.Config.ConfigVersion != 2 {
		t.Fatalf("%+v", snap.Config)
	}
	// Restart sees applied
	node2, err := rt.New(rt.Options{
		ConfigPath: cfgPath, KeyPath: filepath.Join(state, "node.key"), AppliedPath: appliedPath,
		SkipTUN: true, TestMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if node2.Status().LocationID != "loc-canonical" || node2.Status().ConfigVersion != 2 {
		t.Fatalf("%+v", node2.Status())
	}
}

func TestRepairPreservesNewerAppliedConfig(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(state, 0o700)
	appliedPath := filepath.Join(state, "applied-config.json")
	_ = localconfig.SaveApplied(appliedPath, controlplane.NodeConfig{
		NodeID: "n1", LocationID: "loc-kept", Enabled: true, Capacity: 9, ConfigVersion: 10,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/nodes/register", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(controlplane.RegisterResponse{
			NodeID:        "n1",
			Registered:    true,
			ConfigVersion: 3, // older than applied
			Config: &controlplane.NodeConfig{
				NodeID: "n1", LocationID: "loc-stale", Enabled: true, Capacity: 1, ConfigVersion: 3,
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfgPath := writeMinimalServerJSON(t, dir, srv.URL)
	// Pre-existing node.key → PoP repair path (not fresh bootstrap).
	keyPath := filepath.Join(state, "node.key")
	k, err := mustGenKey(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Save(keyPath); err != nil {
		t.Fatal(err)
	}
	node, err := rt.New(rt.Options{
		ConfigPath: cfgPath, KeyPath: keyPath, AppliedPath: appliedPath,
		SkipTUN: true, TestMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = node.Register(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := localconfig.LoadApplied(appliedPath)
	if snap.Config.ConfigVersion != 10 || snap.Config.LocationID != "loc-kept" {
		t.Fatalf("repair overwrote applied: %+v", snap.Config)
	}
}

func containsBytes(b, sub []byte) bool {
	return len(sub) == 0 || (len(b) >= len(sub) && stringIndex(string(b), string(sub)) >= 0)
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// silence unused import if Start tests elsewhere use time
var _ = time.Second
