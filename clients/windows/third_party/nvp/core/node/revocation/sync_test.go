package revocation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nyxveil/nvp/core/controlplane/api"
)

func TestSyncCache(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, api.RevocationListResponse{
			RevokedDevices: []string{"dev_x"},
			UpdatedAt:      123,
		})
	}))
	defer ts.Close()

	c := NewSyncCache(ts.URL)
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !c.IsRevoked("", "", "dev_x") {
		t.Fatal("expected device revoked")
	}
	if c.UpdatedAt() != 123 {
		t.Fatalf("updated_at=%d", c.UpdatedAt())
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
