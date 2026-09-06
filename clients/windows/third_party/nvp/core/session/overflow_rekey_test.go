package session

import (
	"bytes"
	"context"
	"math"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/keys"
	"github.com/nyxveil/nvp/core/transport/memory"
)

func TestSequenceOverflowGuard(t *testing.T) {
	s := New(DefaultConfig(true))
	sk, err := keys.DeriveSessionKeys([32]byte{1}, []byte("transcript"), 1)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	if err := s.applyKeysLocked(sk); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.state = StateEstablished
	s.sendSeq = seqExhaustThreshold
	before := s.sendSeq
	s.mu.Unlock()

	err = s.SendData(context.Background(), []byte("x"))
	if err != ErrSequenceExhausted {
		t.Fatalf("expected ErrSequenceExhausted, got %v", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sendSeq != before {
		t.Fatalf("sendSeq wrapped or changed: before=%d after=%d", before, s.sendSeq)
	}
	if s.sendSeq == 0 && before != 0 {
		t.Fatal("sendSeq wrapped to 0")
	}
	if s.state != StateClosed && s.state != StateClosing {
		t.Fatalf("expected fail-close, state=%s", s.state)
	}
}

func TestEpochOverflowGuard(t *testing.T) {
	s := New(DefaultConfig(true))
	sk, err := keys.DeriveSessionKeys([32]byte{1}, []byte("transcript"), math.MaxUint32)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.applyKeysLocked(sk); err != nil {
		t.Fatal(err)
	}
	s.state = StateEstablished
	s.epoch = math.MaxUint32

	payload, err := s.prepareRekeyLocked()
	if err != ErrEpochExhausted {
		t.Fatalf("expected ErrEpochExhausted, got %v payload=%v", err, payload)
	}
	if s.epoch != math.MaxUint32 {
		t.Fatalf("epoch wrapped: %d", s.epoch)
	}
	if s.state != StateClosed && s.state != StateClosing {
		t.Fatalf("expected fail-close on epoch exhaust, state=%s", s.state)
	}
}

func TestRekeyAckTimeout(t *testing.T) {
	clientConn, serverConn := memory.Pair()
	_ = serverConn

	cfg := DefaultConfig(true)
	cfg.RekeyAckTimeout = 40 * time.Millisecond
	s := New(cfg)
	sk, err := keys.DeriveSessionKeys([32]byte{3}, []byte("t"), 1)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := s.Connect(ctx, clientConn); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	if err := s.applyKeysLocked(sk); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.state = StateEstablished
	s.transcript = []byte("t")
	payload, err := s.prepareRekeyLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) == 0 {
		t.Fatal("expected rekey payload")
	}
	if s.State() != StateRekeying {
		t.Fatalf("state=%s", s.State())
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		st := s.State()
		if st == StateClosed || st == StateClosing {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session remained in %s after rekey ACK timeout", s.State())
}

func TestRekeyFailureClosesOrRecoversSession(t *testing.T) {
	cfg := DefaultConfig(true)
	cfg.RekeyAckTimeout = 30 * time.Millisecond
	s := New(cfg)
	sk, err := keys.DeriveSessionKeys([32]byte{4}, []byte("t"), 1)
	if err != nil {
		t.Fatal(err)
	}
	clientConn, _ := memory.Pair()
	ctx := context.Background()
	_ = s.Connect(ctx, clientConn)
	s.mu.Lock()
	_ = s.applyKeysLocked(sk)
	s.state = StateEstablished
	s.transcript = []byte("t")
	_, err = s.prepareRekeyLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		st := s.State()
		// Fail-close after retry, or recover to Established if ACK somehow arrives.
		if st == StateClosed || st == StateClosing || st == StateEstablished {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("rekey failure left session stuck in %s", s.State())
}

func TestRekeyStateCannotRemainForever(t *testing.T) {
	cfg := DefaultConfig(false)
	cfg.RekeyAckTimeout = 25 * time.Millisecond
	s := New(cfg)
	sk, err := keys.DeriveSessionKeys([32]byte{5}, []byte("t"), 2)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := memory.Pair()
	_ = s.Connect(context.Background(), c)
	s.mu.Lock()
	_ = s.applyKeysLocked(sk)
	s.state = StateEstablished
	s.transcript = []byte("t")
	start := time.Now()
	_, _ = s.prepareRekeyLocked()
	s.mu.Unlock()

	deadline := time.Now().Add(350 * time.Millisecond)
	for time.Now().Before(deadline) {
		if s.State() != StateRekeying {
			if time.Since(start) > 2*cfg.RekeyAckTimeout+100*time.Millisecond {
				// left REKEYING after bounded wait — ok
			}
			if s.State() == StateRekeying {
				t.Fatal("still rekeying")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("REKEYING persisted beyond ACK timeout + retry budget")
}

func TestSecretWipeOnClose(t *testing.T) {
	s := New(DefaultConfig(true))
	sk, err := keys.DeriveSessionKeys([32]byte{9, 9, 9}, []byte("wipe"), 1)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	_ = s.applyKeysLocked(sk)
	copy(s.sharedSecret[:], bytes.Repeat([]byte{0xAB}, 32))
	s.state = StateEstablished
	s.mu.Unlock()

	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.sharedSecret {
		if b != 0 {
			t.Fatal("sharedSecret not zeroized on Close")
		}
	}
	if s.sessionKeys != nil {
		t.Fatal("sessionKeys should be cleared")
	}
}
