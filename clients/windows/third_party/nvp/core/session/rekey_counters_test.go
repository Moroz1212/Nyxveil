package session

import (
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/keys"
	"github.com/nyxveil/nvp/core/packet"
)

func TestApplyKeysResetsSendCountersAndExpiresPrevEpoch(t *testing.T) {
	s := New(DefaultConfig(true))
	sk1, err := keys.DeriveSessionKeys([32]byte{1}, []byte("transcript"), 1)
	if err != nil {
		t.Fatal(err)
	}
	sk2, err := keys.DeriveSessionKeys([32]byte{2}, []byte("transcript"), 2)
	if err != nil {
		t.Fatal(err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.applyKeysLocked(sk1); err != nil {
		t.Fatal(err)
	}
	s.sendPackets = 100
	s.sendBytes = 9999
	if err := s.applyKeysLocked(sk2); err != nil {
		t.Fatal(err)
	}
	if s.sendPackets != 0 || s.sendBytes != 0 {
		t.Fatalf("counters not reset: packets=%d bytes=%d", s.sendPackets, s.sendBytes)
	}
	if s.prevRecvAEAD == nil || s.prevEpoch != 1 {
		t.Fatal("expected previous-epoch recv keys")
	}
	s.prevDeadline = time.Now().Add(-time.Second)
	if _, _, err := s.recvContextLocked(1); err == nil {
		t.Fatal("expected previous epoch to expire after overlap window")
	}
}

func TestPostRekeyReplayRejected(t *testing.T) {
	s := New(DefaultConfig(false)) // server receives client-to-server
	sk1, err := keys.DeriveSessionKeys([32]byte{1}, []byte("transcript"), 1)
	if err != nil {
		t.Fatal(err)
	}
	sk2, err := keys.DeriveSessionKeys([32]byte{2}, []byte("transcript"), 2)
	if err != nil {
		t.Fatal(err)
	}

	clientAEAD, err := keys.NewClientAEAD(sk1.ClientToServer)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := packet.EncodeInner(0x10, []byte("old-epoch"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := clientAEAD.Seal(1, 7, inner)
	if err != nil {
		t.Fatal(err)
	}

	s.mu.Lock()
	if err := s.applyKeysLocked(sk1); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	if err := s.applyKeysLocked(sk2); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.prevDeadline = time.Now().Add(-time.Second)
	s.state = StateEstablished
	s.mu.Unlock()

	if err := s.processRecord(1, 7, ct); err == nil {
		t.Fatal("post-rekey previous-epoch ciphertext must be rejected")
	}

	// Same sequence in the new epoch after keys rotated must also reject replay once marked.
	client2, err := keys.NewClientAEAD(sk2.ClientToServer)
	if err != nil {
		t.Fatal(err)
	}
	inner2, _ := packet.EncodeInner(0x10, []byte("new"), nil)
	ct2, _ := client2.Seal(2, 1, inner2)
	if err := s.processRecord(2, 1, ct2); err != nil {
		t.Fatalf("first post-rekey record: %v", err)
	}
	if err := s.processRecord(2, 1, ct2); err == nil {
		t.Fatal("replay after rekey should be rejected")
	}
}
