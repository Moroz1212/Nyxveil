// Package netcfg encodes the VPN network configuration pushed to clients
// via NVP control TypeConfig (0x04) after AUTH succeeds and before ReadLoop.
//
// Delivery uses an explicit go:linkname shim to Frozen Core Session.sendControl
// (see THIRD_PARTY_CORE.md). That is not a public Core API; builds assert the
// exact Frozen Core SHA before packaging.
package netcfg

import (
	"encoding/json"
	"fmt"
	"net/netip"
)

// Message is the JSON payload for control.TypeConfig.
//
// Contract (see docs/NETWORKING.md):
//
//	{"vpn_ip":"10.66.0.2","vpn_prefix":24,"mtu":1420,"gateway":"10.66.0.1","dns_servers":["203.0.113.53"]}
//
// dns_servers is required (≥1 IPv4). Values are operator-configured only —
// never inferred from the TUN gateway and never hardcoded public resolvers.
type Message struct {
	VPNIP      string   `json:"vpn_ip"`
	VPNPrefix  int      `json:"vpn_prefix"`
	MTU        int      `json:"mtu"`
	Gateway    string   `json:"gateway"`
	DNSServers []string `json:"dns_servers"`
}

// Encode marshals a Message to JSON bytes for TypeConfig.
func Encode(m Message) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

// Decode unmarshals TypeConfig JSON.
func Decode(b []byte) (*Message, error) {
	var m Message
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate checks required fields and IP shapes.
func (m Message) Validate() error {
	ip, err := netip.ParseAddr(m.VPNIP)
	if err != nil || !ip.Is4() {
		return fmt.Errorf("netcfg: invalid vpn_ip %q", m.VPNIP)
	}
	if m.VPNPrefix < 0 || m.VPNPrefix > 32 {
		return fmt.Errorf("netcfg: invalid vpn_prefix %d", m.VPNPrefix)
	}
	if m.MTU <= 0 {
		return fmt.Errorf("netcfg: invalid mtu %d", m.MTU)
	}
	gw, err := netip.ParseAddr(m.Gateway)
	if err != nil || !gw.Is4() {
		return fmt.Errorf("netcfg: invalid gateway %q", m.Gateway)
	}
	if len(m.DNSServers) == 0 {
		return fmt.Errorf("netcfg: dns_servers required (operator-configured; fail-closed)")
	}
	for _, s := range m.DNSServers {
		a, err := netip.ParseAddr(s)
		if err != nil || !a.Is4() {
			return fmt.Errorf("netcfg: invalid dns_servers entry %q", s)
		}
	}
	return nil
}

// FromAllocation builds a Message for a client VPN IP within a subnet CIDR.
// dnsServers must be a non-empty operator list of IPv4 resolvers.
func FromAllocation(clientIP netip.Addr, subnetCIDR string, mtu int, gateway netip.Addr, dnsServers []string) (Message, error) {
	prefix, err := netip.ParsePrefix(subnetCIDR)
	if err != nil {
		return Message{}, err
	}
	prefix = prefix.Masked()
	if mtu <= 0 {
		mtu = 1420
	}
	if !gateway.IsValid() {
		gateway = prefix.Addr().Next()
	}
	dns := append([]string(nil), dnsServers...)
	m := Message{
		VPNIP:      clientIP.String(),
		VPNPrefix:  prefix.Bits(),
		MTU:        mtu,
		Gateway:    gateway.String(),
		DNSServers: dns,
	}
	if err := m.Validate(); err != nil {
		return Message{}, err
	}
	return m, nil
}
