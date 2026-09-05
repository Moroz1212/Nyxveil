// Package health exposes node status for CLI and control socket.
package health

import (
	"github.com/nyxveil/server/internal/version"
)

// Status is a JSON-serializable node status snapshot (snake_case).
type Status struct {
	Running          bool    `json:"running"`
	Healthy          bool    `json:"healthy"`
	NodeID           string  `json:"node_id"`
	LocationID       string  `json:"location_id"`
	CPConnected      bool    `json:"cp_connected"`
	Sessions         int     `json:"sessions"`
	Capacity         int     `json:"capacity"`
	CPUUsage         float64 `json:"cpu_usage"`
	MemoryUsage      float64 `json:"memory_usage"`
	MemoryBytes      int64   `json:"memory_bytes"`
	ServerVersion    string  `json:"server_version"`
	CoreVersion      string  `json:"core_version"`
	ProtocolVersion  string  `json:"protocol_version"`
	ConfigVersion    int64   `json:"config_version"`
	TLSOK            bool    `json:"tls_ok"`
	QUICOK           bool    `json:"quic_ok"`
	TUNReady         bool    `json:"tun_ready"`
	BridgeOK         bool    `json:"bridge_ok"`
	SkipTUN          bool    `json:"skip_tun"`
	TicketKeysLoaded bool    `json:"ticket_keys_loaded"`
	IdentityPresent  bool    `json:"identity_present"`
	VersionBlocked   bool    `json:"version_blocked"`
	Accepting        bool    `json:"accepting"`
	Enabled          bool    `json:"enabled"`
	Draining         bool    `json:"draining"`
	MaintenanceMode  bool    `json:"maintenance_mode"`
	UptimeSeconds    int64   `json:"uptime_seconds"`
	RevocationStale  bool    `json:"revocation_stale"`
	Message          string  `json:"message,omitempty"`
}

// DefaultVersions fills version fields from the version package.
func (s *Status) DefaultVersions() {
	if s.ServerVersion == "" {
		s.ServerVersion = version.ServerVersion
	}
	if s.CoreVersion == "" {
		s.CoreVersion = version.CoreVersion
	}
	if s.ProtocolVersion == "" {
		s.ProtocolVersion = version.ProtocolVersion
	}
}

// ComputeHealthy applies production health gates.
// When the node is disabled (not accepting and not enabled), listener readiness
// is not required.
func (s *Status) ComputeHealthy() bool {
	if !s.Running {
		return false
	}
	if !s.IdentityPresent {
		return false
	}
	if !s.CPConnected {
		return false
	}
	if s.RevocationStale {
		return false
	}
	if !s.TicketKeysLoaded {
		return false
	}
	if s.VersionBlocked {
		return false
	}
	if !s.SkipTUN {
		if !s.TUNReady {
			return false
		}
		if !s.BridgeOK {
			return false
		}
	}
	// Listeners: when accepting (enabled + not drain/maint), need at least one OK.
	if s.Accepting {
		if !s.TLSOK && !s.QUICOK {
			return false
		}
	}
	return true
}
