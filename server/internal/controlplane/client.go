package controlplane

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nyxveil/server/internal/nodeauth"
	"github.com/nyxveil/server/internal/version"
)

// Client talks to Nyxveil Control Plane Node APIs.
type Client struct {
	BaseURL    string
	HTTP       *http.Client
	NodeID     string
	PrivateKey ed25519.PrivateKey
	// TLS holds the shared-factory result used to build this client (diagnostics).
	TLS *TLSResult
}

func NewClient(baseURL string, tlsConfig *tls.Config) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("controlplane: invalid base URL %q", baseURL)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("controlplane: unsupported scheme %q", u.Scheme)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if tlsConfig != nil {
		tr.TLSClientConfig = tlsConfig
	} else if u.Scheme == "https" {
		// Prefer shared SystemTrust factory when caller passes nil.
		res, err := BuildTLS(TLSOptions{BaseURL: u.String()})
		if err != nil {
			return nil, err
		}
		tr.TLSClientConfig = res.Config
		c := &Client{
			BaseURL: u.String(),
			HTTP: &http.Client{
				Timeout:   30 * time.Second,
				Transport: tr,
			},
			TLS: res,
		}
		return c, nil
	}
	return &Client{
		BaseURL: u.String(),
		HTTP: &http.Client{
			Timeout:   30 * time.Second,
			Transport: tr,
		},
	}, nil
}

type Endpoint struct {
	Host          string `json:"host"`
	Port          int    `json:"port"`
	AddressFamily string `json:"address_family"`
	Priority      int    `json:"priority"`
	Enabled       bool   `json:"enabled"`
}

type RegisterRequest struct {
	BootstrapToken  string     `json:"bootstrap_token,omitempty"`
	NodeToken       string     `json:"node_token,omitempty"`
	NodeID          string     `json:"node_id"`
	LocationID      string     `json:"location_id"`
	DisplayName     string     `json:"display_name"`
	PublicIdentity  []byte     `json:"public_identity"`
	PublicKey       []byte     `json:"public_key"`
	ServerName      string     `json:"server_name,omitempty"`
	SPKIPin         []byte     `json:"spki_pin,omitempty"`
	ProtocolVersion uint16     `json:"protocol_version"`
	ServerVersion   string     `json:"server_version,omitempty"`
	Capacity        int        `json:"capacity"`
	TestOnly        bool       `json:"test_only"`
	Endpoints       []Endpoint `json:"endpoints"`
}

type RegisterResponse struct {
	NodeID        string      `json:"node_id"`
	Registered    bool        `json:"registered"`
	NodeToken     string      `json:"node_token"`
	ConfigVersion int64       `json:"config_version"`
	Config        *NodeConfig `json:"config"`
}

type NodeConfig struct {
	NodeID                 string  `json:"node_id"`
	LocationID             string  `json:"location_id"`
	Enabled                bool    `json:"enabled"`
	Draining               bool    `json:"draining"`
	MaintenanceMode        bool    `json:"maintenance_mode"`
	TransportPolicyJSON    string  `json:"transport_policy_json"`
	ECHPolicyJSON          *string `json:"ech_policy_json"`
	MTU                    *int    `json:"mtu"`
	Capacity               int     `json:"capacity"`
	MinimumServerVersion   *string `json:"minimum_server_version"`
	MinimumProtocolVersion *uint16 `json:"minimum_protocol_version"`
	ConfigVersion          int64   `json:"config_version"`
	UpdatedAt              APITime `json:"updated_at"`
}

type HeartbeatRequest struct {
	NodeID                 string   `json:"node_id"`
	Version                string   `json:"version"`
	ProtocolVersion        uint16   `json:"protocol_version"`
	Capacity               int      `json:"capacity"`
	CurrentSessions        int      `json:"current_sessions"`
	Load                   float64  `json:"load"`
	CPUUsage               *float64 `json:"cpu_usage,omitempty"`
	MemoryUsage            *float64 `json:"memory_usage,omitempty"`
	MemoryBytes            *int64   `json:"memory_bytes,omitempty"`
	Uptime                 *int64   `json:"uptime,omitempty"`
	NetworkRxRate          *float64 `json:"network_rx_rate,omitempty"`
	NetworkTxRate          *float64 `json:"network_tx_rate,omitempty"`
	Healthy                *bool    `json:"healthy,omitempty"`
	TLSMode                string   `json:"tls_mode,omitempty"`
	CertSubject            string   `json:"cert_subject,omitempty"`
	CertIssuer             string   `json:"cert_issuer,omitempty"`
	CertSAN                string   `json:"cert_san,omitempty"`
	CertNotBefore          string   `json:"cert_not_before,omitempty"`
	CertNotAfter           string   `json:"cert_not_after,omitempty"`
	CertThumbprint         string   `json:"cert_thumbprint,omitempty"`
	ACMEAutoRenew          *bool    `json:"acme_auto_renew,omitempty"`
	LastRenewalAttempt     string   `json:"last_renewal_attempt,omitempty"`
	LastSuccessfulRenewal  string   `json:"last_successful_renewal,omitempty"`
	NextPlannedRenewal     string   `json:"next_planned_renewal,omitempty"`
	LastRenewalError       string   `json:"last_renewal_error,omitempty"`
	TUNReady               *bool    `json:"tun_ready,omitempty"`
	TLSOK                  *bool    `json:"tls_ok,omitempty"`
	QUICOK                 *bool    `json:"quic_ok,omitempty"`
	BridgeOK               *bool    `json:"bridge_ok,omitempty"`
	TicketKeysLoaded       *bool    `json:"ticket_keys_loaded,omitempty"`
	RevocationStale        *bool    `json:"revocation_stale,omitempty"`
	CPConnected            *bool    `json:"cp_connected,omitempty"`
	ManagementCapabilities string   `json:"management_capabilities,omitempty"`
	BootID                 string   `json:"boot_id,omitempty"`
	SupportsCommands       *bool    `json:"supports_commands,omitempty"`
}

// NodeCommand is GET /api/v1/node/commands/next (snake_case matches Control Plane DTO).
type NodeCommand struct {
	ID              string  `json:"id"`
	NodeID          string  `json:"node_id"`
	Type            string  `json:"type"`
	Status          string  `json:"status"`
	IssuedAt        APITime `json:"issued_at"`
	ExpiresAt       APITime `json:"expires_at"`
	CorrelationID   string  `json:"correlation_id"`
	PayloadJSON     *string `json:"payload_json,omitempty"`
	PreviousVersion string  `json:"previous_version,omitempty"`
	TargetVersion   string  `json:"target_version,omitempty"`
}

// NodeCommandResultRequest is POST /api/v1/node/commands/{id}/result body.
type NodeCommandResultRequest struct {
	Success       bool   `json:"success"`
	ResultCode    string `json:"result_code,omitempty"`
	ResultMessage string `json:"result_message,omitempty"`
	BootID        string `json:"boot_id,omitempty"`
}

// ErrNoCommand indicates GET /commands/next returned 204 No Content.
var ErrNoCommand = errors.New("controlplane: no command available")

type HeartbeatResponse struct {
	Accepted      bool   `json:"accepted"`
	Status        string `json:"status"`
	ConfigVersion int64  `json:"config_version"`
}

type RevocationSnapshot struct {
	RevokedJTIs     []string `json:"revoked_jtis"`
	RevokedLicenses []string `json:"revoked_licenses"`
	RevokedDevices  []string `json:"revoked_devices"`
	UpdatedAt       int64    `json:"updated_at"`
}

func (c *Client) Register(ctx context.Context, req RegisterRequest) (*RegisterResponse, error) {
	if req.ProtocolVersion == 0 {
		req.ProtocolVersion = version.ProtocolNumber
	}
	if req.ServerVersion == "" {
		req.ServerVersion = version.ServerVersion
	}
	if req.Capacity <= 0 {
		req.Capacity = 100
	}
	if req.Endpoints == nil {
		req.Endpoints = []Endpoint{}
	}
	var out RegisterResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/nodes/register", req, false, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Heartbeat(ctx context.Context, hb HeartbeatRequest) (*HeartbeatResponse, error) {
	if hb.NodeID == "" {
		hb.NodeID = c.NodeID
	}
	if hb.Version == "" {
		hb.Version = version.ServerVersion
	}
	if hb.ProtocolVersion == 0 {
		hb.ProtocolVersion = version.ProtocolNumber
	}
	path := "/api/v1/nodes/" + url.PathEscape(hb.NodeID) + "/health"
	var out HeartbeatResponse
	if err := c.doJSON(ctx, http.MethodPost, path, hb, true, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetConfig(ctx context.Context) (*NodeConfig, error) {
	var out NodeConfig
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/node/config", nil, true, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateNodeSPKIRequest is POST /api/v1/node/spki (NodeAuth).
type UpdateNodeSPKIRequest struct {
	SPKIPin []byte `json:"spki_pin"`
}

// UpdateNodeSPKIResponse is the NodeAuth SPKI maintenance result.
type UpdateNodeSPKIResponse struct {
	NodeID        string `json:"node_id"`
	SPKIPin       []byte `json:"spki_pin"`
	ConfigVersion int64  `json:"config_version"`
}

// UpdateNodeSPKI advertises a leaf SPKI pin for the authenticated node only.
// Used for runtime/background TLS renewal — NOT installer bootstrap registration.
func (c *Client) UpdateNodeSPKI(ctx context.Context, pin []byte) (*UpdateNodeSPKIResponse, error) {
	if len(pin) != 32 {
		return nil, fmt.Errorf("controlplane: spki_pin must be 32 bytes, got %d", len(pin))
	}
	req := UpdateNodeSPKIRequest{SPKIPin: append([]byte(nil), pin...)}
	var out UpdateNodeSPKIResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/node/spki", req, true, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetRevocation(ctx context.Context) (*RevocationSnapshot, error) {
	var out RevocationSnapshot
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/revocation", nil, true, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TicketKeysResponse is GET /api/v1/node/ticket-keys (verification pubs only).
type TicketKeysResponse struct {
	Issuer    string            `json:"issuer"`
	Keys      map[string]string `json:"keys"` // kid -> std base64 Ed25519 pubkey
	UpdatedAt int64             `json:"updated_at"`
}

// GetTicketKeys fetches Access Ticket verification public keys (signed req-v2).
func (c *Client) GetTicketKeys(ctx context.Context) (*TicketKeysResponse, error) {
	var out TicketKeysResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/node/ticket-keys", nil, true, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetCatalogKeys fetches the public catalog-signing key ring.
func (c *Client) GetCatalogKeys(ctx context.Context) ([]byte, error) {
	var out json.RawMessage
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/catalog-keys", nil, false, &out); err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// GetSignedSelf fetches the node-authenticated single-node signed catalog.
func (c *Client) GetSignedSelf(ctx context.Context) ([]byte, error) {
	var out json.RawMessage
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/node/signed-self", nil, true, &out); err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// ClaimNextCommand claims the next pending remote command for this node (or ErrNoCommand).
func (c *Client) ClaimNextCommand(ctx context.Context) (*NodeCommand, error) {
	status, respBody, err := c.doRequest(ctx, http.MethodGet, "/api/v1/node/commands/next", nil, true)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNoContent {
		return nil, ErrNoCommand
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("controlplane: GET /api/v1/node/commands/next -> %d: %s", status, truncate(string(respBody), 512))
	}
	var cmd NodeCommand
	if err := json.Unmarshal(respBody, &cmd); err != nil {
		return nil, &AcceptedLocalError{
			Method: http.MethodGet,
			Path:   "/api/v1/node/commands/next",
			Status: status,
			Body:   append([]byte(nil), respBody...),
			Err:    err,
		}
	}
	return &cmd, nil
}

// MarkCommandStarted reports that execution has begun for a claimed command.
func (c *Client) MarkCommandStarted(ctx context.Context, commandID string) error {
	path := "/api/v1/node/commands/" + url.PathEscape(commandID) + "/started"
	return c.doJSON(ctx, http.MethodPost, path, nil, true, nil)
}

// ReportCommandResult posts the final (or intermediate reboot-accepted) outcome.
func (c *Client) ReportCommandResult(ctx context.Context, commandID string, req NodeCommandResultRequest) error {
	path := "/api/v1/node/commands/" + url.PathEscape(commandID) + "/result"
	return c.doJSON(ctx, http.MethodPost, path, req, true, nil)
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, sign bool, out any) error {
	status, respBody, err := c.doRequest(ctx, method, path, body, sign)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("controlplane: %s %s -> %d: %s", method, path, status, truncate(string(respBody), 512))
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return &AcceptedLocalError{
			Method: method,
			Path:   path,
			Status: status,
			Body:   append([]byte(nil), respBody...),
			Err:    err,
		}
	}
	return nil
}

func (c *Client) doRequest(ctx context.Context, method, path string, body any, sign bool) (int, []byte, error) {
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
	}
	u := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if sign {
		if c.NodeID == "" || len(c.PrivateKey) == 0 {
			return 0, nil, fmt.Errorf("controlplane: signed request requires NodeID and private key")
		}
		pq := nodeauth.CanonicalPathQuery(req)
		if err := nodeauth.SignRequestV2(req, c.NodeID, c.PrivateKey, pq, raw); err != nil {
			return 0, nil, err
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if isTLSError(err) {
			if c.TLS != nil {
				LogTLSFailure(c.TLS, err)
			}
			if c.HTTP != nil {
				c.HTTP.CloseIdleConnections()
			}
		}
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, respBody, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func isTLSError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "tls:") || strings.Contains(msg, "x509:") || strings.Contains(msg, "certificate")
}
