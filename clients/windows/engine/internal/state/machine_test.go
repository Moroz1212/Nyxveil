package state_test

import (
	"testing"

	"github.com/nyxveil/client-windows/internal/state"
)

func TestMachineSetAndFail(t *testing.T) {
	m := state.New()
	st, errMsg := m.Get()
	if st != state.Disconnected || errMsg != "" {
		t.Fatalf("%v %q", st, errMsg)
	}
	m.Set(state.ConnectingTransport)
	st, _ = m.Get()
	if st != state.ConnectingTransport {
		t.Fatal(st)
	}
	m.Fail("boom")
	st, errMsg = m.Get()
	if st != state.Error || errMsg != "boom" {
		t.Fatalf("%v %q", st, errMsg)
	}
	m.Set(state.Disconnected)
	st, errMsg = m.Get()
	if st != state.Disconnected || errMsg != "boom" {
		t.Fatalf("LastError must survive Disconnected: %v %q", st, errMsg)
	}
	m.Set(state.Connected)
	st, errMsg = m.Get()
	if st != state.Connected || errMsg != "" {
		t.Fatalf("LastError clears on Connected: %v %q", st, errMsg)
	}
}

func TestStateString(t *testing.T) {
	if state.Connected.String() != "Connected" {
		t.Fatal(state.Connected.String())
	}
}
