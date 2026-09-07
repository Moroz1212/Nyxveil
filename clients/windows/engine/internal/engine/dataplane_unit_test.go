package engine_test

import (
	"testing"

	"github.com/nyxveil/client-windows/internal/engine"
)

func TestDataplaneGateReadyRequiresAllFlags(t *testing.T) {
	var g engine.DataplaneGate
	if g.Ready() {
		t.Fatal("empty gate must not be ready")
	}
	if g.ErrIncomplete() == nil {
		t.Fatal("expected ErrIncomplete")
	}
	g.TypeConfigOK = true
	g.AdapterOpen = true
	g.AddressApplied = true
	g.RoutesApplied = true
	g.DNSApplied = true
	if g.Ready() {
		t.Fatal("pumps missing → not ready")
	}
	g.PumpsStarted = true
	if !g.Ready() {
		t.Fatal("full gate must be ready")
	}
	if err := g.ErrIncomplete(); err != nil {
		t.Fatal(err)
	}
}

func TestNoopVerifyRefusesUnmarkedApply(t *testing.T) {
	a := &engine.NoopApplier{SkipMarkApplied: true}
	p := engine.NewPlan()
	_ = a.Capture(p)
	_ = a.ApplyBypass(p)
	if err := a.ApplyTunnel(p); err != nil {
		t.Fatal(err)
	}
	if err := a.VerifyTunnel(p); err == nil {
		t.Fatal("VerifyTunnel must fail when Apply skipped marking dataplane")
	}
}
