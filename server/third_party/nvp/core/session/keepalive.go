package session

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"github.com/nyxveil/nvp/core/protocol"
)

// KeepaliveDelay returns the next keepalive wait duration: base interval plus
// bounded jitter drawn from KeepaliveRand (or crypto/rand).
func (s *Session) KeepaliveDelay() (time.Duration, error) {
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()

	base := cfg.KeepaliveInterval
	if base <= 0 {
		return 0, fmt.Errorf("keepalive disabled")
	}
	jitterMax := cfg.KeepaliveJitter
	if jitterMax < 0 {
		jitterMax = 0
	}
	if jitterMax == 0 && base > 0 {
		jitterMax = protocol.DefaultKeepaliveJitter
		if jitterMax > base {
			jitterMax = base / 5
		}
	}
	if jitterMax == 0 {
		return base, nil
	}

	r := cfg.KeepaliveRand
	if r == nil {
		r = rand.Reader
	}
	n, err := randIntn(r, int(jitterMax)+1)
	if err != nil {
		return 0, err
	}
	return base + time.Duration(n), nil
}

func randIntn(r io.Reader, n int) (int, error) {
	if n <= 1 {
		return 0, nil
	}
	limit := uint64(n)
	max := (^uint64(0) / limit) * limit
	for {
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, err
		}
		v := binary.BigEndian.Uint64(b[:])
		if v < max {
			return int(v % limit), nil
		}
	}
}

// RunKeepalive sends PINGs on a jittered schedule until ctx is cancelled or send fails.
func (s *Session) RunKeepalive(ctx context.Context) error {
	for {
		delay, err := s.KeepaliveDelay()
		if err != nil {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-s.closedCh:
			timer.Stop()
			return ErrNotEstablished
		case <-timer.C:
		}
		if err := s.SendPing(ctx); err != nil {
			return err
		}
	}
}
