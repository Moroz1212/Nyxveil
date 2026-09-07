package engine

import (
	"context"
	"fmt"

	"github.com/nyxveil/client-windows/internal/diag"
	"github.com/nyxveil/nvp/core/session"
)

// runKeepaliveLogged mirrors Session.RunKeepalive but emits diagnostic events
// without modifying Frozen Core.
func runKeepaliveLogged(ctx context.Context, sess *session.Session) error {
	var seq uint64
	for {
		delay, err := sess.KeepaliveDelay()
		if err != nil {
			diag.Info("KEEPALIVE", "stop", err.Error())
			return err
		}
		timer := newTimer(delay)
		select {
		case <-ctx.Done():
			stopTimer(timer)
			diag.Info("KEEPALIVE", "stop", "context done")
			return ctx.Err()
		case <-timer.C:
		}
		seq++
		diag.InfoFields("KEEPALIVE", "send", map[string]string{"seq": fmt.Sprintf("%d", seq)})
		if err := sess.SendPing(ctx); err != nil {
			diag.Warn("KEEPALIVE", "failure", fmt.Sprintf("seq=%d err=%v", seq, err))
			return err
		}
		// Frozen Core replies with PONG on the peer; we do not observe ACKs here.
		if seq <= 3 || seq%10 == 0 {
			diag.InfoFields("KEEPALIVE", "sent_ok", map[string]string{"seq": fmt.Sprintf("%d", seq)})
		}
	}
}
