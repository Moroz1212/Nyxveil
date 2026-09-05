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
	"strings"
	"testing"

	"github.com/nyxveil/server/internal/controlplane"
	"github.com/nyxveil/server/internal/identity"
	rt "github.com/nyxveil/server/internal/runtime"
)

func TestSuccessfulHTTPRegistrationLocalFailurePreservesIdentity(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(state, 0o700)
	keyPath := filepath.Join(state, "node.key")

	pub, _, _ := ed25519.GenerateKey(nil)
	keysJSON := `{"issuer":"nyxveil-control-plane","keys":{"k1":"` + base64.StdEncoding.EncodeToString(pub) + `"},"updated_at":1}`

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/nodes/register", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// HTTP 200 with intentionally unparseable updated_at → AcceptedLocalError.
		_, _ = w.Write([]byte(`{
  "node_id":"n1",
  "registered":true,
  "config_version":1,
  "config":{
    "node_id":"n1",
    "location_id":"hel-1",
    "enabled":true,
    "capacity":100,
    "config_version":1,
    "updated_at":"not-a-valid-timestamp"
  }
}`))
	})
	mux.HandleFunc("/api/v1/node/ticket-keys", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(keysJSON))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfgPath := writeMinimalServerJSON(t, dir, srv.URL)
	node, err := rt.New(rt.Options{
		ConfigPath:  cfgPath,
		KeyPath:     keyPath,
		AppliedPath: filepath.Join(state, "applied-config.json"),
		SkipTUN:     true,
		TestMode:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}

	_, err = node.Register(context.Background(), "boot-token")
	if err == nil {
		t.Fatal("expected local decode failure after HTTP 200")
	}
	if !strings.Contains(err.Error(), "Control Plane may already have registered") {
		t.Fatalf("missing warning: %v", err)
	}
	if !controlplane.IsAcceptedLocal(err) && !strings.Contains(err.Error(), "local processing failed") {
		t.Fatalf("expected accepted-local wrap: %v", err)
	}
	after, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("node.key must be preserved after HTTP-200 local failure")
	}
}

func TestRetryAfterLocalFailureUsesSameNodeIdentity(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(state, 0o700)
	keyPath := filepath.Join(state, "node.key")

	pub, _, _ := ed25519.GenerateKey(nil)
	keysJSON := `{"issuer":"nyxveil-control-plane","keys":{"k1":"` + base64.StdEncoding.EncodeToString(pub) + `"},"updated_at":1}`

	var sawBootstrap, sawPoP int
	var firstPubKey, secondPubKey string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/nodes/register", func(w http.ResponseWriter, r *http.Request) {
		var req controlplane.RegisterRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		pk := base64.StdEncoding.EncodeToString(req.PublicKey)
		if req.BootstrapToken != "" {
			sawBootstrap++
			firstPubKey = pk
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"node_id":"n1","registered":true,"config_version":1,"config":{"node_id":"n1","location_id":"hel-1","enabled":true,"capacity":1,"config_version":1,"updated_at":"garbage"}}`))
			return
		}
		if req.NodeToken != "" {
			sawPoP++
			secondPubKey = pk
			_ = json.NewEncoder(w).Encode(controlplane.RegisterResponse{
				NodeID:        "n1",
				Registered:    true,
				ConfigVersion: 2,
				Config: &controlplane.NodeConfig{
					NodeID: "n1", LocationID: "hel-1", Enabled: true, Capacity: 50, ConfigVersion: 2,
					UpdatedAt: controlplane.APITime{},
				},
			})
			return
		}
		http.Error(w, "expected bootstrap or pop", 400)
	})
	mux.HandleFunc("/api/v1/node/ticket-keys", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(keysJSON))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfgPath := writeMinimalServerJSON(t, dir, srv.URL)
	node, err := rt.New(rt.Options{
		ConfigPath:  cfgPath,
		KeyPath:     keyPath,
		AppliedPath: filepath.Join(state, "applied-config.json"),
		SkipTUN:     true,
		TestMode:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = node.Register(context.Background(), "boot-token")
	if err == nil {
		t.Fatal("expected first register local failure")
	}

	rawKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	keyOnDisk, err := identity.ParsePEM(rawKey)
	if err != nil {
		t.Fatal(err)
	}

	// Same process retry: must use PoP with the same public key (no new identity).
	resp, err := node.Register(context.Background(), "boot-token-ignored")
	if err != nil {
		t.Fatal(err)
	}
	if resp.ConfigVersion != 2 {
		t.Fatalf("%+v", resp)
	}
	if sawBootstrap != 1 || sawPoP != 1 {
		t.Fatalf("bootstrap=%d pop=%d", sawBootstrap, sawPoP)
	}
	if firstPubKey == "" || firstPubKey != secondPubKey {
		t.Fatalf("public key changed across retry: %q vs %q", firstPubKey, secondPubKey)
	}
	wantPK := base64.StdEncoding.EncodeToString(keyOnDisk.Public)
	if firstPubKey != wantPK {
		t.Fatalf("retry did not reuse disk identity")
	}
}
