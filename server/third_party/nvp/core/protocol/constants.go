package protocol

import "time"

const (
	// MaxFrameSize is the maximum allowed encrypted frame size (64 KiB payload + overhead).
	MaxFrameSize = 65536

	// DefaultMTU is a conservative tunnel MTU default accounting for transport overhead.
	DefaultMTU = 1280

	// MaxHandshakeSize limits pre-auth handshake message size.
	MaxHandshakeSize = 4096

	// DefaultReplayWindow is the number of sequences accepted behind the highest seen.
	DefaultReplayWindow uint64 = 1024

	// DefaultRekeyInterval is the default time-based rekey interval.
	DefaultRekeyInterval = 30 * time.Minute

	// DefaultRekeyPacketCount triggers rekey after this many packets per direction.
	DefaultRekeyPacketCount uint64 = 1_000_000

	// DefaultRekeyByteCount triggers rekey after this many bytes per direction.
	DefaultRekeyByteCount uint64 = 1 << 30 // 1 GiB

	// DefaultTicketTTL is the default access ticket lifetime.
	DefaultTicketTTL = 15 * time.Minute

	// DefaultAuthTimeout is how long OpenSession waits for AUTH_OK after SendAuth.
	DefaultAuthTimeout = 15 * time.Second

	// RekeyOverlapWindow is how long previous-epoch recv keys stay valid after rekey.
	RekeyOverlapWindow = 60 * time.Second

	// RekeyTimeout limits how long a rekey control send may take before the session fails closed.
	RekeyTimeout = 30 * time.Second

	// HandshakeTimeout limits incomplete handshake duration.
	HandshakeTimeout = 30 * time.Second

	// DefaultKeepaliveInterval is the base PING interval when keepalive is enabled.
	DefaultKeepaliveInterval = 25 * time.Second

	// DefaultKeepaliveJitter is the maximum additive jitter applied to keepalive delays.
	DefaultKeepaliveJitter = 5 * time.Second

	// MaxPendingHandshakes limits concurrent unauthenticated handshakes per node.
	MaxPendingHandshakes = 256
)
