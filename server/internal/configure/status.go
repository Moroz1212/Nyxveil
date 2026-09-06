package configure

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/nyxveil/server/internal/localconfig"
	"github.com/nyxveil/server/internal/paths"
)

// StatusView is operator-facing configure/status summary (no secrets).
type StatusView struct {
	NodeID       string   `json:"node_id"`
	LocationID   string   `json:"location_id"`
	PublicHost   string   `json:"public_host"`
	ServerName   string   `json:"server_name,omitempty"`
	DNSServers   []string `json:"dns_servers"`
	ACMEDomain   string   `json:"acme_domain,omitempty"`
	ACMEEmail    string   `json:"acme_email,omitempty"`
	TLSListen    string   `json:"tls_listen,omitempty"`
	QUICListen   string   `json:"quic_listen,omitempty"`
	Certificate  CertInfo `json:"certificate"`
	NodeKeyPath  string   `json:"node_key_path"`
	ConfigPath   string   `json:"config_path"`
}

// LoadStatus reads server.json + cert metadata for display.
func LoadStatus(configPath string) (*StatusView, error) {
	if configPath == "" {
		configPath = paths.ServerConfig()
	}
	cfg, err := localconfig.Load(configPath)
	if err != nil {
		return nil, err
	}
	certPath, keyPath := DefaultTLSPaths(cfg.TLSCertFile, cfg.TLSKeyFile)
	view := &StatusView{
		NodeID:      cfg.NodeID,
		LocationID:  cfg.LocationID,
		PublicHost:  cfg.PublicHost,
		ServerName:  cfg.ServerName,
		DNSServers:  append([]string(nil), cfg.DNSServers...),
		ACMEDomain:  cfg.ACMEDomain,
		ACMEEmail:   cfg.ACMEEmail,
		TLSListen:   cfg.TLSListen,
		QUICListen:  cfg.QUICListen,
		Certificate: InspectCert(certPath, keyPath, cfg.ACMEDomain),
		NodeKeyPath: paths.NodeKey(),
		ConfigPath:  configPath,
	}
	if _, err := os.Stat(paths.NodeKey()); err != nil {
		return view, fmt.Errorf("configure status: node.key missing: %w", err)
	}
	return view, nil
}

// PrintStatusJSON writes indented JSON to stdout.
func PrintStatusJSON(v *StatusView) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
