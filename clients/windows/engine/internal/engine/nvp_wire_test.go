package engine

import (
	"errors"
	"fmt"
	"testing"

	"github.com/quic-go/quic-go"
	"golang.org/x/crypto/chacha20poly1305"
)

func TestNVPWireOverheadExact(t *testing.T) {
	if NVPFixedWireOverhead != 36 {
		t.Fatalf("NVPFixedWireOverhead=%d want 36", NVPFixedWireOverhead)
	}
	if chacha20poly1305.Overhead != 16 {
		t.Fatalf("AEAD tag=%d", chacha20poly1305.Overhead)
	}
	if NVPOverheadWorstCase() != 36+64 {
		t.Fatalf("worst overhead=%d", NVPOverheadWorstCase())
	}
	ip := 1420
	wire0 := NVPWireLen(ip, 0)
	wire64 := NVPWireLen(ip, 64)
	if wire0 != 36+ip {
		t.Fatalf("wire0=%d", wire0)
	}
	if wire64 != 36+ip+64 {
		t.Fatalf("wire64=%d", wire64)
	}
}

func TestPacketFitsWithinDatagramCeiling(t *testing.T) {
	// A: IP + NVP overhead <= ceiling → Write would succeed (budget allows).
	ceiling := 1200
	budget, err := ComputeDatagramBudget(ceiling, HTTP3DatagramPrefixMax, NVPDefaultMaxPadding)
	if err != nil {
		t.Fatal(err)
	}
	ip := budget.MaxIPPacket
	worstQuic := HTTP3DatagramPrefixMax + NVPWireLen(ip, NVPDefaultMaxPadding)
	if worstQuic > ceiling {
		t.Fatalf("max IP %d still overflows: %d > %d", ip, worstQuic, ceiling)
	}
	if NVPWireLen(ip+1, NVPDefaultMaxPadding)+HTTP3DatagramPrefixMax <= ceiling {
		t.Fatal("max+1 should not fit under worst-case padding")
	}
}

func TestBoundaryExactMaxPayload(t *testing.T) {
	// B: exact max IP for a known ceiling succeeds in budget math.
	ceiling := 1300
	eff, budget, err := EffectiveTunnelMTU(1420, ceiling)
	if err != nil {
		t.Fatal(err)
	}
	if eff != budget.MaxIPPacket {
		t.Fatalf("eff=%d maxIP=%d", eff, budget.MaxIPPacket)
	}
	if HTTP3DatagramPrefixMax+NVPWireLen(eff, NVPDefaultMaxPadding) > ceiling {
		t.Fatal("boundary IP overflows")
	}
}

func TestOneByteOversizedBudget(t *testing.T) {
	ceiling := 1200
	_, budget, err := EffectiveTunnelMTU(2000, ceiling)
	if err != nil {
		t.Fatal(err)
	}
	ip := budget.MaxIPPacket + 1
	if HTTP3DatagramPrefixMax+NVPWireLen(ip, NVPDefaultMaxPadding) <= ceiling {
		t.Fatal("expected one-byte oversized under worst-case pad")
	}
}

func TestDatagramTooLargeMaxPayloadRecalc(t *testing.T) {
	// D: MaxDatagramPayloadSize drives effective MTU.
	typeConfig := 1420
	maxPayload := int64(1200)
	eff, budget, err := EffectiveTunnelMTU(typeConfig, int(maxPayload))
	if err != nil {
		t.Fatal(err)
	}
	want := 1200 - HTTP3DatagramPrefixMax - NVPFixedWireOverhead - NVPDefaultMaxPadding
	if budget.MaxIPPacket != want || eff != want {
		t.Fatalf("eff=%d maxIP=%d want=%d", eff, budget.MaxIPPacket, want)
	}
}

func TestTypeConfig1420ClampedToTransport(t *testing.T) {
	// E: TypeConfig 1420 but lower transport ceiling → adapter gets effective, not 1420.
	eff, _, err := EffectiveTunnelMTU(1420, 1200)
	if err != nil {
		t.Fatal(err)
	}
	if eff >= 1420 {
		t.Fatalf("effective=%d still unsafe 1420", eff)
	}
	if eff != 1200-HTTP3DatagramPrefixMax-NVPFixedWireOverhead-NVPDefaultMaxPadding {
		t.Fatalf("eff=%d", eff)
	}
	ctrl := newTunnelMTU(1420, eff, "Nyxveil", nil)
	if ctrl.Effective() == 1420 {
		t.Fatal("controller stored unsafe MTU")
	}
	if ctrl.TypeConfig() != 1420 {
		t.Fatal("TypeConfig must remain 1420")
	}
}

func TestHandleDatagramTooLargeShrinksAndContinues(t *testing.T) {
	// C/F: typed DatagramTooLarge → shrink; pump classification continues.
	var applied []int
	ctrl := newTunnelMTU(1420, 1400, "Nyxveil", func(name string, mtu int) error {
		applied = append(applied, mtu)
		return nil
	})
	var icmpCount int
	ctrl.injectICMP = func(pkt []byte) {
		icmpCount++
		if len(pkt) < 28 || pkt[9] != 1 || pkt[20] != 3 || pkt[21] != 4 {
			t.Fatalf("bad ICMP: %x", pkt[:min(32, len(pkt))])
		}
	}
	pkt := make([]byte, 1400)
	pkt[0] = 0x45
	copy(pkt[12:16], []byte{10, 66, 0, 24})
	copy(pkt[16:20], []byte{1, 1, 1, 1})

	dtl := &quic.DatagramTooLargeError{MaxDatagramPayloadSize: 1200}
	if _, ok := AsDatagramTooLarge(fmt.Errorf("wrap: %w", dtl)); !ok {
		t.Fatal("errors.As should unwrap DatagramTooLargeError")
	}
	ctrl.HandleDatagramTooLarge(pkt, dtl)
	newEff := ctrl.Effective()
	want := 1200 - HTTP3DatagramPrefixMax - NVPFixedWireOverhead - NVPDefaultMaxPadding
	if newEff != want {
		t.Fatalf("newEff=%d want=%d", newEff, want)
	}
	if len(applied) != 1 || applied[0] != want {
		t.Fatalf("applied=%v", applied)
	}
	if icmpCount != 1 {
		t.Fatalf("icmp=%d", icmpCount)
	}

	// Subsequent smaller packet would fit; pump must not treat DTL as fatal.
	fatal := false
	recovered := false
	writeErr := error(&quic.DatagramTooLargeError{MaxDatagramPayloadSize: 1200})
	if d, ok := AsDatagramTooLarge(writeErr); ok {
		recovered = true
		ctrl.HandleDatagramTooLarge(pkt[:1000], d)
	} else if writeErr != nil {
		fatal = true
	}
	if !recovered || fatal {
		t.Fatalf("recoverable=%v fatal=%v", recovered, fatal)
	}
}

func TestOrdinaryFatalTransportErrorStillFatal(t *testing.T) {
	// G: non-DatagramTooLarge remains fatal.
	err := errors.New("connection closed")
	if _, ok := AsDatagramTooLarge(err); ok {
		t.Fatal("must not classify ordinary error as DTL")
	}
	fatal := true
	if _, ok := AsDatagramTooLarge(err); ok {
		fatal = false
	}
	if !fatal {
		t.Fatal("ordinary error must stay fatal")
	}
}

func TestPathMTUShrinkThenSubsequentOK(t *testing.T) {
	// F: one packet rejected → MTU lowered → subsequent fits budget.
	ctrl := newTunnelMTU(1420, 1420, "Nyxveil", func(string, int) error { return nil })
	ctrl.HandleDatagramTooLarge(make([]byte, 1420), &quic.DatagramTooLargeError{MaxDatagramPayloadSize: 1250})
	eff := ctrl.Effective()
	if eff >= 1420 {
		t.Fatalf("not shrunk: %d", eff)
	}
	// Packet at new effective size fits worst-case framing under new ceiling.
	worst := HTTP3DatagramPrefixMax + NVPWireLen(eff, NVPDefaultMaxPadding)
	if worst > 1250 {
		t.Fatalf("post-shrink packet still too large: %d > 1250", worst)
	}
	// Pump alive: second write error that is not DTL would be fatal; DTL continues.
	alive := true
	if dtl, ok := AsDatagramTooLarge(&quic.DatagramTooLargeError{MaxDatagramPayloadSize: 1250}); ok {
		ctrl.HandleDatagramTooLarge(make([]byte, eff+1), dtl)
		_ = alive
	} else {
		alive = false
	}
	if !alive {
		t.Fatal("pump should remain alive after DTL")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
