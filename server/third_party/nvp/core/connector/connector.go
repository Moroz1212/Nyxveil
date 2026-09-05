package connector

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/controlplane/api"
	"github.com/nyxveil/nvp/core/controlplane/catalog"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/failover"
	"github.com/nyxveil/nvp/core/nvperr"
	"github.com/nyxveil/nvp/core/protocol"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
)

// DefaultAuthTimeout is how long OpenSession waits for AUTH_OK after SendAuth.
// Alias of protocol.DefaultAuthTimeout for connector callers.
const DefaultAuthTimeout = protocol.DefaultAuthTimeout

// ControlPlaneClient implements Control Plane HTTP API calls.
type ControlPlaneClient struct {
	BaseURL string
	HTTP    *http.Client
}

// NewControlPlaneClient creates API client.
func NewControlPlaneClient(baseURL string) *ControlPlaneClient {
	return &ControlPlaneClient{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *ControlPlaneClient) post(ctx context.Context, path string, req, resp any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpResp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode >= 400 {
		return fmt.Errorf("control plane %s: status %d", path, httpResp.StatusCode)
	}
	return json.NewDecoder(httpResp.Body).Decode(resp)
}

// ValidateLicense checks license validity.
func (c *ControlPlaneClient) ValidateLicense(ctx context.Context, token string) (*api.LicenseValidateResponse, error) {
	var resp api.LicenseValidateResponse
	err := c.post(ctx, "/api/v1/license/validate", api.LicenseValidateRequest{LicenseToken: token}, &resp)
	return &resp, err
}

// ActivateDevice registers device with Control Plane.
func (c *ControlPlaneClient) ActivateDevice(ctx context.Context, token, deviceID string, pubKey []byte) error {
	var resp api.DeviceActivateResponse
	err := c.post(ctx, "/api/v1/device/activate", api.DeviceActivateRequest{
		LicenseToken: token,
		DeviceID:     deviceID,
		PublicKey:    pubKey,
	}, &resp)
	if err != nil {
		return err
	}
	if !resp.Activated {
		return fmt.Errorf("%w: device activation rejected", nvperr.ErrDeviceUnauthorized)
	}
	return nil
}

// IssueTicket requests an access ticket.
// For multi-node failover pass nodeID="" and locationID set so the CP issues a
// location-scoped ticket (Locations from license; empty NodeScope = any node in Locations).
// When nodeID is set, CP pins NodeScope=[nodeID].
func (c *ControlPlaneClient) IssueTicket(ctx context.Context, token, deviceID, nodeID, locationID string) (*api.TicketIssueResponse, error) {
	var resp api.TicketIssueResponse
	err := c.post(ctx, "/api/v1/ticket/issue", api.TicketIssueRequest{
		LicenseToken: token,
		DeviceID:     deviceID,
		NodeID:       nodeID,
		LocationID:   locationID,
	}, &resp)
	return &resp, err
}

// FetchCatalog downloads signed node catalog.
// bearer is a Client↔CP credential (typically LicenseToken); not an AccessTicket.
func (c *ControlPlaneClient) FetchCatalog(ctx context.Context, bearer string) (*model.SignedCatalog, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/v1/catalog", nil)
	if err != nil {
		return nil, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("control plane catalog: status %d", resp.StatusCode)
	}
	var signed model.SignedCatalog
	if err := json.NewDecoder(resp.Body).Decode(&signed); err != nil {
		return nil, err
	}
	return &signed, nil
}

// ConnectConfig holds end-to-end client connection parameters.
//
// Credential split:
//   - LicenseToken: Client↔Control Plane auth for license, device, ticket, and catalog
//     (used automatically for FetchCatalog when CatalogBearer is empty).
//   - AccessTicket (from CP IssueTicket): Client↔Node session AUTH; requires DevicePrivateKey
//     when the ticket is device-bound.
//   - CatalogBearer: optional override for catalog Authorization only; prefer leaving empty
//     so LicenseToken is used.
type ConnectConfig struct {
	LicenseToken     string
	DeviceID         string
	DevicePublicKey  []byte
	DevicePrivateKey ed25519.PrivateKey
	LocationID       string
	Role             string
	CatalogBearer    string        // optional catalog auth override; empty → LicenseToken
	AuthTimeout      time.Duration // 0 = DefaultAuthTimeout; OpenSession wait for AUTH_OK
}

// catalogAuthCredential returns the Bearer used for CP catalog fetches.
func catalogAuthCredential(cfg ConnectConfig) string {
	if cfg.CatalogBearer != "" {
		return cfg.CatalogBearer
	}
	return cfg.LicenseToken
}

func authTimeout(cfg ConnectConfig) time.Duration {
	if cfg.AuthTimeout > 0 {
		return cfg.AuthTimeout
	}
	return DefaultAuthTimeout
}

// Connector orchestrates CP ticket fetch and transport dial with node failover.
//
// Ticket strategy (location-scoped): IssueTicket with LocationID set and NodeID
// empty. Control Plane sets JWT Locations=[location] (must be allowed) and leaves
// NodeScope empty, meaning any node in Locations is allowed. Same-location
// multi-node failover reuses one ticket. Cross-location is not automatic — the
// application must call OpenSession with a new DesiredLocationID / LocationID.
type Connector struct {
	CP                *ControlPlaneClient
	Registry          *transport.Registry
	Policy            failover.ConnectPolicy
	Provider          failover.DialConfigProvider
	CatalogVerifyKeys catalog.VerifyKeys
	RequirePin        bool // production: fail if selected node has no SPKI pin
	ECHPolicy         transport.ECHPolicy
	ECHConfigList     []byte
}

// PrepareSelection validates license, verifies catalog (fail-closed), lists
// candidates for the location, and issues a location-scoped ticket — without dialing.
// The returned node is the preferred (first) candidate for UI/diagnostics; OpenSession
// failovers across all candidates using the same ticket.
func (c *Connector) PrepareSelection(ctx context.Context, cfg ConnectConfig) (string, model.NodeRegistryEntry, error) {
	ticketStr, _, preferred, err := c.prepare(ctx, cfg)
	return ticketStr, preferred, err
}

func (c *Connector) prepare(ctx context.Context, cfg ConnectConfig) (string, []model.NodeRegistryEntry, model.NodeRegistryEntry, error) {
	if cfg.LicenseToken == "" && cfg.CatalogBearer == "" {
		return "", nil, model.NodeRegistryEntry{}, nvperr.ErrLicenseInvalid
	}
	if cfg.LicenseToken == "" {
		return "", nil, model.NodeRegistryEntry{}, nvperr.ErrLicenseInvalid
	}
	valid, err := c.CP.ValidateLicense(ctx, cfg.LicenseToken)
	if err != nil {
		return "", nil, model.NodeRegistryEntry{}, fmt.Errorf("%w: %v", nvperr.ErrLicenseInvalid, err)
	}
	if valid == nil || !valid.Valid {
		return "", nil, model.NodeRegistryEntry{}, nvperr.ErrLicenseInvalid
	}
	if len(cfg.DevicePublicKey) > 0 {
		if err := c.CP.ActivateDevice(ctx, cfg.LicenseToken, cfg.DeviceID, cfg.DevicePublicKey); err != nil {
			return "", nil, model.NodeRegistryEntry{}, err
		}
	}
	signed, err := c.CP.FetchCatalog(ctx, catalogAuthCredential(cfg))
	if err != nil {
		return "", nil, model.NodeRegistryEntry{}, err
	}
	if err := catalog.Verify(c.CatalogVerifyKeys, *signed); err != nil {
		return "", nil, model.NodeRegistryEntry{}, fmt.Errorf("catalog verify failed: %w", err)
	}
	sel := &failover.Selector{Catalog: signed.Catalog, Role: cfg.Role, LocationID: cfg.LocationID}
	candidates := sel.CandidateNodes()
	if len(candidates) == 0 {
		return "", nil, model.NodeRegistryEntry{}, nvperr.ErrNoHealthyNodes
	}
	// Location-scoped ticket: empty NodeID so CP does not pin NodeScope to one node.
	// Same-location multi-node failover reuses one ticket. Cross-location requires a
	// new OpenSession(DesiredLocationID=...) from the application — Core never
	// automatically switches locations.
	ticketResp, err := c.CP.IssueTicket(ctx, cfg.LicenseToken, cfg.DeviceID, "", cfg.LocationID)
	if err != nil {
		return "", nil, model.NodeRegistryEntry{}, fmt.Errorf("%w: %v", nvperr.ErrTicketRejected, err)
	}
	return ticketResp.AccessTicket, candidates, candidates[0], nil
}

// PrepareTicket is an alias for PrepareSelection.
func (c *Connector) PrepareTicket(ctx context.Context, cfg ConnectConfig) (string, model.NodeRegistryEntry, error) {
	return c.PrepareSelection(ctx, cfg)
}

// OpenSession verifies catalog once, issues a location-scoped ticket, then dials
// candidates with transport+node failover (QUIC→TLS per node, then next node).
// Success is returned only when State() == ESTABLISHED after AUTH_OK.
func (c *Connector) OpenSession(ctx context.Context, cfg ConnectConfig) (*session.Session, transport.Conn, model.NodeRegistryEntry, error) {
	ticketStr, candidates, preferred, err := c.prepare(ctx, cfg)
	if err != nil {
		return nil, nil, model.NodeRegistryEntry{}, err
	}
	if err := c.checkPins(candidates); err != nil {
		return nil, nil, model.NodeRegistryEntry{}, err
	}
	_ = preferred
	sel := &failover.Selector{
		Catalog:    model.Catalog{Nodes: candidates},
		Role:       cfg.Role,
		LocationID: cfg.LocationID,
	}
	policy := c.Policy
	if claims, peekErr := ticket.PeekClaims(ticketStr); peekErr == nil {
		// Node-scoped tickets: restrict dials to NodeScope (intersection with location candidates).
		if len(claims.NodeScope) > 0 {
			policy.AllowedNodeIDs = append([]string(nil), claims.NodeScope...)
		}
	}
	conn, node, err := failover.ConnectWithFailover(ctx, sel, c.Registry, policy, c.dialProvider())
	if err != nil {
		var ex *failover.ExhaustedError
		if errors.As(err, &ex) {
			return nil, nil, model.NodeRegistryEntry{}, err
		}
		return nil, nil, model.NodeRegistryEntry{}, fmt.Errorf("%w: %v", nvperr.ErrTransportUnavailable, err)
	}
	sess := session.New(session.DefaultConfig(true))
	if err := sess.Connect(ctx, conn); err != nil {
		conn.Close()
		return nil, nil, model.NodeRegistryEntry{}, fmt.Errorf("%w: %v", nvperr.ErrHandshakeFailed, err)
	}
	if err := sess.RunHandshake(ctx); err != nil {
		conn.Close()
		return nil, nil, model.NodeRegistryEntry{}, fmt.Errorf("%w: %v", nvperr.ErrHandshakeFailed, err)
	}

	// Access tickets are Client↔Node credentials and require device binding for AUTH.
	if ticketStr == "" {
		conn.Close()
		return nil, nil, model.NodeRegistryEntry{}, nvperr.ErrTicketRejected
	}
	if len(cfg.DevicePrivateKey) != ed25519.PrivateKeySize {
		conn.Close()
		return nil, nil, model.NodeRegistryEntry{}, nvperr.ErrDeviceKeyRequired
	}

	timeout := authTimeout(cfg)
	authCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if dl, ok := authCtx.Deadline(); ok {
		_ = conn.SetReadDeadline(dl)
		_ = conn.SetWriteDeadline(dl)
		defer func() {
			_ = conn.SetReadDeadline(time.Time{})
			_ = conn.SetWriteDeadline(time.Time{})
		}()
	}

	authBody, err := ticket.EncodeAuthPayload(ticketStr, sess.Transcript(), cfg.DevicePrivateKey)
	if err != nil {
		conn.Close()
		return nil, nil, model.NodeRegistryEntry{}, fmt.Errorf("%w: %v", nvperr.ErrDeviceKeyRequired, err)
	}
	if err := sess.SendAuth(authCtx, authBody); err != nil {
		conn.Close()
		return nil, nil, model.NodeRegistryEntry{}, fmt.Errorf("%w: %v", nvperr.ErrAuthFailed, err)
	}

	if err := sess.WaitEstablished(authCtx); err != nil {
		conn.Close()
		if errors.Is(err, nvperr.ErrAuthTimeout) || errors.Is(err, context.DeadlineExceeded) {
			return nil, nil, model.NodeRegistryEntry{}, nvperr.ErrAuthTimeout
		}
		if errors.Is(err, nvperr.ErrAuthFailed) || errors.Is(err, nvperr.ErrTicketRejected) {
			return nil, nil, model.NodeRegistryEntry{}, err
		}
		if sess.State() == session.StateClosed {
			return nil, nil, model.NodeRegistryEntry{}, nvperr.ErrSessionClosed
		}
		return nil, nil, model.NodeRegistryEntry{}, err
	}
	if sess.State() != session.StateEstablished {
		conn.Close()
		return nil, nil, model.NodeRegistryEntry{}, nvperr.ErrAuthFailed
	}
	return sess, conn, node, nil
}

func (c *Connector) dialProvider() failover.DialConfigProvider {
	return &mergedDialProvider{
		inner:         c.Provider,
		echPolicy:     c.ECHPolicy,
		echConfigList: c.ECHConfigList,
	}
}

type mergedDialProvider struct {
	inner         failover.DialConfigProvider
	echPolicy     transport.ECHPolicy
	echConfigList []byte
}

func (p *mergedDialProvider) RootCAs() interface{} {
	if p.inner != nil {
		return p.inner.RootCAs()
	}
	return nil
}

func (p *mergedDialProvider) ServerNameFor(node model.NodeRegistryEntry) string {
	if p.inner != nil {
		return p.inner.ServerNameFor(node)
	}
	return ""
}

func (p *mergedDialProvider) PinnedPubKeyFor(node model.NodeRegistryEntry) []byte {
	if p.inner != nil {
		return p.inner.PinnedPubKeyFor(node)
	}
	return nil
}

func (p *mergedDialProvider) ECHPolicy() transport.ECHPolicy {
	if p.echPolicy != "" {
		return p.echPolicy
	}
	if p.inner != nil {
		return p.inner.ECHPolicy()
	}
	return ""
}

func (p *mergedDialProvider) ECHConfigList() []byte {
	if len(p.echConfigList) > 0 {
		return append([]byte(nil), p.echConfigList...)
	}
	if p.inner != nil {
		return p.inner.ECHConfigList()
	}
	return nil
}

func (c *Connector) checkPins(nodes []model.NodeRegistryEntry) error {
	if !c.RequirePin {
		return nil
	}
	for _, node := range nodes {
		if err := c.checkPin(node); err != nil {
			return err
		}
	}
	return nil
}

func (c *Connector) checkPin(node model.NodeRegistryEntry) error {
	if !c.RequirePin {
		return nil
	}
	pin := append([]byte(nil), node.SPKIPin...)
	if c.Provider != nil {
		if p := c.Provider.PinnedPubKeyFor(node); len(p) > 0 {
			pin = append([]byte(nil), p...)
		}
	}
	if len(pin) == 0 {
		return fmt.Errorf("%w: SPKI pin required but empty for node %s", nvperr.ErrServerIdentityMismatch, node.NodeID)
	}
	return nil
}
