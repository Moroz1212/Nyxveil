package engine

import (
	"net/netip"
	"testing"
)

func TestShouldForwardTunnelPacket(t *testing.T) {
	vpn := netip.MustParseAddr("10.66.0.2")
	ipv6 := []byte{0x60, 0x00, 0x00, 0x00}
	if ok, reason := shouldForwardTunnelPacket(ipv6, vpn); ok || reason != "non_ipv4" {
		t.Fatalf("ipv6: ok=%v reason=%s", ok, reason)
	}
	good := make([]byte, 20)
	good[0] = 0x45
	a4 := vpn.As4()
	copy(good[12:16], a4[:])
	if ok, _ := shouldForwardTunnelPacket(good, vpn); !ok {
		t.Fatal("good ipv4 rejected")
	}
	spoof := append([]byte(nil), good...)
	copy(spoof[12:16], []byte{10, 66, 0, 99})
	if ok, reason := shouldForwardTunnelPacket(spoof, vpn); ok || reason != "spoofed_source" {
		t.Fatalf("spoof: ok=%v reason=%s", ok, reason)
	}
	if ok, reason := shouldForwardTunnelPacket(good, netip.Addr{}); ok || reason != "missing_expected_src" {
		t.Fatalf("empty expected: ok=%v reason=%s", ok, reason)
	}
}

func TestShouldForwardTunnelPacketSequentialSessionVPNIP(t *testing.T) {
	// session #1 then #2 with a new TypeConfig address — filter must track current IP only.
	old := netip.MustParseAddr("10.66.0.18")
	cur := netip.MustParseAddr("10.66.0.21")
	pkt := make([]byte, 20)
	pkt[0] = 0x45
	a := cur.As4()
	copy(pkt[12:16], a[:])
	if ok, _ := shouldForwardTunnelPacket(pkt, cur); !ok {
		t.Fatal("current session VPN IP must accept")
	}
	if ok, reason := shouldForwardTunnelPacket(pkt, old); ok || reason != "spoofed_source" {
		t.Fatalf("stale session VPN IP must drop: ok=%v reason=%s", ok, reason)
	}
	// Packet still sourced from previous session address must drop against current expected.
	stalePkt := append([]byte(nil), pkt...)
	o := old.As4()
	copy(stalePkt[12:16], o[:])
	if ok, reason := shouldForwardTunnelPacket(stalePkt, cur); ok || reason != "spoofed_source" {
		t.Fatalf("old src vs new expected: ok=%v reason=%s", ok, reason)
	}
}

func TestShouldForwardTunnelPacketCanonicalUnmap(t *testing.T) {
	vpn4 := netip.MustParseAddr("10.66.0.21")
	mapped := netip.AddrFrom16([16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 10, 66, 0, 21})
	pkt := make([]byte, 20)
	pkt[0] = 0x45
	a := vpn4.As4()
	copy(pkt[12:16], a[:])
	if ok, _ := shouldForwardTunnelPacket(pkt, mapped); !ok {
		t.Fatal("IPv4-mapped expected must Unmap and accept")
	}
}

func TestParseTunnelPacketMetaIPv4(t *testing.T) {
	pkt := make([]byte, 20)
	pkt[0] = 0x45
	pkt[9] = 6 // TCP
	copy(pkt[12:16], []byte{10, 66, 0, 21})
	copy(pkt[16:20], []byte{1, 1, 1, 1})
	m := parseTunnelPacketMeta(pkt)
	if m.Src.String() != "10.66.0.21" || m.Dst.String() != "1.1.1.1" || m.Protocol != 6 {
		t.Fatalf("meta=%+v", m)
	}
}
