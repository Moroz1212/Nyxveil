package session

import (
	"bytes"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/protocol"
)

type fixedReader struct {
	b []byte
}

func (f *fixedReader) Read(p []byte) (int, error) {
	n := copy(p, f.b)
	if n < len(p) {
		// repeat pattern for subsequent reads
		for i := n; i < len(p); i++ {
			p[i] = f.b[i%len(f.b)]
		}
		return len(p), nil
	}
	return n, nil
}

func TestKeepaliveDelayIncludesBoundedJitter(t *testing.T) {
	cfg := DefaultConfig(true)
	cfg.KeepaliveInterval = 100 * time.Millisecond
	cfg.KeepaliveJitter = 50 * time.Millisecond
	// Force jitter = 0 via all-zero rand → delay == base
	cfg.KeepaliveRand = bytes.NewReader(make([]byte, 64))
	s := New(cfg)

	d, err := s.KeepaliveDelay()
	if err != nil {
		t.Fatal(err)
	}
	if d < cfg.KeepaliveInterval || d > cfg.KeepaliveInterval+cfg.KeepaliveJitter {
		t.Fatalf("delay %v out of [%v, %v]", d, cfg.KeepaliveInterval, cfg.KeepaliveInterval+cfg.KeepaliveJitter)
	}
}

func TestKeepaliveDelayInjectableRNG(t *testing.T) {
	cfg := DefaultConfig(true)
	cfg.KeepaliveInterval = time.Second
	cfg.KeepaliveJitter = 100 * time.Millisecond
	// uint64 bytes that decode to a value yielding jitter near max after rejection sampling may vary;
	// use high bytes to get non-zero modulo.
	cfg.KeepaliveRand = &fixedReader{b: []byte{0, 0, 0, 0, 0, 0, 0, 50}}
	s := New(cfg)

	seen := map[time.Duration]bool{}
	for i := 0; i < 5; i++ {
		d, err := s.KeepaliveDelay()
		if err != nil {
			t.Fatal(err)
		}
		seen[d] = true
		if d < time.Second || d > time.Second+100*time.Millisecond {
			t.Fatalf("unexpected delay %v", d)
		}
	}
	if len(seen) < 1 {
		t.Fatal("expected at least one delay")
	}
}

func TestKeepaliveDefaults(t *testing.T) {
	cfg := DefaultConfig(true)
	if cfg.KeepaliveInterval != protocol.DefaultKeepaliveInterval {
		t.Fatalf("interval=%v", cfg.KeepaliveInterval)
	}
	if cfg.KeepaliveJitter != protocol.DefaultKeepaliveJitter {
		t.Fatalf("jitter=%v", cfg.KeepaliveJitter)
	}
}
