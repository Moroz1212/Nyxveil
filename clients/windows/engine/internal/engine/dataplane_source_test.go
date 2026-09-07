package engine

import (
	"net/netip"
	"testing"
)

// mirrors production server-v1.1.6 ValidateSource contract used on TUN ingress:
// IPv4 only + src must equal assigned client VPN IP.
func serverValidateSourceLike116(pkt []byte, assigned netip.Addr) error {
	ok, reason := shouldForwardTunnelPacket(pkt, assigned)
	if !ok {
		return errString(reason)
	}
	return nil
}

type errString string

func (e errString) Error() string { return string(e) }

func TestDataplaneBidirectionalFilterPath(t *testing.T) {
	assigned := netip.MustParseAddr("10.66.0.21")
	tx := make([]byte, 40)
	tx[0] = 0x45
	tx[9] = 17 // UDP
	a := assigned.As4()
	copy(tx[12:16], a[:])
	copy(tx[16:20], []byte{1, 1, 1, 1})

	if err := serverValidateSourceLike116(tx, assigned); err != nil {
		t.Fatalf("server would reject valid TX: %v", err)
	}
	ok, _ := shouldForwardTunnelPacket(tx, assigned)
	if !ok {
		t.Fatal("client filter must accept")
	}

	// Response toward client (server→client): destination is VPN IP; client RX inject has no anti-spoof.
	rx := make([]byte, 40)
	rx[0] = 0x45
	rx[9] = 17
	copy(rx[12:16], []byte{1, 1, 1, 1})
	copy(rx[16:20], a[:])
	meta := parseTunnelPacketMeta(rx)
	if meta.Dst != assigned {
		t.Fatalf("rx dst=%v", meta.Dst)
	}
}

func TestExpectedVPNIPFromPlanNotStaleString(t *testing.T) {
	p := NewPlan()
	if err := p.ApplyTypeConfig("Nyxveil", "10.66.0.18", 24, "10.66.0.1", []string{"1.1.1.1"}, 1420); err != nil {
		t.Fatal(err)
	}
	old := canonicalVPNIP(p.TunPrefix.Addr())
	if err := p.ApplyTypeConfig("Nyxveil", "10.66.0.21", 24, "10.66.0.1", []string{"1.1.1.1"}, 1420); err != nil {
		t.Fatal(err)
	}
	cur := canonicalVPNIP(p.TunPrefix.Addr())
	if cur.String() != "10.66.0.21" || old.String() != "10.66.0.18" {
		t.Fatalf("old=%s cur=%s", old, cur)
	}
	pkt := make([]byte, 20)
	pkt[0] = 0x45
	ca := cur.As4()
	copy(pkt[12:16], ca[:])
	if ok, _ := shouldForwardTunnelPacket(pkt, cur); !ok {
		t.Fatal("current plan IP must accept")
	}
	if ok, _ := shouldForwardTunnelPacket(pkt, old); ok {
		t.Fatal("stale plan IP must not accept current src")
	}
}

func TestApplyTypeConfigPreservesHostBits(t *testing.T) {
	// Regression for live 1.0.8 dataplane blackhole:
	// Addr.Prefix(24) masks 10.66.0.21 → 10.66.0.0; PrefixFrom keeps the host address.
	masked, err := netip.MustParseAddr("10.66.0.21").Prefix(24)
	if err != nil {
		t.Fatal(err)
	}
	if masked.Addr().String() != "10.66.0.0" {
		t.Fatalf("sanity: Addr.Prefix should mask, got %s", masked.Addr())
	}
	p := NewPlan()
	if err := p.ApplyTypeConfig("Nyxveil", "10.66.0.21", 24, "10.66.0.1", []string{"1.1.1.1"}, 1420); err != nil {
		t.Fatal(err)
	}
	if got := p.TunPrefix.Addr().String(); got != "10.66.0.21" {
		t.Fatalf("TunPrefix.Addr=%s want 10.66.0.21 (host bits must be preserved for netsh + TX filter)", got)
	}
	if p.TunPrefix.Bits() != 24 || p.TunPrefix.String() != "10.66.0.21/24" {
		t.Fatalf("TunPrefix=%s", p.TunPrefix)
	}
}
