package connector_test

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/connector"
	"github.com/nyxveil/nvp/core/controlplane/api"
	"github.com/nyxveil/nvp/core/controlplane/catalog"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/transport"
)

func TestFetchCatalogDoesNotUseRoleQuery(t *testing.T) {
	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(model.SignedCatalog{Catalog: model.Catalog{Version: "1"}})
	}))
	defer ts.Close()

	cp := connector.NewControlPlaneClient(ts.URL)
	if _, err := cp.FetchCatalog(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if gotQuery != "" {
		t.Fatalf("catalog request must not use query string, got %q", gotQuery)
	}
}

func TestFetchCatalogSendsBearer(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(model.SignedCatalog{})
	}))
	defer ts.Close()

	cp := connector.NewControlPlaneClient(ts.URL)
	if _, err := cp.FetchCatalog(context.Background(), "ticket"); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer ticket" {
		t.Fatalf("auth header: %q", gotAuth)
	}
}

func TestPrepareSelectionRequiresSignedCatalog(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "cat-key-1", PrivateKey: priv}
	cat := model.Catalog{
		Version: "1",
		Nodes: []model.NodeRegistryEntry{{
			NodeID:     "n1",
			LocationID: "fi-hel",
			Enabled:    true,
			Capacity:   10,
			SPKIPin:    []byte{1, 2, 3, 4},
			ServerName: "n1.example",
			Endpoints:  []transport.Endpoint{{Host: "127.0.0.1", Port: 443}},
		}},
	}
	signed, err := signer.Sign(cat)
	if err != nil {
		t.Fatal(err)
	}

	var issueNode, issueLoc string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/license/validate":
			_ = json.NewEncoder(w).Encode(api.LicenseValidateResponse{Valid: true})
		case "/api/v1/catalog":
			_ = json.NewEncoder(w).Encode(signed)
		case "/api/v1/ticket/issue":
			var req api.TicketIssueRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			issueNode = req.NodeID
			issueLoc = req.LocationID
			_ = json.NewEncoder(w).Encode(api.TicketIssueResponse{AccessTicket: "tok"})
		default:
			t.Errorf("unexpected path %s (PrepareSelection must not dial VPN)", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	c := &connector.Connector{
		CP:                connector.NewControlPlaneClient(ts.URL),
		CatalogVerifyKeys: catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"cat-key-1": pub}},
	}
	ticket, node, err := c.PrepareSelection(context.Background(), connector.ConnectConfig{
		LicenseToken: "lic",
		DeviceID:     "dev",
		LocationID:   "fi-hel",
		Role:         "user",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ticket != "tok" || node.NodeID != "n1" {
		t.Fatalf("ticket=%q node=%q", ticket, node.NodeID)
	}
	if issueNode != "" {
		t.Fatalf("failover tickets must not pin NodeID, got %q", issueNode)
	}
	if issueLoc != "fi-hel" {
		t.Fatalf("expected location_id=fi-hel on issue, got %q", issueLoc)
	}
	if len(node.SPKIPin) == 0 || node.ServerName != "n1.example" {
		t.Fatalf("expected pin and server name on selected node, got %+v", node)
	}
}

func TestPrepareSelectionRejectsUnsignedCatalog(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/license/validate":
			_ = json.NewEncoder(w).Encode(api.LicenseValidateResponse{Valid: true})
		case "/api/v1/catalog":
			_ = json.NewEncoder(w).Encode(model.SignedCatalog{
				Catalog: model.Catalog{
					Version:   "1",
					IssuedAt:  time.Now().UTC(),
					ExpiresAt: time.Now().UTC().Add(time.Hour),
					Nodes:     []model.NodeRegistryEntry{{NodeID: "n1", Enabled: true}},
				},
				KeyID: "cat-key-1",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	pub, _, _ := ed25519.GenerateKey(nil)
	c := &connector.Connector{
		CP:                connector.NewControlPlaneClient(ts.URL),
		CatalogVerifyKeys: catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"cat-key-1": pub}},
	}
	_, _, err := c.PrepareSelection(context.Background(), connector.ConnectConfig{
		LicenseToken: "lic",
		DeviceID:     "dev",
	})
	if err == nil {
		t.Fatal("expected catalog verify failure")
	}
}

func TestOpenSessionRequirePinFailsClosed(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "cat-key-1", PrivateKey: priv}
	signed, err := signer.Sign(model.Catalog{
		Version: "1",
		Nodes: []model.NodeRegistryEntry{{
			NodeID:   "n1",
			Enabled:  true,
			Capacity: 10,
			// no SPKIPin
			Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: 1}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/license/validate":
			_ = json.NewEncoder(w).Encode(api.LicenseValidateResponse{Valid: true})
		case "/api/v1/catalog":
			_ = json.NewEncoder(w).Encode(signed)
		case "/api/v1/ticket/issue":
			_ = json.NewEncoder(w).Encode(api.TicketIssueResponse{AccessTicket: "tok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	c := &connector.Connector{
		CP:                connector.NewControlPlaneClient(ts.URL),
		CatalogVerifyKeys: catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"cat-key-1": pub}},
		RequirePin:        true,
	}
	_, _, _, err = c.OpenSession(context.Background(), connector.ConnectConfig{
		LicenseToken: "lic",
		DeviceID:     "dev",
	})
	if err == nil {
		t.Fatal("expected require-pin failure before dial")
	}
}
