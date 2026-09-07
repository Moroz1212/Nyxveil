package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
	"github.com/quic-go/quic-go"
)

// ProbeDatagramPayloadCeiling sends an intentionally oversized DATA frame.
// quic-go Connection.SendDatagram rejects locally when payload > maxDataLen and
// returns *quic.DatagramTooLargeError without queueing/sending to the peer
// (quic-go@v0.48.2 connection.SendDatagram).
//
// Returns MaxDatagramPayloadSize. If datagrams are disabled, ok=false.
func ProbeDatagramPayloadCeiling(ctx context.Context, sess *session.Session, conn transport.Conn) (maxPayload int64, ok bool, err error) {
	if sess == nil {
		return 0, false, fmt.Errorf("engine: nil session for DATAGRAM probe")
	}
	if dg, okDG := conn.(transport.DatagramConn); !okDG || !dg.DatagramsEnabled() {
		return 0, false, nil
	}
	// Large enough that wire (36+ip+pad≤64) cannot fit a typical QUIC DATAGRAM.
	probeIP := make([]byte, 4096)
	probeIP[0] = 0x45
	probeIP[1] = 0x00
	err = sess.WritePacket(ctx, probeIP)
	if err == nil {
		// Unexpected: packet fit and was sent. Treat as unknown ceiling.
		return 0, false, fmt.Errorf("engine: DATAGRAM probe unexpectedly succeeded (ceiling unknown)")
	}
	var tooLarge *quic.DatagramTooLargeError
	if !errors.As(err, &tooLarge) || tooLarge == nil || tooLarge.MaxDatagramPayloadSize <= 0 {
		return 0, false, fmt.Errorf("engine: DATAGRAM probe: want DatagramTooLargeError, got %v", err)
	}
	return tooLarge.MaxDatagramPayloadSize, true, nil
}

// AsDatagramTooLarge extracts typed quic-go DATAGRAM size error (recoverable).
func AsDatagramTooLarge(err error) (*quic.DatagramTooLargeError, bool) {
	var tooLarge *quic.DatagramTooLargeError
	if errors.As(err, &tooLarge) && tooLarge != nil {
		return tooLarge, true
	}
	return nil, false
}
