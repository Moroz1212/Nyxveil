// Package configure implements transactional reconfiguration of an already-registered
// Nyxveil VPN node without creating a new identity or requiring a bootstrap token.
package configure

import (
	"context"
	"fmt"
	"net"
	"strings"
)

// Options are operator flags for nyxveilctl configure.
type Options struct {
	ConfigPath string // default paths.ServerConfig()

	PublicHost string
	DNSServers string // comma-separated IPv4

	TLSDomain  string
	TLSEmail   string
	TLSCert    string // operator cert path
	TLSKey     string // operator key path
	TLSReplace bool

	DryRun  bool // --check / --dry-run: validate only
	SkipCP  bool // tests: skip Control Plane register
	SkipFW  bool // tests: skip nftables
	SkipSvc bool // tests: skip stop/start/health

	// Test overrides (empty = production defaults).
	NodeKeyPath   string
	StateDir      string
	NFTFile       string
	SkipCertTrust bool // tests: skip system-trust verify (still checks SAN/expiry/key)

	// Hooks (overridable in tests).
	ExecRegister  func(cfgPath string) error
	ExecSystemctl func(action, unit string) error
	ExecHealth    func() error
	ExecACME      func(ctx context.Context, args ACMEIssueArgs) error
	ExecFirewall  func(opts FirewallOpts) error // default ApplyNyxveilFirewall
	OnBeforeACME  func()                        // fired after TCP/80 FW prep, before HTTP-01
	LookupIP      func(host string) ([]net.IP, error)
	PublicIPHint  string // optional expected public IP for DNS check (tests / --expect-public-ip)
}

// ParseDNSServers validates and splits a comma-separated IPv4 list.
func ParseDNSServers(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("configure: dns-servers required (non-empty)")
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("configure: dns-servers contains empty entry")
		}
		ip := net.ParseIP(p)
		if ip == nil || ip.To4() == nil {
			return nil, fmt.Errorf("configure: dns-servers entry %q is not a valid IPv4 address", p)
		}
		out = append(out, ip.To4().String())
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("configure: dns-servers must contain at least one IPv4 address")
	}
	return out, nil
}

// ValidateFlags checks mutually exclusive / required combinations before touching the node.
func (o *Options) ValidateFlags() error {
	hasACME := strings.TrimSpace(o.TLSDomain) != ""
	hasOp := strings.TrimSpace(o.TLSCert) != "" || strings.TrimSpace(o.TLSKey) != ""
	if hasOp && (o.TLSCert == "" || o.TLSKey == "") {
		return fmt.Errorf("configure: --tls-cert and --tls-key must be used together")
	}
	if hasACME && hasOp {
		return fmt.Errorf("configure: refuse mixing --tls-domain (ACME) with --tls-cert/--tls-key")
	}
	if o.DNSServers != "" {
		if _, err := ParseDNSServers(o.DNSServers); err != nil {
			return err
		}
	}
	if hasACME && strings.TrimSpace(o.TLSEmail) == "" {
		// Email is strongly recommended but Let's Encrypt allows empty; require for production UX.
		return fmt.Errorf("configure: --tls-email required with --tls-domain")
	}
	return nil
}
