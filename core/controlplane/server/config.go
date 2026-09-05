package server

import (
	"crypto/ed25519"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/controlplane/catalog"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"golang.org/x/crypto/chacha20poly1305"
)

// TLSConfig holds HTTPS listen configuration.
type TLSConfig struct {
	CertFile string
	KeyFile  string
}

// RateLimitConfig configures per-IP request rate limiting.
type RateLimitConfig struct {
	RequestsPerMinute int
	Burst             int
}

// DefaultRateLimit returns production defaults.
func DefaultRateLimit() RateLimitConfig {
	return RateLimitConfig{RequestsPerMinute: 120, Burst: 30}
}

// ServerOptions configures HTTP serving behavior.
type ServerOptions struct {
	TLS              TLSConfig
	RateLimit        RateLimitConfig
	AllowInsecureDev bool // HTTP on localhost only; empty KEK allowed for stub/dev
}

// Config holds Control Plane server configuration.
type Config struct {
	Issuer        ticket.IssuerConfig
	CatalogSigner catalog.Signer
	Catalog       model.Catalog
	MaxDevices    map[string]int
	Options       ServerOptions
}

// ListenConfig returns tls.Config when cert files are set.
func (o ServerOptions) ListenConfig() (*tls.Config, bool) {
	if o.TLS.CertFile == "" || o.TLS.KeyFile == "" {
		return nil, false
	}
	return &tls.Config{MinVersion: tls.VersionTLS13}, true
}

// RequireProduction fails closed when TLS or license KEK are missing/invalid.
func RequireProduction(opts ServerOptions) error {
	if opts.AllowInsecureDev {
		return nil
	}
	if opts.TLS.CertFile == "" || opts.TLS.KeyFile == "" {
		return fmt.Errorf("production requires TLS: set -cert and -key (or use -allow-insecure-dev for localhost-only HTTP)")
	}
	if !ValidLicenseKEK(os.Getenv("NVP_LICENSE_KEK")) {
		return fmt.Errorf("production requires NVP_LICENSE_KEK: 32-byte key as 64 hex characters (or use -allow-insecure-dev)")
	}
	return nil
}

// ValidLicenseKEK reports whether raw is a usable 32-byte ChaCha20 key (64 hex or raw bytes).
func ValidLicenseKEK(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	if len(raw) == 64 {
		b, err := hex.DecodeString(raw)
		return err == nil && len(b) == chacha20poly1305.KeySize
	}
	return len(raw) == chacha20poly1305.KeySize
}

// IsLocalhostListen reports whether addr binds only to loopback.
func IsLocalhostListen(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// Bare ":port" binds all interfaces вЂ” not localhost-only.
		if strings.HasPrefix(addr, ":") {
			return false
		}
		host = addr
		_ = port
	}
	if host == "" {
		return false
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func startHTTPServer(addr string, handler http.Handler, opts ServerOptions) (*http.Server, error) {
	srv := &http.Server{
		Addr:              addr,
		Handler:           wrapHandler(handler, opts.RateLimit),
		ReadHeaderTimeout: 10 * time.Second,
	}
	if _, ok := opts.ListenConfig(); ok {
		err := srv.ListenAndServeTLS(opts.TLS.CertFile, opts.TLS.KeyFile)
		return srv, err
	}
	if opts.AllowInsecureDev {
		if !IsLocalhostListen(addr) {
			return srv, fmt.Errorf("insecure HTTP (-allow-insecure-dev) only allowed on localhost, got %q", addr)
		}
		err := srv.ListenAndServe()
		return srv, err
	}
	return srv, fmt.Errorf("TLS required: set -cert and -key (refusing plaintext HTTP listen)")
}

// PublicKeys returns CP ticket verification keys for VPN nodes.
func PublicKeys(cfg Config) map[string]ed25519.PublicKey {
	return map[string]ed25519.PublicKey{
		cfg.Issuer.KeyID: cfg.Issuer.PrivateKey.Public().(ed25519.PublicKey),
	}
}

// WarnIfInsecure returns warnings for insecure configuration (dev diagnostics).
func WarnIfInsecure(opts ServerOptions) string {
	var msgs []string
	if _, ok := opts.ListenConfig(); !ok {
		msgs = append(msgs, "WARNING: Control Plane listening without TLS вЂ” use -cert and -key in production")
	}
	if !ValidLicenseKEK(os.Getenv("NVP_LICENSE_KEK")) {
		msgs = append(msgs, "WARNING: NVP_LICENSE_KEK unset/invalid вЂ” license secrets stored in plaintext SQLite")
	}
	return strings.Join(msgs, "\n")
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// MasterAccessRequest grants master role catalog access.
type MasterAccessRequest struct {
	LicenseToken string `json:"license_token"`
	DeviceID     string `json:"device_id"`
}

type MasterAccessResponse struct {
	Role    string `json:"role"`
	Granted bool   `json:"granted"`
}

// NodeDrainRequest toggles node drain state.
type NodeDrainRequest struct {
	NodeID    string `json:"node_id"`
	NodeToken string `json:"node_token"`
	Draining  bool   `json:"draining"`
}

type NodeMaintenanceRequest struct {
	NodeID    string `json:"node_id"`
	NodeToken string `json:"node_token"`
	Enabled   bool   `json:"enabled"`
}
