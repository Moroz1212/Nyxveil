package controlplane

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
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
	NodeID                 string    `json:"node_id"`
	LocationID             string    `json:"location_id"`
	Enabled                bool      `json:"enabled"`
	Draining               bool      `json:"draining"`
	MaintenanceMode        bool      `json:"maintenance_mode"`
	TransportPolicyJSON    string    `json:"transport_policy_json"`
	ECHPolicyJSON          *string   `json:"ech_policy_json"`
	MTU                    *int      `json:"mtu"`
	Capacity               int       `json:"capacity"`
	MinimumServerVersion   *string   `json:"minimum_server_version"`
	MinimumProtocolVersion *uint16   `json:"minimum_protocol_version"`
	ConfigVersion          int64     `json:"config_version"`
	UpdatedAt              time.Time `json:"updated_at"`
}

type HeartbeatRequest struct {
	NodeID          string   `json:"node_id"`
	Version         string   `json:"version"`
	ProtocolVersion uint16   `json:"protocol_version"`
	Capacity        int      `json:"capacity"`
	CurrentSessions int      `json:"current_sessions"`
	Load            float64  `json:"load"`
	CPUUsage        *float64 `json:"cpu_usage,omitempty"`
	MemoryUsage     *float64 `json:"memory_usage,omitempty"`
	MemoryBytes     *int64   `json:"memory_bytes,omitempty"`
	Uptime          *int64   `json:"uptime,omitempty"`
	NetworkRxRate   *float64 `json:"network_rx_rate,omitempty"`
	NetworkTxRate   *float64 `json:"network_tx_rate,omitempty"`
	Healthy         *bool    `json:"healthy,omitempty"`
}

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

func (c *Client) doJSON(ctx context.Context, method, path string, body any, sign bool, out any) error {
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	u := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if sign {
		if c.NodeID == "" || len(c.PrivateKey) == 0 {
			return fmt.Errorf("controlplane: signed request requires NodeID and private key")
		}
		pq := nodeauth.CanonicalPathQuery(req)
		if err := nodeauth.SignRequestV2(req, c.NodeID, c.PrivateKey, pq, raw); err != nil {
			return err
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("controlplane: %s %s -> %d: %s", method, path, resp.StatusCode, truncate(string(respBody), 512))
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	return json.Unmarshal(respBody, out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
