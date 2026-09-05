package controlplane

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHeartbeatSignsRequestHeaders(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var saw http.Header
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		saw = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		_ = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accepted":true,"status":"ok","config_version":3}`))
	}))
	defer srv.Close()

	c, err := NewClient(srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.NodeID = "fi-hel-01"
	c.PrivateKey = priv

	resp, err := c.Heartbeat(context.Background(), HeartbeatRequest{
		NodeID:          "fi-hel-01",
		CurrentSessions: 2,
		Capacity:        100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Accepted || resp.ConfigVersion != 3 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if method != http.MethodPost {
		t.Fatalf("method=%s", method)
	}
	if path != "/api/v1/nodes/fi-hel-01/health" {
		t.Fatalf("path=%s", path)
	}
	for _, h := range []string{"X-Node-Id", "X-Node-Timestamp", "X-Node-Nonce", "X-Node-Signature"} {
		if saw.Get(h) == "" {
			t.Fatalf("missing signed header %s", h)
		}
	}
	if saw.Get("X-Node-Id") != "fi-hel-01" {
		t.Fatalf("node id header=%s", saw.Get("X-Node-Id"))
	}
	if saw.Get("Content-Type") != "application/json" {
		t.Fatalf("content-type=%s", saw.Get("Content-Type"))
	}
}

func TestGetConfigSignsGET(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var saw http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = r.Header.Clone()
		if r.Method != http.MethodGet {
			t.Errorf("want GET got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"node_id":"n1","location_id":"fi-hel","enabled":true,"capacity":50,"config_version":7}`))
	}))
	defer srv.Close()

	c, err := NewClient(srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.NodeID = "n1"
	c.PrivateKey = priv
	cfg, err := c.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConfigVersion != 7 || cfg.Capacity != 50 {
		t.Fatalf("%+v", cfg)
	}
	if saw.Get("X-Node-Signature") == "" {
		t.Fatal("expected signature on GET")
	}
}

func TestRegisterDoesNotRequireSignature(t *testing.T) {
	var sawSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawSig = r.Header.Get("X-Node-Signature")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"node_id":"n1","registered":true,"node_token":"t","config_version":1}`))
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Register(context.Background(), RegisterRequest{
		BootstrapToken: "boot",
		NodeID:         "n1",
		LocationID:     "fi-hel",
		DisplayName:    "Test",
		PublicIdentity: make([]byte, 32),
		PublicKey:      make([]byte, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	if sawSig != "" {
		t.Fatal("register should not sign with node key")
	}
}
