package health

import "testing"

func TestComputeHealthy_RequiresGates(t *testing.T) {
	ok := Status{
		Running: true, IdentityPresent: true, CPConnected: true,
		TicketKeysLoaded: true, SkipTUN: true, Accepting: true, TLSOK: true,
	}
	if !ok.ComputeHealthy() {
		t.Fatal("expected healthy")
	}
	bad := ok
	bad.TicketKeysLoaded = false
	if bad.ComputeHealthy() {
		t.Fatal("keys required")
	}
	disabled := ok
	disabled.Accepting = false
	disabled.Enabled = false
	disabled.TLSOK = false
	disabled.QUICOK = false
	// When not accepting, listeners not required.
	if !disabled.ComputeHealthy() {
		t.Fatal("disabled node without listeners should still pass listener gate")
	}
	tun := ok
	tun.SkipTUN = false
	tun.TUNReady = false
	if tun.ComputeHealthy() {
		t.Fatal("TUN required when not skipped")
	}
}
