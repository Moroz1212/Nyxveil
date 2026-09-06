package server_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/auth/nodeauth"
	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/controlplane/api"
	"github.com/nyxveil/nvp/core/controlplane/catalog"
	"github.com/nyxveil/nvp/core/controlplane/model"
	cpserver "github.com/nyxveil/nvp/core/controlplane/server"
	"github.com/nyxveil/nvp/core/controlplane/store"
	"github.com/nyxveil/nvp/core/node"
	"github.com/nyxveil/nvp/core/transport"
)

type prodEnv struct {
	ts     *httptest.Server
	prod   *cpserver.ProductionServer
	st     *store.Store
	pub    ed25519.PublicKey
	issuer ticket.IssuerConfig
}

func setupProduction(t *testing.T) (*httptest.Server, ed25519.PublicKey) {
	t.Helper()
	env := setupProductionEnv(t)
	return env.ts, env.pub
}

func setupProductionEnv(t *testing.T) *prodEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "cp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	pub, priv, _ := ed25519.GenerateKey(nil)
	issuer := ticket.IssuerConfig{
		Issuer:     "https://control.nyxveil.test",
		Audience:   "nvp-node",
		KeyID:      "cp-key-1",
		PrivateKey: priv,
		TTL:        15 * time.Minute,
	}
	signer := catalog.Signer{KeyID: "cat-key-1", PrivateKey: priv}
	prod := cpserver.NewProduction(cpserver.Config{
		Issuer:        issuer,
		CatalogSigner: signer,
		Catalog: model.Catalog{
			Version: "1",
			Locations: []model.Location{
				{LocationID: "fi-hel", Country: "FI", Enabled: true},
				{LocationID: "de-fra", Country: "DE", Enabled: true},
			},
			Nodes: []model.NodeRegistryEntry{
				{NodeID: "fi-hel-01", LocationID: "fi-hel", Enabled: true, TestOnly: false},
				{NodeID: "de-fra-01", LocationID: "de-fra", Enabled: true, TestOnly: false},
				{NodeID: "test-01", LocationID: "fi-hel", Enabled: true, TestOnly: true},
			},
		},
	}, st)
	if err := prod.RegisterLicense(context.Background(), model.LicenseRecord{
		LicenseID:  "nyx_lic_test1",
		Plan:       "premium",
		MaxDevices: 3,
		Enabled:    true,
		Secret:     "test-secret",
		ExpiresAt:  time.Now().Add(365 * 24 * time.Hour),
		Locations:  []string{"fi-hel"},
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(prod.HTTPHandler())
	t.Cleanup(ts.Close)
	return &prodEnv{ts: ts, prod: prod, st: st, pub: pub, issuer: issuer}
}

func activateDevice(t *testing.T, ts *httptest.Server, licenseToken, deviceID string, pub ed25519.PublicKey) {
	t.Helper()
	actBody, _ := json.Marshal(map[string]interface{}{
		"license_token": licenseToken,
		"device_id":     deviceID,
		"public_key":    []byte(pub),
	})
	resp, err := http.Post(ts.URL+"/api/v1/device/activate", "application/json", bytes.NewReader(actBody))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func issueTicket(t *testing.T, ts *httptest.Server, licenseToken, deviceID, nodeID string) string {
	t.Helper()
	ticketBody, _ := json.Marshal(api.TicketIssueRequest{
		LicenseToken: licenseToken,
		DeviceID:     deviceID,
		NodeID:       nodeID,
	})
	resp, err := http.Post(ts.URL+"/api/v1/ticket/issue", "application/json", bytes.NewReader(ticketBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out api.TicketIssueResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.AccessTicket == "" {
		t.Fatal("expected access ticket")
	}
	return out.AccessTicket
}

func TestProductionLicenseValidate(t *testing.T) {
	ts, _ := setupProduction(t)

	body, _ := json.Marshal(api.LicenseValidateRequest{LicenseToken: "nyx_lic_test1:test-secret"})
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

func TestProductionDeviceActivateAndTicketIssue(t *testing.T) {
	ts, pub := setupProduction(t)

	devPub, _, _ := ed25519.GenerateKey(nil)
	activateDevice(t, ts, "nyx_lic_test1:test-secret", "dev_001", devPub)
	tok := issueTicket(t, ts, "nyx_lic_test1:test-secret", "dev_001", "fi-hel-01")

	verifier := ticket.VerifierConfig{
		Issuer:     "https://control.nyxveil.test",
		Audience:   "nvp-node",
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
	}
	if _, err := ticket.VerifyAt(verifier, tok, "dev_001", "fi-hel-01", "fi-hel"); err != nil {
		t.Fatalf("ticket verify: %v", err)
	}
}

func TestProductionRevocationList(t *testing.T) {
	env := setupProductionEnv(t)

	devPub, _, _ := ed25519.GenerateKey(nil)
	activateDevice(t, env.ts, "nyx_lic_test1:test-secret", "dev_rev", devPub)

	body, _ := json.Marshal(map[string]string{"license_token": "nyx_lic_test1:test-secret", "device_id": "dev_rev"})
	resp, err := http.Post(env.ts.URL+"/api/v1/device/remove", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	nodePub, nodePriv, _ := ed25519.GenerateKey(nil)
	if err := env.prod.RegisterNodeIdentity(context.Background(), "fi-hel-01", nodePub); err != nil {
		t.Fatal(err)
	}
	token := nodeauth.SignToken("fi-hel-01", nodePriv, time.Now())
	req, _ := http.NewRequest(http.MethodGet, env.ts.URL+"/api/v1/revocation", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Node-ID", "fi-hel-01")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp2.StatusCode)
	}
	var list api.RevocationListResponse
	_ = json.NewDecoder(resp2.Body).Decode(&list)
	found := false
	for _, d := range list.RevokedDevices {
		if d == "dev_rev" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected dev_rev in revocation list")
	}
}

func TestProductionVersion(t *testing.T) {
	ts, _ := setupProduction(t)

	resp, err := http.Get(ts.URL + "/api/v1/version")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var out api.VersionResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.ControlPlaneVersion == "" {
		t.Fatal("expected version")
	}
}

func TestHeartbeatDoesNotOverwriteNodeConfiguration(t *testing.T) {
	env := setupProductionEnv(t)
	ctx := context.Background()
	pin := []byte{1, 2, 3, 4, 5}
	before := model.NodeRegistryEntry{
		NodeID: "fi-hel-01", LocationID: "fi-hel", Country: "FI", City: "Helsinki",
		DisplayName: "Helsinki 01", Status: node.StatusHealthy, Enabled: true, TestOnly: true,
		Draining: false, ProtocolVersion: 1, ServerVersion: "2.0.0",
		ServerName: "vpn.example", SPKIPin: pin,
		Endpoints: []transport.Endpoint{{Host: "vpn.example", Port: 443}},
		Capacity:  100, CurrentSessions: 1,
		Health:   model.HealthInfo{Healthy: true, SessionCount: 1},
		LastSeen: time.Now().UTC().Add(-time.Hour).Truncate(time.Second),
	}
	if err := env.st.CreateOrUpdateNodeConfig(ctx, before); err != nil {
		t.Fatal(err)
	}
	nodePub, nodePriv, _ := ed25519.GenerateKey(nil)
	if err := env.prod.RegisterNodeIdentity(ctx, "fi-hel-01", nodePub); err != nil {
		t.Fatal(err)
	}
	token := nodeauth.SignToken("fi-hel-01", nodePriv, time.Now())
	body, _ := json.Marshal(api.NodeHeartbeatRequest{
		NodeID: "fi-hel-01", NodeToken: token, Capacity: 250, CurrentSessions: 99,
	})
	resp, err := http.Post(env.ts.URL+"/api/v1/nodes/fi-hel-01/health", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("heartbeat status=%d", resp.StatusCode)
	}
	got, err := env.st.GetNode(ctx, "fi-hel-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.LocationID != before.LocationID || got.Country != before.Country ||
		got.City != before.City || got.DisplayName != before.DisplayName ||
		got.ServerName != before.ServerName || got.ServerVersion != before.ServerVersion ||
		got.ProtocolVersion != before.ProtocolVersion || got.TestOnly != before.TestOnly ||
		got.Enabled != before.Enabled || got.Draining != before.Draining ||
		got.Status != before.Status || !bytes.Equal(got.SPKIPin, before.SPKIPin) {
		t.Fatalf("static fields overwritten:\nbefore=%+v\ngot=%+v", before, got)
	}
	if len(got.Endpoints) != 1 || got.Endpoints[0].Host != "vpn.example" {
		t.Fatalf("endpoints overwritten: %+v", got.Endpoints)
	}
	if got.CurrentSessions != 99 || got.Capacity != 250 {
		t.Fatalf("health not updated: %+v", got)
	}
}

func refreshTicket(t *testing.T, env *prodEnv, licenseToken, deviceID, oldTok string) *ticket.Claims {
	t.Helper()
	body, _ := json.Marshal(api.TicketRefreshRequest{
		LicenseToken: licenseToken,
		DeviceID:     deviceID,
		AccessTicket: oldTok,
	})
	resp, err := http.Post(env.ts.URL+"/api/v1/ticket/refresh", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("refresh status=%d body=%s", resp.StatusCode, b)
	}
	var out api.TicketIssueResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	claims, err := ticket.VerifyIdentity(ticket.VerifierConfig{
		Issuer: env.issuer.Issuer, Audience: env.issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": env.pub},
	}, out.AccessTicket, deviceID)
	if err != nil {
		t.Fatal(err)
	}
	return claims
}

func issueTicketAt(t *testing.T, ts *httptest.Server, licenseToken, deviceID, nodeID, locationID string) string {
	t.Helper()
	ticketBody, _ := json.Marshal(api.TicketIssueRequest{
		LicenseToken: licenseToken,
		DeviceID:     deviceID,
		NodeID:       nodeID,
		LocationID:   locationID,
	})
	resp, err := http.Post(ts.URL+"/api/v1/ticket/issue", "application/json", bytes.NewReader(ticketBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("issue status=%d body=%s", resp.StatusCode, b)
	}
	var out api.TicketIssueResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.AccessTicket == "" {
		t.Fatal("expected access ticket")
	}
	return out.AccessTicket
}

// TestTicketRefreshPreservesNodeScope: old Locations=[FI] NodeScope=[NodeA] →
// after refresh (unrestricted node policy) same Locations and NodeScope.
func TestTicketRefreshPreservesNodeScope(t *testing.T) {
	env := setupProductionEnv(t)
	devPub, _, _ := ed25519.GenerateKey(nil)
	activateDevice(t, env.ts, "nyx_lic_test1:test-secret", "dev_scope", devPub)
	oldTok := issueTicket(t, env.ts, "nyx_lic_test1:test-secret", "dev_scope", "fi-hel-01")

	oldClaims, err := ticket.VerifyIdentity(ticket.VerifierConfig{
		Issuer: env.issuer.Issuer, Audience: env.issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": env.pub},
	}, oldTok, "dev_scope")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(oldClaims.Locations, []string{"fi-hel"}) ||
		!reflect.DeepEqual(oldClaims.NodeScope, []string{"fi-hel-01"}) {
		t.Fatalf("precondition: Locations=%v NodeScope=%v", oldClaims.Locations, oldClaims.NodeScope)
	}

	claims := refreshTicket(t, env, "nyx_lic_test1:test-secret", "dev_scope", oldTok)
	if !reflect.DeepEqual(claims.NodeScope, []string{"fi-hel-01"}) {
		t.Fatalf("refresh must preserve NodeScope, got %v", claims.NodeScope)
	}
	if !reflect.DeepEqual(claims.Locations, []string{"fi-hel"}) {
		t.Fatalf("expected Locations [fi-hel], got %v", claims.Locations)
	}
}

// TestTicketRefreshNeverWidensNodeScope: refresh cannot expand NodeScope beyond
// the prior grant even when administrative allowlist is wider.
func TestTicketRefreshNeverWidensNodeScope(t *testing.T) {
	env := setupProductionEnv(t)
	devPub, _, _ := ed25519.GenerateKey(nil)
	activateDevice(t, env.ts, "nyx_lic_test1:test-secret", "dev_widen", devPub)
	oldTok := issueTicket(t, env.ts, "nyx_lic_test1:test-secret", "dev_widen", "fi-hel-01")
	oldClaims, err := ticket.VerifyIdentity(ticket.VerifierConfig{
		Issuer: env.issuer.Issuer, Audience: env.issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": env.pub},
	}, oldTok, "dev_widen")
	if err != nil {
		t.Fatal(err)
	}
	lic, err := env.st.GetLicense(context.Background(), "nyx_lic_test1")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := cpserver.BuildRefreshTicketWithNodePolicy(env.issuer, oldClaims, lic, []byte(devPub),
		[]string{"fi-hel-01", "de-fra-01", "extra-node"})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ticket.VerifyIdentity(ticket.VerifierConfig{
		Issuer: env.issuer.Issuer, Audience: env.issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": env.pub},
	}, tok, "dev_widen")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(claims.NodeScope, []string{"fi-hel-01"}) {
		t.Fatalf("NodeScope must not widen, got %v", claims.NodeScope)
	}
}

// TestTicketRefreshIntersectsCurrentLocations: old FI,DE → license FI only → FI.
func TestTicketRefreshIntersectsCurrentLocations(t *testing.T) {
	env := setupProductionEnv(t)
	if err := env.prod.RegisterLicense(context.Background(), model.LicenseRecord{
		LicenseID: "nyx_lic_intersect", Plan: "premium", MaxDevices: 3, Enabled: true,
		Secret: "intersect-secret", ExpiresAt: time.Now().Add(24 * time.Hour),
		Locations: []string{"fi-hel", "de-fra"},
	}); err != nil {
		t.Fatal(err)
	}
	devPub, _, _ := ed25519.GenerateKey(nil)
	activateDevice(t, env.ts, "nyx_lic_intersect:intersect-secret", "dev_intersect", devPub)
	oldTok := issueTicketAt(t, env.ts, "nyx_lic_intersect:intersect-secret", "dev_intersect", "", "")

	oldClaims, err := ticket.VerifyIdentity(ticket.VerifierConfig{
		Issuer: env.issuer.Issuer, Audience: env.issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": env.pub},
	}, oldTok, "dev_intersect")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(oldClaims.Locations, []string{"fi-hel", "de-fra"}) {
		t.Fatalf("precondition Locations=%v", oldClaims.Locations)
	}

	lic, err := env.st.GetLicense(context.Background(), "nyx_lic_intersect")
	if err != nil {
		t.Fatal(err)
	}
	lic.Locations = []string{"fi-hel"}
	if err := env.st.UpsertLicense(context.Background(), *lic); err != nil {
		t.Fatal(err)
	}

	claims := refreshTicket(t, env, "nyx_lic_intersect:intersect-secret", "dev_intersect", oldTok)
	if !reflect.DeepEqual(claims.Locations, []string{"fi-hel"}) {
		t.Fatalf("expected intersected [fi-hel], got %v", claims.Locations)
	}
}

// TestTicketRefreshRejectsEmptyIntersection: old DE only, license FI only → refresh error.
func TestTicketRefreshRejectsEmptyIntersection(t *testing.T) {
	env := setupProductionEnv(t)
	if err := env.prod.RegisterLicense(context.Background(), model.LicenseRecord{
		LicenseID: "nyx_lic_empty_ix", Plan: "premium", MaxDevices: 3, Enabled: true,
		Secret: "empty-ix-secret", ExpiresAt: time.Now().Add(24 * time.Hour),
		Locations: []string{"de-fra"},
	}); err != nil {
		t.Fatal(err)
	}
	devPub, _, _ := ed25519.GenerateKey(nil)
	activateDevice(t, env.ts, "nyx_lic_empty_ix:empty-ix-secret", "dev_empty_ix", devPub)
	oldTok := issueTicketAt(t, env.ts, "nyx_lic_empty_ix:empty-ix-secret", "dev_empty_ix", "", "de-fra")

	lic, err := env.st.GetLicense(context.Background(), "nyx_lic_empty_ix")
	if err != nil {
		t.Fatal(err)
	}
	lic.Locations = []string{"fi-hel"}
	if err := env.st.UpsertLicense(context.Background(), *lic); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(api.TicketRefreshRequest{
		LicenseToken: "nyx_lic_empty_ix:empty-ix-secret",
		DeviceID:     "dev_empty_ix",
		AccessTicket: oldTok,
	})
	resp, err := http.Post(env.ts.URL+"/api/v1/ticket/refresh", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("expected refresh rejection on empty location intersection")
	}
}

// TestLocationScopedRefreshRemainsLocationScoped: empty NodeScope stays empty.
func TestLocationScopedRefreshRemainsLocationScoped(t *testing.T) {
	env := setupProductionEnv(t)
	devPub, _, _ := ed25519.GenerateKey(nil)
	activateDevice(t, env.ts, "nyx_lic_test1:test-secret", "dev_loc_scope", devPub)
	oldTok := issueTicketAt(t, env.ts, "nyx_lic_test1:test-secret", "dev_loc_scope", "", "fi-hel")

	claims := refreshTicket(t, env, "nyx_lic_test1:test-secret", "dev_loc_scope", oldTok)
	if len(claims.NodeScope) != 0 {
		t.Fatalf("location-scoped refresh must keep empty NodeScope, got %v", claims.NodeScope)
	}
	if !reflect.DeepEqual(claims.Locations, []string{"fi-hel"}) {
		t.Fatalf("expected Locations [fi-hel], got %v", claims.Locations)
	}
}

func TestRefreshUsesCurrentLicenseRole(t *testing.T) {
	env := setupProductionEnv(t)
	if err := env.prod.RegisterLicense(context.Background(), model.LicenseRecord{
		LicenseID: "nyx_lic_master_r", Plan: "master", MaxDevices: 5, Enabled: true,
		Secret: "master-secret", ExpiresAt: time.Now().Add(24 * time.Hour),
		Locations: []string{"fi-hel", "de-fra"},
	}); err != nil {
		t.Fatal(err)
	}
	devPub, _, _ := ed25519.GenerateKey(nil)
	activateDevice(t, env.ts, "nyx_lic_master_r:master-secret", "dev_role", devPub)
	oldTok := issueTicketAt(t, env.ts, "nyx_lic_master_r:master-secret", "dev_role", "", "fi-hel")

	lic, err := env.st.GetLicense(context.Background(), "nyx_lic_master_r")
	if err != nil {
		t.Fatal(err)
	}
	lic.Plan = "premium"
	if err := env.st.UpsertLicense(context.Background(), *lic); err != nil {
		t.Fatal(err)
	}

	claims := refreshTicket(t, env, "nyx_lic_master_r:master-secret", "dev_role", oldTok)
	if claims.Role != "user" {
		t.Fatalf("expected current plan role=user, got %q", claims.Role)
	}
	if claims.Plan != "premium" {
		t.Fatalf("expected plan=premium, got %q", claims.Plan)
	}
}

func TestRefreshUsesCurrentAllowedLocations(t *testing.T) {
	env := setupProductionEnv(t)
	if err := env.prod.RegisterLicense(context.Background(), model.LicenseRecord{
		LicenseID: "nyx_lic_locs", Plan: "premium", MaxDevices: 3, Enabled: true,
		Secret: "loc-secret", ExpiresAt: time.Now().Add(24 * time.Hour),
		Locations: []string{"fi-hel", "de-fra"},
	}); err != nil {
		t.Fatal(err)
	}
	devPub, _, _ := ed25519.GenerateKey(nil)
	activateDevice(t, env.ts, "nyx_lic_locs:loc-secret", "dev_locs", devPub)
	// Issue without LocationID → Locations = full license allowlist.
	oldTok := issueTicketAt(t, env.ts, "nyx_lic_locs:loc-secret", "dev_locs", "", "")

	lic, err := env.st.GetLicense(context.Background(), "nyx_lic_locs")
	if err != nil {
		t.Fatal(err)
	}
	lic.Locations = []string{"fi-hel"}
	if err := env.st.UpsertLicense(context.Background(), *lic); err != nil {
		t.Fatal(err)
	}

	claims := refreshTicket(t, env, "nyx_lic_locs:loc-secret", "dev_locs", oldTok)
	if !reflect.DeepEqual(claims.Locations, []string{"fi-hel"}) {
		t.Fatalf("expected intersected/current locations [fi-hel], got %v", claims.Locations)
	}
}

func TestRefreshUsesCurrentPermissions(t *testing.T) {
	env := setupProductionEnv(t)
	devPub, _, _ := ed25519.GenerateKey(nil)
	activateDevice(t, env.ts, "nyx_lic_test1:test-secret", "dev_perms", devPub)
	oldTok := issueTicket(t, env.ts, "nyx_lic_test1:test-secret", "dev_perms", "fi-hel-01")

	claims := refreshTicket(t, env, "nyx_lic_test1:test-secret", "dev_perms", oldTok)
	if !reflect.DeepEqual(claims.Permissions, []string{ticket.PermissionConnect}) {
		t.Fatalf("expected current plan permissions [connect], got %v", claims.Permissions)
	}
	if !claims.HasPermission(ticket.PermissionConnect) {
		t.Fatal("HasPermission(connect) should be true")
	}
}

func TestRefreshAfterLicenseDowngradeDoesNotKeepOldRights(t *testing.T) {
	env := setupProductionEnv(t)
	if err := env.prod.RegisterLicense(context.Background(), model.LicenseRecord{
		LicenseID: "nyx_lic_down", Plan: "master", MaxDevices: 5, Enabled: true,
		Secret: "down-secret", ExpiresAt: time.Now().Add(24 * time.Hour),
		Locations: []string{"fi-hel", "de-fra"},
	}); err != nil {
		t.Fatal(err)
	}
	devPub, _, _ := ed25519.GenerateKey(nil)
	activateDevice(t, env.ts, "nyx_lic_down:down-secret", "dev_down", devPub)
	oldTok := issueTicketAt(t, env.ts, "nyx_lic_down:down-secret", "dev_down", "", "")

	oldClaims, err := ticket.VerifyIdentity(ticket.VerifierConfig{
		Issuer: env.issuer.Issuer, Audience: env.issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": env.pub},
	}, oldTok, "dev_down")
	if err != nil {
		t.Fatal(err)
	}
	if oldClaims.Role != "master" || !reflect.DeepEqual(oldClaims.Locations, []string{"fi-hel", "de-fra"}) {
		t.Fatalf("precondition failed: %+v", oldClaims)
	}

	lic, err := env.st.GetLicense(context.Background(), "nyx_lic_down")
	if err != nil {
		t.Fatal(err)
	}
	lic.Plan = "basic"
	lic.Locations = []string{"fi-hel"}
	if err := env.st.UpsertLicense(context.Background(), *lic); err != nil {
		t.Fatal(err)
	}

	claims := refreshTicket(t, env, "nyx_lic_down:down-secret", "dev_down", oldTok)
	if claims.Role != "user" {
		t.Fatalf("downgrade must drop master role, got %q", claims.Role)
	}
	if !reflect.DeepEqual(claims.Locations, []string{"fi-hel"}) {
		t.Fatalf("downgrade must drop de-fra, got %v", claims.Locations)
	}
	if len(claims.NodeScope) != 0 {
		t.Fatalf("unexpected NodeScope %v", claims.NodeScope)
	}
}

func TestRefreshDisabledLicenseRejected(t *testing.T) {
	env := setupProductionEnv(t)
	devPub, _, _ := ed25519.GenerateKey(nil)
	activateDevice(t, env.ts, "nyx_lic_test1:test-secret", "dev_dis", devPub)
	oldTok := issueTicket(t, env.ts, "nyx_lic_test1:test-secret", "dev_dis", "fi-hel-01")

	lic, err := env.st.GetLicense(context.Background(), "nyx_lic_test1")
	if err != nil {
		t.Fatal(err)
	}
	lic.Enabled = false
	if err := env.st.UpsertLicense(context.Background(), *lic); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(api.TicketRefreshRequest{
		LicenseToken: "nyx_lic_test1:test-secret",
		DeviceID:     "dev_dis",
		AccessTicket: oldTok,
	})
	resp, err := http.Post(env.ts.URL+"/api/v1/ticket/refresh", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestRefreshRevokedDeviceRejected(t *testing.T) {
	env := setupProductionEnv(t)
	devPub, _, _ := ed25519.GenerateKey(nil)
	activateDevice(t, env.ts, "nyx_lic_test1:test-secret", "dev_revdev", devPub)
	oldTok := issueTicket(t, env.ts, "nyx_lic_test1:test-secret", "dev_revdev", "fi-hel-01")

	if err := env.st.RevokeDevice(context.Background(), "dev_revdev"); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(api.TicketRefreshRequest{
		LicenseToken: "nyx_lic_test1:test-secret",
		DeviceID:     "dev_revdev",
		AccessTicket: oldTok,
	})
	resp, err := http.Post(env.ts.URL+"/api/v1/ticket/refresh", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestRefreshCannotEscalateScope(t *testing.T) {
	env := setupProductionEnv(t)
	devPub, _, _ := ed25519.GenerateKey(nil)
	activateDevice(t, env.ts, "nyx_lic_test1:test-secret", "dev_esc", devPub)
	oldTok := issueTicketAt(t, env.ts, "nyx_lic_test1:test-secret", "dev_esc", "", "fi-hel")

	raw := map[string]interface{}{
		"license_token": "nyx_lic_test1:test-secret",
		"device_id":     "dev_esc",
		"access_ticket": oldTok,
		"role":          "master",
		"permissions":   []string{"connect", "admin"},
		"node_id":       "de-fra-01",
		"locations":     []string{"fi-hel", "de-fra"},
	}
	body, _ := json.Marshal(raw)
	resp, err := http.Post(env.ts.URL+"/api/v1/ticket/refresh", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out api.TicketIssueResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	claims, err := ticket.VerifyIdentity(ticket.VerifierConfig{
		Issuer: env.issuer.Issuer, Audience: env.issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": env.pub},
	}, out.AccessTicket, "dev_esc")
	if err != nil {
		t.Fatal(err)
	}
	if claims.Role != "user" {
		t.Fatalf("role escalated to %q", claims.Role)
	}
	if !reflect.DeepEqual(claims.Permissions, []string{ticket.PermissionConnect}) {
		t.Fatalf("permissions escalated: %v", claims.Permissions)
	}
	if len(claims.NodeScope) != 0 {
		t.Fatalf("node scope must stay empty, got %v", claims.NodeScope)
	}
	if !reflect.DeepEqual(claims.Locations, []string{"fi-hel"}) {
		t.Fatalf("locations expanded: %v", claims.Locations)
	}
}

func TestTicketRefreshWrongDeviceRejected(t *testing.T) {
	env := setupProductionEnv(t)
	devPub, _, _ := ed25519.GenerateKey(nil)
	activateDevice(t, env.ts, "nyx_lic_test1:test-secret", "dev_a", devPub)
	activateDevice(t, env.ts, "nyx_lic_test1:test-secret", "dev_b", devPub)
	oldTok := issueTicket(t, env.ts, "nyx_lic_test1:test-secret", "dev_a", "fi-hel-01")

	body, _ := json.Marshal(api.TicketRefreshRequest{
		LicenseToken: "nyx_lic_test1:test-secret",
		DeviceID:     "dev_b",
		AccessTicket: oldTok,
	})
	resp, err := http.Post(env.ts.URL+"/api/v1/ticket/refresh", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestTicketRefreshRevokedLicenseRejected(t *testing.T) {
	env := setupProductionEnv(t)
	devPub, _, _ := ed25519.GenerateKey(nil)
	activateDevice(t, env.ts, "nyx_lic_test1:test-secret", "dev_revlic", devPub)
	oldTok := issueTicket(t, env.ts, "nyx_lic_test1:test-secret", "dev_revlic", "fi-hel-01")

	lic, err := env.st.GetLicense(context.Background(), "nyx_lic_test1")
	if err != nil {
		t.Fatal(err)
	}
	lic.Revoked = true
	if err := env.st.UpsertLicense(context.Background(), *lic); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(api.TicketRefreshRequest{
		LicenseToken: "nyx_lic_test1:test-secret",
		DeviceID:     "dev_revlic",
		AccessTicket: oldTok,
	})
	resp, err := http.Post(env.ts.URL+"/api/v1/ticket/refresh", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestCatalogRequiresAuthentication(t *testing.T) {
	env := setupProductionEnv(t)
	resp, err := http.Get(env.ts.URL + "/api/v1/catalog")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestInvalidLicenseCannotFetchCatalog(t *testing.T) {
	env := setupProductionEnv(t)
	req, _ := http.NewRequest(http.MethodGet, env.ts.URL+"/api/v1/catalog", nil)
	req.Header.Set("Authorization", "Bearer nyx_lic_test1:wrong-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func fetchCatalog(t *testing.T, ts *httptest.Server, bearer string) model.SignedCatalog {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/catalog", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("catalog status=%d body=%s", resp.StatusCode, b)
	}
	var signed model.SignedCatalog
	_ = json.NewDecoder(resp.Body).Decode(&signed)
	return signed
}

func TestUserDoesNotReceiveTestNodes(t *testing.T) {
	env := setupProductionEnv(t)
	signed := fetchCatalog(t, env.ts, "nyx_lic_test1:test-secret")
	for _, n := range signed.Catalog.Nodes {
		if n.TestOnly {
			t.Fatal("user must not receive test_only nodes")
		}
	}
}

func TestMasterCanReceiveTestNodes(t *testing.T) {
	env := setupProductionEnv(t)
	if err := env.prod.RegisterLicense(context.Background(), model.LicenseRecord{
		LicenseID: "nyx_lic_master", Plan: "master", MaxDevices: 5, Enabled: true,
		Secret: "master-secret", ExpiresAt: time.Now().Add(24 * time.Hour),
		Locations: []string{"fi-hel", "de-fra"},
	}); err != nil {
		t.Fatal(err)
	}
	signed := fetchCatalog(t, env.ts, "nyx_lic_master:master-secret")
	found := false
	for _, n := range signed.Catalog.Nodes {
		if n.NodeID == "test-01" && n.TestOnly {
			found = true
		}
	}
	if !found {
		t.Fatal("master must receive test_only nodes")
	}
}

func TestCatalogRespectsAllowedLocations(t *testing.T) {
	env := setupProductionEnv(t)
	signed := fetchCatalog(t, env.ts, "nyx_lic_test1:test-secret")
	for _, n := range signed.Catalog.Nodes {
		if n.LocationID != "fi-hel" {
			t.Fatalf("unexpected location %s", n.LocationID)
		}
	}
	for _, loc := range signed.Catalog.Locations {
		if loc.LocationID != "fi-hel" {
			t.Fatalf("unexpected catalog location %s", loc.LocationID)
		}
	}
}

func TestRevocationEndpointRejectsAnonymous(t *testing.T) {
	env := setupProductionEnv(t)
	resp, err := http.Get(env.ts.URL + "/api/v1/revocation")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestRevocationEndpointRejectsUserCredential(t *testing.T) {
	env := setupProductionEnv(t)
	req, _ := http.NewRequest(http.MethodGet, env.ts.URL+"/api/v1/revocation", nil)
	req.Header.Set("Authorization", "Bearer nyx_lic_test1:test-secret")
	req.Header.Set("X-Node-ID", "fi-hel-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 401/403, got %d", resp.StatusCode)
	}
}

func TestRevocationEndpointAcceptsAuthenticatedNode(t *testing.T) {
	env := setupProductionEnv(t)
	nodePub, nodePriv, _ := ed25519.GenerateKey(nil)
	if err := env.prod.RegisterNodeIdentity(context.Background(), "fi-hel-01", nodePub); err != nil {
		t.Fatal(err)
	}
	token := nodeauth.SignToken("fi-hel-01", nodePriv, time.Now())
	req, _ := http.NewRequest(http.MethodGet, env.ts.URL+"/api/v1/revocation", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Node-ID", "fi-hel-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestRevocationEndpointRequiresNodeAuthentication(t *testing.T) {
	// Alias of anonymous rejection + node acceptance coverage.
	TestRevocationEndpointRejectsAnonymous(t)
}
