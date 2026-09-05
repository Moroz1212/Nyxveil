package session

import "testing"

func TestStateTransitions(t *testing.T) {
	s := New(DefaultConfig(true))
	if s.State() != StateNew {
		t.Fatalf("initial state %s", s.State())
	}
	if err := s.transition(StateEstablished); err == nil {
		t.Fatal("expected invalid transition from New to Established")
	}
	if err := s.transition(StateTransportConnected); err != nil {
		t.Fatal(err)
	}
}

func TestCanSendAuth(t *testing.T) {
	cases := []struct {
		state State
		ok    bool
	}{
		{StateAuthenticating, true},
		{StateEstablished, false},
		{StateNew, false},
	}
	for _, c := range cases {
		if c.state.CanSendAuth() != c.ok {
			t.Fatalf("CanSendAuth(%s)=%v want %v", c.state, c.state.CanSendAuth(), c.ok)
		}
	}
}

func TestCanRekey(t *testing.T) {
	if !StateEstablished.CanRekey() {
		t.Fatal("established should allow rekey")
	}
	if StateNew.CanRekey() {
		t.Fatal("new should not allow rekey")
	}
}
