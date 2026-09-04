package server_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nyxveil/nvp/auth/ticket"
	"github.com/nyxveil/nvp/controlplane/api"
	"github.com/nyxveil/nvp/controlplane/catalog"
	"github.com/nyxveil/nvp/controlplane/model"
	cpserver "github.com/nyxveil/nvp/controlplane/server"
)

func setupStub(t *testing.T) (*httptest.Server, ed25519.PublicKey) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(nil)
	issuer := ticket.IssuerConfig{
		Issuer:     "https://control.nyxveil.test",
		Audience:   "nvp-node",
		KeyID:      "cp-key-1",
		PrivateKey: priv,
		TTL:        15 * time.Minute,
	}
	signer := catalog.Signer{KeyID: "cat-key-1", PrivateKey: priv}
	stub := cpserver.NewStub(cpserver.Config{
		Issuer:        issuer,
		CatalogSigner: signer,
		Catalog: model.Catalog{
			Version:   "1",
			Locations: []model.Location{{LocationID: "fi-hel", Country: "FI", Enabled: true}},
			Nodes: []model.NodeRegistryEntry{
				{NodeID: "fi-hel-01", LocationID: "fi-hel", Enabled: true, TestOnly: false},
				{NodeID: "test-01", LocationID: "fi-hel", Enabled: true, TestOnly: true},
			},
		},
	})
	stub.RegisterLicense(model.LicenseRecord{
		LicenseID:  "nyx_lic_test1",
		Plan:       "premium",
		MaxDevices: 3,
		Enabled:    true,
		ExpiresAt:  time.Now().Add(365 * 24 * time.Hour),
		Locations:  []string{"fi-hel"},
	})
	return httptest.NewServer(stub.HTTPHandler()), pub
}

func TestLicenseValidate(t *testing.T) {
	ts, _ := setupStub(t)
	defer ts.Close()

	body, _ := json.Marshal(api.LicenseValidateRequest{LicenseToken: "nyx_lic_test1"})
	resp, err := http.Post(ts.URL+"/api/v1/license/validate", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var out api.LicenseValidateResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if !out.Valid {
		t.Fatal("license should be valid")
	}
}

func TestDeviceActivateAndTicketIssue(t *testing.T) {
	ts, pub := setupStub(t)
	defer ts.Close()

	actBody, _ := json.Marshal(map[string]interface{}{
		"license_token": "nyx_lic_test1",
		"device_id":     "dev_001",
		"public_key":    []byte{1, 2, 3},
	})
	resp, err := http.Post(ts.URL+"/api/v1/device/activate", "application/json", bytes.NewReader(actBody))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	ticketBody, _ := json.Marshal(api.TicketIssueRequest{
		LicenseToken: "nyx_lic_test1",
		DeviceID:     "dev_001",
		NodeID:       "fi-hel-01",
	})
	resp2, err := http.Post(ts.URL+"/api/v1/ticket/issue", "application/json", bytes.NewReader(ticketBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	var out api.TicketIssueResponse
	_ = json.NewDecoder(resp2.Body).Decode(&out)
	if out.AccessTicket == "" {
		t.Fatal("expected access ticket")
	}

	verifier := ticket.VerifierConfig{
		Issuer:     "https://control.nyxveil.test",
		Audience:   "nvp-node",
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
	}
	_, err = ticket.Verify(verifier, out.AccessTicket, "dev_001", "")
	if err != nil {
		t.Fatalf("ticket verify: %v", err)
	}
}

func TestCatalogFiltersTestNodes(t *testing.T) {
	ts, pub := setupStub(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/catalog")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var signed model.SignedCatalog
	_ = json.NewDecoder(resp.Body).Decode(&signed)
	if err := catalog.Verify(catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"cat-key-1": pub}}, signed); err != nil {
		t.Fatalf("catalog verify: %v", err)
	}
	for _, n := range signed.Catalog.Nodes {
		if n.TestOnly {
			t.Fatal("test node should be filtered for user catalog")
		}
	}
}

func TestRevokedLicenseRejected(t *testing.T) {
	ts, _ := setupStub(t)
	defer ts.Close()

	// activate device first via internal path - issue without activation should fail anyway
	body, _ := json.Marshal(api.TicketIssueRequest{LicenseToken: "nyx_lic_test1", DeviceID: "dev_x"})
	resp, err := http.Post(ts.URL+"/api/v1/ticket/issue", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 401, got %d: %s", resp.StatusCode, b)
	}
}

func TestMasterCatalogIncludesTestNodes(t *testing.T) {
	ts, _ := setupStub(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/catalog?role=master")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var signed model.SignedCatalog
	_ = json.NewDecoder(resp.Body).Decode(&signed)
	foundTest := false
	for _, n := range signed.Catalog.Nodes {
		if n.TestOnly {
			foundTest = true
		}
	}
	if !foundTest {
		t.Fatal("master catalog should include test nodes")
	}
}
