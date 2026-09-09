package netcfg

import (
	"encoding/json"
	"fmt"
	"net/netip"
)

// Message matches server TypeConfig JSON (same contract as Windows engine netcfg).
type Message struct {
	VPNIP      string   `json:"vpn_ip"`
	VPNPrefix  int      `json:"vpn_prefix"`
	MTU        int      `json:"mtu"`
	Gateway    string   `json:"gateway"`
	DNSServers []string `json:"dns_servers"`
}

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
		return fmt.Errorf("netcfg: dns_servers required (fail-closed; no public DNS fallback)")
	}
	for _, s := range m.DNSServers {
		a, err := netip.ParseAddr(s)
		if err != nil || !a.Is4() {
			return fmt.Errorf("netcfg: invalid dns_servers entry %q", s)
		}
	}
	return nil
}

// IsNetworkBase reports whether addr is the network base for prefix (host bits zero).
func IsNetworkBase(ipStr string, prefix int) bool {
	ip, err := netip.ParseAddr(ipStr)
	if err != nil || !ip.Is4() || prefix < 0 || prefix > 32 || prefix == 32 {
		return false
	}
	addr := ip.As4()
	v := uint32(addr[0])<<24 | uint32(addr[1])<<16 | uint32(addr[2])<<8 | uint32(addr[3])
	var mask uint32
	if prefix == 0 {
		mask = 0
	} else {
		mask = ^uint32(0) << (32 - prefix)
	}
	return (v &^ mask) == 0
}
