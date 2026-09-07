package engine

import "net/netip"

// tunnelPacketMeta is safe diagnostic metadata extracted from an IP packet.
type tunnelPacketMeta struct {
	Src      netip.Addr
	Dst      netip.Addr
	Protocol uint8
	Length   int
	Version  int
}

func parseTunnelPacketMeta(pkt []byte) tunnelPacketMeta {
	m := tunnelPacketMeta{Length: len(pkt)}
	if len(pkt) < 1 {
		return m
	}
	m.Version = int(pkt[0] >> 4)
	if m.Version == 4 && len(pkt) >= 20 {
		var src4, dst4 [4]byte
		copy(src4[:], pkt[12:16])
		copy(dst4[:], pkt[16:20])
		m.Src = netip.AddrFrom4(src4)
		m.Dst = netip.AddrFrom4(dst4)
		m.Protocol = pkt[9]
		return m
	}
	if m.Version == 6 && len(pkt) >= 40 {
		var src16, dst16 [16]byte
		copy(src16[:], pkt[8:24])
		copy(dst16[:], pkt[24:40])
		m.Src = netip.AddrFrom16(src16)
		m.Dst = netip.AddrFrom16(dst16)
		m.Protocol = pkt[6]
	}
	return m
}

func canonicalVPNIP(vpnIP netip.Addr) netip.Addr {
	if !vpnIP.IsValid() {
		return netip.Addr{}
	}
	return vpnIP.Unmap()
}

// shouldForwardTunnelPacket filters Wintun→NVP frames before SendData.
//
// Production server-v1.1.6 fails Session.ReadLoop when OnData returns
// ValidateSource errors (non-IPv4 / spoofed source), which closes the transport
// and surfaces as client ReadLoop EOF ~50ms after pumps start. Dropping those
// packets client-side is required for live compatibility until server ≥1.1.7.
//
// expectedVPNIP must be the current session TypeConfig address (canonical IPv4).
func shouldForwardTunnelPacket(pkt []byte, expectedVPNIP netip.Addr) (ok bool, reason string) {
	if len(pkt) < 1 {
		return false, "empty"
	}
	ver := pkt[0] >> 4
	if ver != 4 {
		return false, "non_ipv4"
	}
	if len(pkt) < 20 {
		return false, "ipv4_too_short"
	}
	want := canonicalVPNIP(expectedVPNIP)
	if !want.IsValid() || !want.Is4() {
		// Fail-closed: pumps always run after TypeConfig; missing expected = misconfig.
		return false, "missing_expected_src"
	}
	var src4 [4]byte
	copy(src4[:], pkt[12:16])
	src := netip.AddrFrom4(src4)
	if src != want {
		return false, "spoofed_source"
	}
	return true, ""
}
