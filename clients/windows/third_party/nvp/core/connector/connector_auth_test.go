package connector_test

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/authhandler"
	"github.com/nyxveil/nvp/core/connector"
	"github.com/nyxveil/nvp/core/controlplane/api"
	"github.com/nyxveil/nvp/core/controlplane/catalog"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/failover"
	"github.com/nyxveil/nvp/core/internal/testutil"
	"github.com/nyxveil/nvp/core/nvperr"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
	tlsstream "github.com/nyxveil/nvp/core/transport/tlsstream"
)

func openSessionHarness(t *testing.T, onAuth func(ctx context.Context, sess *session.Session, payload []byte) error, ticketJWT string) (*connector.Connector, connector.ConnectConfig, func()) {
	t.Helper()
	bundle, err := testutil.GenerateCertBundle("localhost")
	if err != nil {
		t.Fatal(err)
	}
	addr, closeNode := startTLSNodeWithAuth(t, bundle, onAuth)
	pub, priv, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "cat-key-1", PrivateKey: priv}
	pin := spkiPin(bundle.Cert)
	signed, err := signer.Sign(model.Catalog{
		Version: "1",
		Nodes: []model.NodeRegistryEntry{{
			NodeID: "n1", LocationID: "fi-hel", Enabled: true, Capacity: 10,
			ServerName: bundle.ServerName, SPKIPin: pin,
			Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: mustPort(addr), Profiles: []transport.Profile{transport.ProfileTLSTCP}}},
		}},
	})
	if err != nil {
		closeNode()
		t.Fatal(err)
	}
	accessTicket := ticketJWT
	if accessTicket == "" {
		accessTicket = "tok"
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/license/validate":
			_ = json.NewEncoder(w).Encode(api.LicenseValidateResponse{Valid: true})
		case "/api/v1/catalog":
			_ = json.NewEncoder(w).Encode(signed)
		case "/api/v1/ticket/issue":
			_ = json.NewEncoder(w).Encode(api.TicketIssueResponse{AccessTicket: accessTicket})
		default:
			http.NotFound(w, r)
		}
	}))
	reg := transport.NewRegistry()
	reg.Register(tlsstream.NewTransport())
	c := &connector.Connector{
		CP:                connector.NewControlPlaneClient(ts.URL),
		Registry:          reg,
		RequirePin:        true,
		CatalogVerifyKeys: catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"cat-key-1": pub}},
		Provider:          &staticProvider{pool: bundle.CAPool, sn: bundle.ServerName},
		Policy: failover.ConnectPolicy{
			MaxNodeAttempts: 1,
			TransportRacing: transport.RacingConfig{Primary: transport.ProfileTLSTCP, Fallback: transport.ProfileTLSTCP},
			RetryDelay:      time.Millisecond,
		},
	}
	cfg := connector.ConnectConfig{
		LicenseToken:     "lic",
		DeviceID:         "dev",
		LocationID:       "fi-hel",
		DevicePrivateKey: mustDeviceKey(t),
		AuthTimeout:      3 * time.Second,
	}
	cleanup := func() {
		ts.Close()
		closeNode()
	}
	return c, cfg, cleanup
}

func TestOpenSessionWaitsForAuthOK(t *testing.T) {
	c, cfg, cleanup := openSessionHarness(t, nil, "")
	defer cleanup()
	sess, conn, _, err := c.OpenSession(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if sess.State() != session.StateEstablished {
		t.Fatalf("state=%s", sess.State())
	}
}

func TestOpenSessionReturnsEstablishedState(t *testing.T) {
	c, cfg, cleanup := openSessionHarness(t, nil, "")
	defer cleanup()
	sess, conn, _, err := c.OpenSession(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if sess.State() != session.StateEstablished {
		t.Fatalf("want ESTABLISHED got %s", sess.State())
	}
	st := sess.Stats()
	if st.State != session.StateEstablished {
		t.Fatalf("stats state=%s", st.State)
	}
}

func TestOpenSessionAuthFailReturnsError(t *testing.T) {
	c, cfg, cleanup := openSessionHarness(t, func(ctx context.Context, sess *session.Session, _ []byte) error {
		return sess.HandleAuthFail(ctx, 0)
	}, "")
	defer cleanup()
	_, _, _, err := c.OpenSession(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected auth fail error")
	}
	if !errors.Is(err, nvperr.ErrAuthFailed) {
		t.Fatalf("got %v want ErrAuthFailed", err)
	}
}

func TestOpenSessionAuthTimeoutReturnsError(t *testing.T) {
	c, cfg, cleanup := openSessionHarness(t, func(context.Context, *session.Session, []byte) error {
		// Ignore AUTH — never send AUTH_OK.
		return nil
	}, "")
	defer cleanup()
	cfg.AuthTimeout = 200 * time.Millisecond
	start := time.Now()
	_, _, _, err := c.OpenSession(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected auth timeout")
	}
	if !errors.Is(err, nvperr.ErrAuthTimeout) {
		t.Fatalf("got %v want ErrAuthTimeout", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("timeout took too long: %s", time.Since(start))
	}
}

func TestDeviceBoundTicketWithoutPrivateKeyRejected(t *testing.T) {
	c, cfg, cleanup := openSessionHarness(t, nil, "")
	defer cleanup()
	cfg.DevicePrivateKey = nil
	_, _, _, err := c.OpenSession(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected ErrDeviceKeyRequired")
	}
	if !errors.Is(err, nvperr.ErrDeviceKeyRequired) {
		t.Fatalf("got %v", err)
	}
}

func TestCorrectDevicePrivateKeyAccepted(t *testing.T) {
	devPub, devPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	issPub, issPriv, _ := ed25519.GenerateKey(nil)
	issuer := ticket.IssuerConfig{
		Issuer: "https://control.nyxveil.test", Audience: "nvp-node",
		KeyID: "cp-key-1", PrivateKey: issPriv, TTL: 15 * time.Minute,
	}
	tok, err := ticket.IssueWithDevice(issuer, "lic", "dev", "user", "premium", []string{"connect"}, []string{"fi-hel"}, devPub)
	if err != nil {
		t.Fatal(err)
	}
	verifier := ticket.VerifierConfig{
		Issuer: issuer.Issuer, Audience: issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": issPub},
		Revoked:    ticket.NewMemoryRevocation(),
	}
	handler := authhandler.NewAuthHandler("n1", "fi-hel", verifier)
	c, cfg, cleanup := openSessionHarness(t, func(ctx context.Context, sess *session.Session, payload []byte) error {
		return handler.HandleAuth(ctx, sess, payload)
	}, tok)
	defer cleanup()
	cfg.DevicePrivateKey = devPriv
	sess, conn, _, err := c.OpenSession(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if sess.State() != session.StateEstablished {
		t.Fatalf("state=%s", sess.State())
	}
}

func TestWrongDevicePrivateKeyRejected(t *testing.T) {
	devPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, wrongPriv, _ := ed25519.GenerateKey(nil)
	issPub, issPriv, _ := ed25519.GenerateKey(nil)
	issuer := ticket.IssuerConfig{
		Issuer: "https://control.nyxveil.test", Audience: "nvp-node",
		KeyID: "cp-key-1", PrivateKey: issPriv, TTL: 15 * time.Minute,
	}
	tok, err := ticket.IssueWithDevice(issuer, "lic", "dev", "user", "premium", []string{"connect"}, []string{"fi-hel"}, devPub)
	if err != nil {
		t.Fatal(err)
	}
	verifier := ticket.VerifierConfig{
		Issuer: issuer.Issuer, Audience: issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": issPub},
		Revoked:    ticket.NewMemoryRevocation(),
	}
	handler := authhandler.NewAuthHandler("n1", "fi-hel", verifier)
	c, cfg, cleanup := openSessionHarness(t, func(ctx context.Context, sess *session.Session, payload []byte) error {
		return handler.HandleAuth(ctx, sess, payload)
	}, tok)
	defer cleanup()
	cfg.DevicePrivateKey = wrongPriv
	_, _, _, err = c.OpenSession(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected rejection of wrong device key")
	}
	if !errors.Is(err, nvperr.ErrAuthFailed) {
		t.Fatalf("got %v want ErrAuthFailed", err)
	}
}

func TestConnectorUsesLicenseCredentialForCatalog(t *testing.T) {
	var gotAuth string
	pub, priv, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "cat-key-1", PrivateKey: priv}
	signed, err := signer.Sign(model.Catalog{
		Version: "1",
		Nodes: []model.NodeRegistryEntry{{
			NodeID: "n1", LocationID: "fi-hel", Enabled: true, Capacity: 10,
			SPKIPin: []byte{1}, Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: 1}},
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
			gotAuth = r.Header.Get("Authorization")
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
	}
	_, _, err = c.PrepareSelection(context.Background(), connector.ConnectConfig{
		LicenseToken: "lic-secret",
		DeviceID:     "dev",
		LocationID:   "fi-hel",
		// CatalogBearer intentionally empty — LicenseToken must be used.
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer lic-secret" {
		t.Fatalf("catalog auth=%q want Bearer lic-secret", gotAuth)
	}
}

func TestConnectorInvalidLicenseCannotFetchCatalog(t *testing.T) {
	var catalogHits int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/license/validate":
			_ = json.NewEncoder(w).Encode(api.LicenseValidateResponse{Valid: false})
		case "/api/v1/catalog":
			catalogHits++
			_ = json.NewEncoder(w).Encode(model.SignedCatalog{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	c := &connector.Connector{
		CP:                connector.NewControlPlaneClient(ts.URL),
		CatalogVerifyKeys: catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{}},
	}
	_, _, err := c.PrepareSelection(context.Background(), connector.ConnectConfig{
		LicenseToken: "bad",
		DeviceID:     "dev",
	})
	if err == nil {
		t.Fatal("expected invalid license error")
	}
	if !errors.Is(err, nvperr.ErrLicenseInvalid) {
		t.Fatalf("got %v", err)
	}
	if catalogHits != 0 {
		t.Fatalf("catalog must not be fetched with invalid license, hits=%d", catalogHits)
	}
}

func TestConnectorMissingCredentialFails(t *testing.T) {
	c := &connector.Connector{
		CP: connector.NewControlPlaneClient("http://127.0.0.1:1"),
	}
	_, _, err := c.PrepareSelection(context.Background(), connector.ConnectConfig{})
	if err == nil {
		t.Fatal("expected missing credential failure")
	}
	if !errors.Is(err, nvperr.ErrLicenseInvalid) {
		t.Fatalf("got %v", err)
	}
}
