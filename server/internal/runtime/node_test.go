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
	"github.com/nyxveil/server/internal/localconfig"
	rt "github.com/nyxveil/server/internal/runtime"
)

func TestStartFailClosedWithoutSkipTUN(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Linux may have TUN; fail-closed asserted on non-Linux")
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "server.json")
	cfg := localconfig.Default()
	cfg.ControlPlaneURL = "http://127.0.0.1:9"
	cfg.NodeID = "test-node"
	cfg.LocationID = "loc"
	cfg.DisplayName = "t"
	cfg.PublicHost = "203.0.113.1"
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, b, 0o644); err != nil {
		t.Fatal(err)
	}

	node, err := rt.New(rt.Options{
		ConfigPath:  cfgPath,
		KeyPath:     filepath.Join(dir, "node.key"),
		AppliedPath: filepath.Join(dir, "applied.json"),
		ControlHTTP: "127.0.0.1:0",
		SkipTUN:     false,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := node.Start(ctx); err == nil {
		_ = node.Shutdown(context.Background())
		t.Fatal("expected TUN fail-closed error without --skip-tun")
	}
}

func TestApplyConfigVersionBlockAndPersist(t *testing.T) {
	dir := t.TempDir()
	pub, _, _ := ed25519.GenerateKey(nil)
	keysJSON := `{"issuer":"nyxveil-control-plane","keys":{"k1":"` + base64.StdEncoding.EncodeToString(pub) + `"},"updated_at":1}`
	_ = os.WriteFile(filepath.Join(dir, "ticket-keys.json"), []byte(keysJSON), 0o600)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/nodes/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"accepted":true,"status":"ok","config_version":1}`))
	})
	mux.HandleFunc("/api/v1/revocation", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"revoked_jtis":[],"revoked_licenses":[],"revoked_devices":[],"updated_at":1}`))
	})
	mux.HandleFunc("/api/v1/node/ticket-keys", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(keysJSON))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfgPath := filepath.Join(dir, "server.json")
	cfg := localconfig.Default()
	cfg.ControlPlaneURL = srv.URL
	cfg.NodeID = "n1"
	cfg.LocationID = "loc"
	cfg.DisplayName = "t"
	cfg.PublicHost = "127.0.0.1"
	cfg.TLSListen = "127.0.0.1:0"
	cfg.QUICListen = "127.0.0.1:0"
	raw, _ := json.Marshal(cfg)
	_ = os.WriteFile(cfgPath, raw, 0o644)

	node, err := rt.New(rt.Options{
		ConfigPath:  cfgPath,
		KeyPath:     filepath.Join(dir, "node.key"),
		AppliedPath: filepath.Join(dir, "applied.json"),
		ControlHTTP: "127.0.0.1:0",
		SkipTUN:     true,
		TestMode:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := node.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer node.Shutdown(context.Background())

	minVer := "99.0.0"
	err = node.ApplyConfig(controlplane.NodeConfig{
		NodeID:               "n1",
		Enabled:              true,
		Capacity:             5,
		ConfigVersion:        5,
		MinimumServerVersion: &minVer,
		TransportPolicyJSON:  `{"tls":true,"quic":false}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	st := node.Status()
	if !st.VersionBlocked {
		t.Fatal("expected version blocked")
	}
	if st.Accepting {
		t.Fatal("must not accept when version blocked")
	}
	if _, err := os.Stat(filepath.Join(dir, "applied.json")); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterRequiresPublicHost(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "server.json")
	cfg := localconfig.Default()
	cfg.ControlPlaneURL = "http://127.0.0.1:9"
	cfg.NodeID = "n1"
	cfg.LocationID = "loc"
	cfg.DisplayName = "t"
	// PublicHost empty
	raw, _ := json.Marshal(cfg)
	_ = os.WriteFile(cfgPath, raw, 0o644)

	node, err := rt.New(rt.Options{
		ConfigPath: cfgPath,
		KeyPath:    filepath.Join(dir, "node.key"),
		SkipTUN:    true,
		TestMode:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = node.Register(context.Background(), "boot-token")
	if err == nil {
		t.Fatal("expected public_host required")
	}
}
