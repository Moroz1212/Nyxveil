package model

import (
	"time"

	"github.com/nyxveil/nvp/node"
	"github.com/nyxveil/nvp/transport"
)

// Location groups multiple nodes in the same geographic area.
type Location struct {
	LocationID  string `json:"location_id"`
	Country     string `json:"country"`
	CountryCode string `json:"country_code"`
	City        string `json:"city"`
	DisplayName string `json:"display_name"`
	Enabled     bool   `json:"enabled"`
}

// NodeRegistryEntry is the Control Plane node registry model.
type NodeRegistryEntry struct {
	NodeID          string               `json:"node_id"`
	LocationID      string               `json:"location_id"`
	Country         string               `json:"country"`
	City            string               `json:"city"`
	DisplayName     string               `json:"display_name"`
	Status          node.Status          `json:"status"`
	Enabled         bool                 `json:"enabled"`
	TestOnly        bool                 `json:"test_only"`
	Draining        bool                 `json:"draining"`
	ProtocolVersion uint16               `json:"protocol_version"`
	ServerVersion   string               `json:"server_version"`
	Endpoints       []transport.Endpoint `json:"endpoints"`
	Capacity        int                  `json:"capacity"`
	CurrentSessions int                  `json:"current_sessions"`
	Health          HealthInfo           `json:"health"`
	LastSeen        time.Time            `json:"last_seen"`
}

// HealthInfo contains node health metrics.
type HealthInfo struct {
	Healthy       bool    `json:"healthy"`
	LatencyMs     float64 `json:"latency_ms"`
	SessionCount  int     `json:"session_count"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryPercent float64 `json:"memory_percent"`
}

// Catalog is a signed list of nodes and locations for clients.
type Catalog struct {
	Version   string              `json:"version"`
	Locations []Location          `json:"locations"`
	Nodes     []NodeRegistryEntry `json:"nodes"`
	IssuedAt  time.Time           `json:"issued_at"`
	ExpiresAt time.Time           `json:"expires_at"`
}

// SignedCatalog wraps catalog with Control Plane signature.
type SignedCatalog struct {
	Catalog   Catalog `json:"catalog"`
	KeyID     string  `json:"key_id"`
	Signature []byte  `json:"signature"`
}

// LicenseRecord represents a license in Control Plane.
type LicenseRecord struct {
	LicenseID  string    `json:"license_id"`
	Plan       string    `json:"plan"`
	MaxDevices int       `json:"max_devices"`
	Enabled    bool      `json:"enabled"`
	Revoked    bool      `json:"revoked"`
	ExpiresAt  time.Time `json:"expires_at"`
	Locations  []string  `json:"allowed_locations"`
}

// DeviceRecord represents a registered device.
type DeviceRecord struct {
	DeviceID   string    `json:"device_id"`
	LicenseID  string    `json:"license_id"`
	PublicKey  []byte    `json:"public_key"`
	Enabled    bool      `json:"enabled"`
	Revoked    bool      `json:"revoked"`
	Registered time.Time `json:"registered_at"`
	LastSeen   time.Time `json:"last_seen"`
}
