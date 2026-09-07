package engine

// Route / DNS apply order (full-tunnel Windows client):
//
//  1. Capture — snapshot existing default route(s) and interface DNS.
//  2. Bypass — host routes for Control Plane + selected node endpoint IPs,
//     installed BEFORE any VPN dial so CP/ticket refresh and the TLS/QUIC
//     transport do not hairpin into a half-configured tunnel.
//  3. Dial + AUTH — Frozen Connector path (catalog re-verify, RequirePin).
//  4. TypeConfig — wait OnControl(TypeConfig); fail-closed if dns_servers empty.
//     No route or TUN changes until this step succeeds.
//  5. TUN — open Wintun, assign vpn_ip/prefix/MTU, set DNS from TypeConfig.
//  6. Default route — point 0.0.0.0/0 via TUN gateway (after DNS on TUN).
//  7. Restore on disconnect — reverse of apply: remove default, close TUN,
//     remove bypass, restore prior DNS/default routes.
//
// This file defines capture/restore plan structures. OS route/DNS mutation
// is implemented behind RouteApplier (Windows build); stubs return nil until
// Wintun bind lands.

import (
	"fmt"
	"net/netip"
)

// HostRoute is a /32 (or host) bypass toward the physical gateway.
type HostRoute struct {
	Destination netip.Prefix
	NextHop     netip.Addr
	InterfaceIndex int
	Metric      int
}

// DefaultRouteSnapshot is the pre-VPN IPv4 default route.
type DefaultRouteSnapshot struct {
	NextHop        netip.Addr
	InterfaceIndex int
	InterfaceLUID  uint64
	Metric         int
	Present        bool
}

// DNSSnapshot is per-interface DNS before VPN.
type DNSSnapshot struct {
	InterfaceIndex int
	Servers        []netip.Addr
}

// IPv6Capture is exact prior ms_tcpip6 enablement for one egress interface.
type IPv6Capture struct {
	InterfaceIndex uint32
	Enabled        bool
}

// Plan holds capture + intended apply state for one connect cycle.
type Plan struct {
	CapturedDefault     DefaultRouteSnapshot
	CapturedDNS         []DNSSnapshot
	CapturedIPv6Phys    *bool  // legacy: first physical IF (compat)
	CapturedIPv6IfIndex uint32 // legacy
	CapturedIPv6        []IPv6Capture

	BypassHosts []HostRoute

	TunName    string
	TunPrefix  netip.Prefix
	TunGateway netip.Addr
	TunDNS     []netip.Addr
	TunMTU     int
	TunIfIndex uint32
	TunLUID    uint64

	DefaultViaTUN bool
	Gate          DataplaneGate
}

// NewPlan returns an empty plan.
func NewPlan() *Plan { return &Plan{} }

// SetBypassHosts records CP / node endpoint host routes (step 2).
func (p *Plan) SetBypassHosts(hosts []HostRoute) {
	p.BypassHosts = append([]HostRoute(nil), hosts...)
}

// ApplyTypeConfig fills TUN addressing from validated netcfg (step 4→5).
func (p *Plan) ApplyTypeConfig(tunName, vpnIP string, prefixLen int, gateway string, dns []string, mtu int) error {
	ip, err := netip.ParseAddr(vpnIP)
	if err != nil || !ip.Is4() {
		return fmt.Errorf("routeplan: vpn_ip: %w", err)
	}
	if prefixLen < 0 || prefixLen > 32 {
		return fmt.Errorf("routeplan: vpn_prefix %d", prefixLen)
	}
	// MUST use PrefixFrom — Addr.Prefix() masks host bits (10.66.0.21/24 → 10.66.0.0),
	// which made netsh assign the network address while the TX filter expected the host IP
	// (live 1.0.8: every packet dropped as spoofed_source).
	pfx := netip.PrefixFrom(ip, prefixLen)
	if !pfx.IsValid() {
		return fmt.Errorf("routeplan: vpn_prefix invalid")
	}
	gw, err := netip.ParseAddr(gateway)
	if err != nil || !gw.Is4() {
		return fmt.Errorf("routeplan: gateway: %w", err)
	}
	if len(dns) == 0 {
		return fmt.Errorf("routeplan: dns_servers required")
	}
	out := make([]netip.Addr, 0, len(dns))
	for _, s := range dns {
		a, err := netip.ParseAddr(s)
		if err != nil || !a.Is4() {
			return fmt.Errorf("routeplan: dns %q: %w", s, err)
		}
		out = append(out, a)
	}
	p.TunName = tunName
	p.TunPrefix = pfx
	p.TunGateway = gw
	p.TunDNS = out
	p.TunMTU = mtu
	return nil
}

// RouteApplier mutates the OS routing table / DNS. Implementations are OS-specific.
type RouteApplier interface {
	Capture(p *Plan) error
	ApplyBypass(p *Plan) error
	ApplyTunnel(p *Plan) error
	// VerifyTunnel fail-closed checks that ApplyTunnel actually left a usable dataplane.
	// Connected must not be set if this returns an error.
	VerifyTunnel(p *Plan) error
	Restore(p *Plan) error
}

// NoopApplier records order without touching the OS (unit tests).
// ApplyTunnel marks the plan applied; VerifyTunnel refuses Connected unless that happened.
type NoopApplier struct {
	Steps []string
	// SkipMarkApplied reproduces the pre-1.0.2 hole: ApplyTunnel returns nil
	// without marking dataplane ready (Connected must still be refused).
	SkipMarkApplied bool
}

func (n *NoopApplier) Capture(p *Plan) error {
	n.Steps = append(n.Steps, "capture")
	return nil
}
func (n *NoopApplier) ApplyBypass(p *Plan) error {
	n.Steps = append(n.Steps, "bypass")
	return nil
}
func (n *NoopApplier) ApplyTunnel(p *Plan) error {
	n.Steps = append(n.Steps, "tunnel")
	if n.SkipMarkApplied {
		return nil
	}
	p.DefaultViaTUN = true
	p.Gate.AddressApplied = true
	p.Gate.RoutesApplied = true
	p.Gate.DNSApplied = true
	return nil
}
func (n *NoopApplier) VerifyTunnel(p *Plan) error {
	n.Steps = append(n.Steps, "verify")
	if !p.DefaultViaTUN || !p.Gate.AddressApplied || !p.Gate.RoutesApplied || !p.Gate.DNSApplied {
		return fmt.Errorf("routes: verify: network apply absent (adapter/routes/dns not confirmed)")
	}
	return nil
}
func (n *NoopApplier) Restore(p *Plan) error {
	n.Steps = append(n.Steps, "restore")
	p.DefaultViaTUN = false
	p.Gate = DataplaneGate{}
	return nil
}
