package node

import (
	"crypto/ed25519"
	"encoding/json"
	"time"

	"github.com/nyxveil/nvp/core/transport"
)

// Status represents node operational status.
type Status string

const (
	StatusHealthy     Status = "healthy"
	StatusDegraded    Status = "degraded"
	StatusMaintenance Status = "maintenance"
	StatusOffline     Status = "offline"
)

// Descriptor is a signed node catalog entry.
type Descriptor struct {
	NodeID            string               `json:"node_id"`
	LocationID        string               `json:"location_id"`
	Country           string               `json:"country"`
	City              string               `json:"city"`
	DisplayName       string               `json:"display_name"`
	Status            Status               `json:"status"`
	Enabled           bool                 `json:"enabled"`
	TestOnly          bool                 `json:"test_only"`
	Draining          bool                 `json:"draining"`
	ProtocolVersion   uint16               `json:"protocol_version"`
	ServerVersion     string               `json:"server_version"`
	Endpoints         []transport.Endpoint `json:"endpoints"`
	ServerIdentityKey ed25519.PublicKey    `json:"server_identity_key"`
	Capacity          int                  `json:"capacity"`
	CurrentSessions   int                  `json:"current_sessions"`
	UpdatedAt         time.Time            `json:"updated_at"`
}

// SignedDescriptor wraps a descriptor with Control Plane signature.
type SignedDescriptor struct {
	Descriptor Descriptor `json:"descriptor"`
	KeyID      string     `json:"key_id"`
	Signature  []byte     `json:"signature"`
}

// Identity holds node service identity for Control Plane authentication.
type Identity struct {
	NodeID     string
	PrivateKey ed25519.PrivateKey
	PublicKey  ed25519.PublicKey
}

// Heartbeat is sent from node to Control Plane.
type Heartbeat struct {
	NodeID            string              `json:"node_id"`
	Version           string              `json:"version"`
	ProtocolVersion   uint16              `json:"protocol_version"`
	Capacity          int                 `json:"capacity"`
	CurrentSessions   int                 `json:"current_sessions"`
	Load              float64             `json:"load"`
	SupportedProfiles []transport.Profile `json:"supported_profiles"`
	Timestamp         time.Time           `json:"timestamp"`
}

// ParseSignedDescriptor unmarshals a signed node descriptor from JSON.
func ParseSignedDescriptor(data []byte) (SignedDescriptor, error) {
	var d SignedDescriptor
	if err := json.Unmarshal(data, &d); err != nil {
		return SignedDescriptor{}, err
	}
	return d, nil
}

// ParseDescriptor unmarshals a node descriptor from JSON.
func ParseDescriptor(data []byte) (Descriptor, error) {
	var d Descriptor
	if err := json.Unmarshal(data, &d); err != nil {
		return Descriptor{}, err
	}
	return d, nil
}

// CanonicalJSON returns deterministic JSON for signing.
func CanonicalJSON(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}
