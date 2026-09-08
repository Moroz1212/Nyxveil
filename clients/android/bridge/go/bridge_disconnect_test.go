package nyxveilbridge

import "testing"

type alwaysProtect struct{}

func (alwaysProtect) Protect(fd int) bool { return true }

func TestDisconnectDuringIdleIsIdempotent(t *testing.T) {
	e := NewEngine()
	e.SetProtector(alwaysProtect{})
	e.Disconnect()
	e.Disconnect()
	if e.StatusJSON() == "" {
		t.Fatal("empty status")
	}
}

func TestDisconnectBumpsOpGen(t *testing.T) {
	e := NewEngine()
	e.SetProtector(alwaysProtect{})
	e.mu.Lock()
	before := e.opGen
	e.desiredConnected = true
	e.mu.Unlock()
	e.Disconnect()
	e.mu.Lock()
	after := e.opGen
	want := e.desiredConnected
	e.mu.Unlock()
	if after <= before {
		t.Fatalf("opGen not bumped: before=%d after=%d", before, after)
	}
	if want {
		t.Fatal("desiredConnected should be false after Disconnect")
	}
}
