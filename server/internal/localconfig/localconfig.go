package localconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nyxveil/server/internal/controlplane"
)

// File is the installer/static bootstrap config at /etc/nyxveil/server.json.
// The daemon treats this file as read-only. Dynamic Control Plane fields
// (location, capacity, transport/ECH policy, config_version, …) live in
// /var/lib/nyxveil/applied-config.json.
type File struct {
	ControlPlaneURL     string `json:"control_plane_url"`
	NodeID              string `json:"node_id"`
	LocationID          string `json:"location_id"` // bootstrap only; applied-config overrides at runtime
	DisplayName         string `json:"display_name"`
	ConfigVersion       int64  `json:"config_version,omitempty"` // bootstrap/legacy; authoritative value is applied-config
	ServerName          string `json:"server_name,omitempty"`
	PublicHost          string `json:"public_host,omitempty"`
	TLSListen           string `json:"tls_listen,omitempty"`
	QUICListen          string `json:"quic_listen,omitempty"`
	VPNSubnetCIDR       string `json:"vpn_subnet_cidr,omitempty"`
	HeartbeatSec        int    `json:"heartbeat_seconds,omitempty"`
	TLSCertFile         string `json:"tls_cert_file,omitempty"`
	TLSKeyFile          string `json:"tls_key_file,omitempty"`
	PinnedCAFile        string `json:"pinned_ca_file,omitempty"`
	ControlPlaneSPKIPin string `json:"control_plane_spki_pin,omitempty"` // hex SHA-256 of peer SPKI
	UpdateURL           string `json:"update_url,omitempty"`
}

func Default() File {
	return File{
		TLSListen:     ":443",
		QUICListen:    ":443",
		VPNSubnetCIDR: "10.66.0.0/24",
		HeartbeatSec:  30,
	}
}

func Load(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := Default()
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	if cfg.ControlPlaneURL == "" || cfg.NodeID == "" {
		return nil, fmt.Errorf("localconfig: control_plane_url and node_id are required")
	}
	return &cfg, nil
}

func (f *File) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// AppliedSnapshot persists last successfully applied Control Plane config.
type AppliedSnapshot struct {
	SavedAt time.Time               `json:"saved_at"`
	Config  controlplane.NodeConfig `json:"config"`
}

func SaveApplied(path string, cfg controlplane.NodeConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	snap := AppliedSnapshot{SavedAt: time.Now().UTC(), Config: cfg}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func LoadApplied(path string) (*AppliedSnapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var snap AppliedSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}
