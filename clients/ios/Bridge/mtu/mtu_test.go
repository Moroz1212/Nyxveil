package mtu

import "testing"

func TestEffectiveTunnelMTU_WindowsLesson(t *testing.T) {
	// TypeConfig 1420, QUIC ceiling 1243 → effective 1135 (Windows proven).
	eff, err := EffectiveTunnelMTU(1420, 1243)
	if err != nil {
		t.Fatal(err)
	}
	if eff != 1135 {
		t.Fatalf("effective=%d want 1135", eff)
	}
}

func TestEffectiveTunnelMTU_NoDatagram(t *testing.T) {
	eff, err := EffectiveTunnelMTU(1420, 0)
	if err != nil {
		t.Fatal(err)
	}
	if eff != 1280 {
		t.Fatalf("effective=%d want 1280", eff)
	}
}

func TestNVPOverhead(t *testing.T) {
	if NVPOverheadWorstCase() != NVPFixedWireOverhead+NVPDefaultMaxPadding {
		t.Fatal("overhead mismatch")
	}
}
