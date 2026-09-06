package metrics

import (
	"sync/atomic"
)

// Collector holds operational metrics without secrets.
type Collector struct {
	ActiveSessions    atomic.Int64
	NewSessions       atomic.Int64
	HandshakeFailures atomic.Int64
	AuthFailures      atomic.Int64
	TransportFailures atomic.Int64
	UDPFailures       atomic.Int64
	TCPFallbackCount  atomic.Int64
	BytesRX           atomic.Uint64
	BytesTX           atomic.Uint64
	PacketsRX         atomic.Uint64
	PacketsTX         atomic.Uint64
	ReplayRejected    atomic.Uint64
	AEADFailures      atomic.Uint64
	Rekeys            atomic.Uint64
}

// Global default metrics collector.
var Default = &Collector{}

func (c *Collector) Snapshot() map[string]uint64 {
	return map[string]uint64{
		"active_sessions":    uint64(c.ActiveSessions.Load()),
		"new_sessions":       uint64(c.NewSessions.Load()),
		"handshake_failures": uint64(c.HandshakeFailures.Load()),
		"auth_failures":      uint64(c.AuthFailures.Load()),
		"transport_failures": uint64(c.TransportFailures.Load()),
		"udp_failures":       uint64(c.UDPFailures.Load()),
		"tcp_fallback_count": uint64(c.TCPFallbackCount.Load()),
		"bytes_rx":           c.BytesRX.Load(),
		"bytes_tx":           c.BytesTX.Load(),
		"packets_rx":         c.PacketsRX.Load(),
		"packets_tx":         c.PacketsTX.Load(),
		"replay_rejected":    c.ReplayRejected.Load(),
		"aead_failures":      c.AEADFailures.Load(),
		"rekeys":             c.Rekeys.Load(),
	}
}
