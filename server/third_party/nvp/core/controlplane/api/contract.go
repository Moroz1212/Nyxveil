// Package api defines the Control Plane HTTP API contract.
package api

// API version prefix for all Control Plane endpoints.
const Version = "v1"

// Endpoint paths (relative to /api/v1).
const (
	PathLicenseValidate = "/license/validate"
	PathDeviceActivate  = "/device/activate"
	PathDeviceRemove    = "/device/remove"
	PathTicketIssue     = "/ticket/issue"
	PathTicketRefresh   = "/ticket/refresh"
	PathCatalog         = "/catalog"
	PathLocations       = "/locations"
	PathNodes           = "/nodes"
	PathNodeHealth      = "/nodes/{node_id}/health"
	PathNodeDrain       = "/nodes/{node_id}/drain"
	PathNodeMaintenance = "/nodes/{node_id}/maintenance"
	PathRevocation      = "/revocation"
	PathVersion         = "/version"
	PathMasterAccess    = "/master/access"
)

// LicenseValidateRequest validates a license token.
type LicenseValidateRequest struct {
	LicenseToken string `json:"license_token"`
}

type LicenseValidateResponse struct {
	Valid      bool   `json:"valid"`
	LicenseID  string `json:"license_id,omitempty"`
	Plan       string `json:"plan,omitempty"`
	MaxDevices int    `json:"max_devices,omitempty"`
	Message    string `json:"message,omitempty"`
}

// DeviceActivateRequest registers a device with Control Plane.
type DeviceActivateRequest struct {
	LicenseToken string `json:"license_token"`
	DeviceID     string `json:"device_id"`
	PublicKey    []byte `json:"public_key"`
}

type DeviceActivateResponse struct {
	DeviceID  string `json:"device_id"`
	Activated bool   `json:"activated"`
}

// TicketIssueRequest requests a short-lived VPN access ticket.
type TicketIssueRequest struct {
	LicenseToken string `json:"license_token"`
	DeviceID     string `json:"device_id"`
	NodeID       string `json:"node_id,omitempty"`
	LocationID   string `json:"location_id,omitempty"`
}

type TicketIssueResponse struct {
	AccessTicket string `json:"access_ticket"`
	ExpiresAt    int64  `json:"expires_at"`
	NodeID       string `json:"node_id,omitempty"`
}

// TicketRefreshRequest refreshes an existing access ticket.
// Refresh rebuilds claims from the CURRENT license/device (role, permissions,
// locations). Clients cannot escalate beyond current entitlements; stale rights
// from the old ticket are dropped on downgrade.
type TicketRefreshRequest struct {
	LicenseToken string `json:"license_token"`
	DeviceID     string `json:"device_id"`
	AccessTicket string `json:"access_ticket"` // existing ticket to refresh (required)
	RefreshHint  string `json:"refresh_hint,omitempty"`
}

// NodeHeartbeatRequest is sent by VPN nodes to Control Plane.
type NodeHeartbeatRequest struct {
	NodeID          string  `json:"node_id"`
	NodeToken       string  `json:"node_token"` // mTLS or signed node identity, not user token
	Version         string  `json:"version"`
	ProtocolVersion uint16  `json:"protocol_version"`
	Capacity        int     `json:"capacity"`
	CurrentSessions int     `json:"current_sessions"`
	Load            float64 `json:"load"`
}

// RevocationListResponse contains revoked JTIs, licenses, devices.
type RevocationListResponse struct {
	RevokedJTIs     []string `json:"revoked_jtis"`
	RevokedLicenses []string `json:"revoked_licenses"`
	RevokedDevices  []string `json:"revoked_devices"`
	UpdatedAt       int64    `json:"updated_at"`
}

// VersionResponse contains protocol and server version info.
type VersionResponse struct {
	ControlPlaneVersion string `json:"control_plane_version"`
	MinProtocolVersion  uint16 `json:"min_protocol_version"`
	MaxProtocolVersion  uint16 `json:"max_protocol_version"`
	RecommendedClient   string `json:"recommended_client_version"`
}
