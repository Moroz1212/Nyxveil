package configure

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/nyxveil/server/internal/controlplane"
	"github.com/nyxveil/server/internal/identity"
	"github.com/nyxveil/server/internal/localconfig"
)

// NormalizeControlPlaneURL validates and normalizes an HTTPS Control Plane base URL.
// Production configure rejects non-HTTPS schemes.
func NormalizeControlPlaneURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("configure: --control-plane-url is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("configure: invalid --control-plane-url: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return "", fmt.Errorf("configure: --control-plane-url must use https:// (got %q)", u.Scheme)
	}
	if u.Host == "" || u.Hostname() == "" {
		return "", fmt.Errorf("configure: --control-plane-url missing host")
	}
	if u.User != nil {
		return "", fmt.Errorf("configure: --control-plane-url must not include userinfo")
	}
	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), nil
}

// LookupIPFunc optional DNS override for CP URL probes (tests).
var LookupIPFunc func(host string) ([]net.IP, error)

// ProbeControlPlaneTLS dials the Control Plane with SystemTrust (no InsecureSkipVerify).
func ProbeControlPlaneTLS(ctx context.Context, baseURL string, dialer *net.Dialer) error {
	normalized, err := NormalizeControlPlaneURL(baseURL)
	if err != nil {
		return err
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return err
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "443"
	}
	addr := net.JoinHostPort(host, port)

	if dialer == nil {
		dialer = &net.Dialer{Timeout: 15 * time.Second}
	}

	if LookupIPFunc != nil {
		ips, err := LookupIPFunc(host)
		if err != nil {
			return fmt.Errorf("configure: control plane DNS resolve failed for %q: %w", host, err)
		}
		if len(ips) == 0 {
			return fmt.Errorf("configure: control plane DNS returned no addresses for %q", host)
		}
	} else {
		var r net.Resolver
		addrs, err := r.LookupIPAddr(ctx, host)
		if err != nil {
			return fmt.Errorf("configure: control plane DNS resolve failed for %q: %w", host, err)
		}
		if len(addrs) == 0 {
			return fmt.Errorf("configure: control plane DNS returned no addresses for %q", host)
		}
	}

	rawConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("configure: control plane TCP unreachable %s: %w", addr, err)
	}
	defer rawConn.Close()
	_ = rawConn.SetDeadline(time.Now().Add(20 * time.Second))

	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         host,
		InsecureSkipVerify: false,
	}
	conn := tls.Client(rawConn, tlsCfg)
	if err := conn.HandshakeContext(ctx); err != nil {
		return fmt.Errorf("configure: control plane SystemTrust TLS failed for %s: %w", host, err)
	}
	defer conn.Close()

	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return fmt.Errorf("configure: control plane presented no certificate")
	}
	leaf := state.PeerCertificates[0]
	now := time.Now()
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return fmt.Errorf("configure: control plane certificate outside validity window")
	}
	if err := leaf.VerifyHostname(host); err != nil {
		return fmt.Errorf("configure: control plane hostname mismatch: %w", err)
	}
	return nil
}

// ControlPlaneProbeResult summarizes a pre-commit CP reachability check (no secrets).
type ControlPlaneProbeResult struct {
	URL           string `json:"url"`
	TLSOK         bool   `json:"tls_ok"`
	AuthOK        bool   `json:"auth_ok"`
	NodeID        string `json:"node_id,omitempty"`
	LocationID    string `json:"location_id,omitempty"`
	ConfigVersion int64  `json:"config_version,omitempty"`
	Message       string `json:"message,omitempty"`
}

// ProbeControlPlaneAuth performs signed GetConfig against the target URL using existing node identity.
func ProbeControlPlaneAuth(ctx context.Context, baseURL, nodeKeyPath string, nodeID string) (*ControlPlaneProbeResult, error) {
	normalized, err := NormalizeControlPlaneURL(baseURL)
	if err != nil {
		return nil, err
	}
	out := &ControlPlaneProbeResult{URL: normalized}
	if err := ProbeControlPlaneTLS(ctx, normalized, nil); err != nil {
		return out, err
	}
	out.TLSOK = true

	b, err := os.ReadFile(nodeKeyPath)
	if err != nil {
		return out, fmt.Errorf("configure: load node.key for CP probe: %w", err)
	}
	k, err := identity.ParsePEM(b)
	if err != nil {
		return out, err
	}

	u, _ := url.Parse(normalized)
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: u.Hostname(),
	}
	client, err := controlplane.NewClient(normalized, tlsCfg)
	if err != nil {
		return out, err
	}
	client.NodeID = nodeID
	client.PrivateKey = k.Private

	cfg, err := client.GetConfig(ctx)
	if err != nil {
		return out, fmt.Errorf("configure: control plane auth/reachability failed (signed GetConfig): %w", err)
	}
	out.AuthOK = true
	out.NodeID = cfg.NodeID
	out.LocationID = cfg.LocationID
	out.ConfigVersion = cfg.ConfigVersion
	if nodeID != "" && cfg.NodeID != "" && !strings.EqualFold(cfg.NodeID, nodeID) {
		return out, fmt.Errorf("configure: target CP returned unexpected node_id %q (want %q)", cfg.NodeID, nodeID)
	}
	out.Message = "control plane TLS+auth OK"
	return out, nil
}

// ProbeControlPlaneAuthFromConfig wraps ProbeControlPlaneAuth with cfg.NodeID.
func ProbeControlPlaneAuthFromConfig(ctx context.Context, targetURL string, cfg *localconfig.File, nodeKeyPath string) (*ControlPlaneProbeResult, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configure: nil config")
	}
	return ProbeControlPlaneAuth(ctx, targetURL, nodeKeyPath, cfg.NodeID)
}

// CatalogFreshness is a post-reconnect verification snapshot (no secrets).
type CatalogFreshness struct {
	Verified      bool   `json:"verified"`
	NodeID        string `json:"node_id,omitempty"`
	LocationID    string `json:"location_id,omitempty"`
	ServerVersion string `json:"server_version,omitempty"`
	SPKIHex       string `json:"spki_sha256,omitempty"`
	Online        bool   `json:"online,omitempty"`
	Message       string `json:"message,omitempty"`
}

// DefaultVerifyCatalogAfterCPURL uses signed GetConfig + Heartbeat against the configured CP URL.
func DefaultVerifyCatalogAfterCPURL(ctx context.Context, cfgPath, nodeKeyPath, spkiHex string) (*CatalogFreshness, error) {
	cfg, err := localconfig.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(nodeKeyPath)
	if err != nil {
		return nil, err
	}
	k, err := identity.ParsePEM(b)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(cfg.ControlPlaneURL)
	if err != nil {
		return nil, err
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname()}
	client, err := controlplane.NewClient(cfg.ControlPlaneURL, tlsCfg)
	if err != nil {
		return nil, err
	}
	client.NodeID = cfg.NodeID
	client.PrivateKey = k.Private

	got, err := client.GetConfig(ctx)
	if err != nil {
		return &CatalogFreshness{Message: err.Error()}, fmt.Errorf("configure: catalog/management verify GetConfig: %w", err)
	}
	if got.NodeID != cfg.NodeID {
		return nil, fmt.Errorf("configure: CP node_id mismatch: got %q want %q", got.NodeID, cfg.NodeID)
	}
	if got.LocationID != "" && cfg.LocationID != "" && got.LocationID != cfg.LocationID {
		return nil, fmt.Errorf("configure: CP location_id mismatch: got %q want %q", got.LocationID, cfg.LocationID)
	}

	if _, err := client.Heartbeat(ctx, controlplane.HeartbeatRequest{
		NodeID:  cfg.NodeID,
		Healthy: boolPtr(true),
	}); err != nil {
		return &CatalogFreshness{
			NodeID:     got.NodeID,
			LocationID: got.LocationID,
			Message:    err.Error(),
		}, fmt.Errorf("configure: heartbeat after CP URL change failed: %w", err)
	}

	return &CatalogFreshness{
		Verified:   true,
		NodeID:     got.NodeID,
		LocationID: got.LocationID,
		SPKIHex:    spkiHex,
		Online:     true,
		Message:    "GetConfig+Heartbeat OK against target Control Plane",
	}, nil
}

func boolPtr(v bool) *bool { return &v }

// HTTPClientForSystemTrust is exported for tests.
func HTTPClientForSystemTrust(serverName string) *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				ServerName: serverName,
			},
		},
	}
}
