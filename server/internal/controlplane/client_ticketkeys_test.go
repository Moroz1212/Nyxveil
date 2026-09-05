package controlplane

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetTicketKeysSignsGET(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var saw http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = r.Header.Clone()
		if r.URL.Path != "/api/v1/node/ticket-keys" {
			t.Errorf("path=%s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issuer":"nyxveil-control-plane","keys":{"k1":"` + base64.StdEncoding.EncodeToString(pub) + `"},"updated_at":123}`))
	}))
	defer srv.Close()

	c, err := NewClient(srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.NodeID = "n1"
	c.PrivateKey = priv
	resp, err := c.GetTicketKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Issuer != "nyxveil-control-plane" || len(resp.Keys) != 1 || resp.UpdatedAt != 123 {
		t.Fatalf("%+v", resp)
	}
	if saw.Get("X-Node-Signature") == "" {
		t.Fatal("expected signature")
	}
}
