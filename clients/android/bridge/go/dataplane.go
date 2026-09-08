package nyxveilbridge

import (
	"context"
	"errors"
	"log"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/nyxveil/nvp/core/session"
	"github.com/quic-go/quic-go"
)

// dataplaneCounters are exported via StatusJSON (no packet payloads).
type dataplaneCounters struct {
	tunReadPackets  atomic.Uint64
	tunReadBytes    atomic.Uint64
	tunReadErrors   atomic.Uint64
	tunReadEagain   atomic.Uint64
	nvpTxPackets    atomic.Uint64
	nvpTxBytes      atomic.Uint64
	nvpTxErrors     atomic.Uint64
	txDatagramLarge atomic.Uint64
	nvpRxPackets    atomic.Uint64
	nvpRxBytes      atomic.Uint64
	nvpRxErrors     atomic.Uint64
	tunWritePackets atomic.Uint64
	tunWriteBytes   atomic.Uint64
	tunWriteErrors  atomic.Uint64
	txDropNonIPv4   atomic.Uint64
	txDropSpoof     atomic.Uint64

	firstTunRead  atomic.Bool
	firstNvpTx    atomic.Bool
	firstNvpRx    atomic.Bool
	firstTunWrite atomic.Bool

	txPumpAlive atomic.Bool
	rxPathAlive atomic.Bool
}

func (e *Engine) resetDataplaneCounters() {
	e.dp = dataplaneCounters{}
	e.txBytes.Store(0)
	e.rxBytes.Store(0)
	e.txTooLarge.Store(0)
}

func (e *Engine) startDataplane(sess *session.Session, dev *tunFile, vpnIP string) {
	expected, _ := netip.ParseAddr(vpnIP)
	expected = canonicalVPNIP(expected)

	e.dp.txPumpAlive.Store(true)
	e.dp.rxPathAlive.Store(true)

	var dataHandler atomic.Value
	dataHandler.Store(func([]byte) error { return nil })
	sess.OnData(func(pkt []byte) error {
		h, _ := dataHandler.Load().(func([]byte) error)
		if h == nil {
			return nil
		}
		return h(pkt)
	})

	dataHandler.Store(func(pkt []byte) error {
		e.dp.nvpRxPackets.Add(1)
		e.dp.nvpRxBytes.Add(uint64(len(pkt)))
		e.rxBytes.Add(uint64(len(pkt)))
		if e.dp.firstNvpRx.CompareAndSwap(false, true) {
			meta := parseTunnelPacketMeta(pkt)
			log.Printf("DATAPLANE nvp_rx first_packet bytes=%d ip_version=%d", meta.Length, meta.Version)
		}
		n, err := dev.Write(pkt)
		if err != nil {
			e.dp.nvpRxErrors.Add(1)
			e.dp.tunWriteErrors.Add(1)
			// Do not return error to ReadLoop for temporary again (handled in Write).
			// Fatal write errors still surface — but drop packet rather than tear session
			// for short writes that already partially succeeded.
			log.Printf("DATAPLANE tun_write error=%v", err)
			return nil
		}
		e.dp.tunWritePackets.Add(1)
		e.dp.tunWriteBytes.Add(uint64(n))
		if e.dp.firstTunWrite.CompareAndSwap(false, true) {
			log.Printf("DATAPLANE tun_write first_packet bytes=%d", n)
		}
		return nil
	})

	log.Printf("DATAPLANE TUN reader started blocking=true")
	log.Printf("DATAPLANE TUN writer started")
	go e.txPump(sess, dev, expected)
}

func (e *Engine) txPump(sess *session.Session, dev *tunFile, expectedVPNIP netip.Addr) {
	defer func() {
		e.dp.txPumpAlive.Store(false)
		log.Printf("DATAPLANE tun_read pump exit")
	}()
	buf := make([]byte, 65535)
	for {
		n, err := dev.Read(buf)
		if err != nil {
			if isAgain(err) {
				e.dp.tunReadEagain.Add(1)
				time.Sleep(2 * time.Millisecond)
				continue
			}
			if isTunClosed(err) {
				return
			}
			e.dp.tunReadErrors.Add(1)
			e.mu.Lock()
			want := e.desiredConnected
			e.lastErr = "tun_read: " + err.Error()
			if want {
				e.dataplaneReady = false
			}
			e.mu.Unlock()
			log.Printf("DATAPLANE tun_read fatal=%v", err)
			return
		}
		if n <= 0 {
			continue
		}
		e.dp.tunReadPackets.Add(1)
		e.dp.tunReadBytes.Add(uint64(n))
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		meta := parseTunnelPacketMeta(pkt)
		if e.dp.firstTunRead.CompareAndSwap(false, true) {
			log.Printf("DATAPLANE tun_read first_packet bytes=%d ip_version=%d", meta.Length, meta.Version)
		}

		e.mu.Lock()
		want := e.desiredConnected
		e.mu.Unlock()
		if !want {
			return
		}

		if ok, reason := shouldForwardTunnelPacket(pkt, expectedVPNIP); !ok {
			switch reason {
			case "non_ipv4":
				e.dp.txDropNonIPv4.Add(1)
			case "spoofed_source":
				e.dp.txDropSpoof.Add(1)
			}
			continue
		}

		ctx := context.Background()
		if err := sess.WritePacket(ctx, pkt); err != nil {
			var tooLarge *quic.DatagramTooLargeError
			if errors.As(err, &tooLarge) {
				e.dp.txDatagramLarge.Add(1)
				e.txTooLarge.Add(1)
				continue
			}
			if errors.Is(err, context.Canceled) {
				return
			}
			e.dp.nvpTxErrors.Add(1)
			e.mu.Lock()
			e.lastErr = "nvp_tx: " + err.Error()
			e.dataplaneReady = false
			e.mu.Unlock()
			log.Printf("DATAPLANE nvp_tx fatal=%v", err)
			return
		}
		e.dp.nvpTxPackets.Add(1)
		e.dp.nvpTxBytes.Add(uint64(n))
		e.txBytes.Add(uint64(n))
		if e.dp.firstNvpTx.CompareAndSwap(false, true) {
			log.Printf("DATAPLANE nvp_tx first_packet")
		}
	}
}

func (c *dataplaneCounters) snapshotMap() map[string]any {
	idle := c.tunReadPackets.Load() == 0 && c.nvpTxPackets.Load() == 0
	return map[string]any{
		"tun_read_packets":      c.tunReadPackets.Load(),
		"tun_read_bytes":        c.tunReadBytes.Load(),
		"tun_read_errors":       c.tunReadErrors.Load(),
		"tun_read_eagain":       c.tunReadEagain.Load(),
		"nvp_tx_packets":        c.nvpTxPackets.Load(),
		"nvp_tx_bytes":          c.nvpTxBytes.Load(),
		"nvp_tx_errors":         c.nvpTxErrors.Load(),
		"tx_datagram_too_large": c.txDatagramLarge.Load(),
		"nvp_rx_packets":        c.nvpRxPackets.Load(),
		"nvp_rx_bytes":          c.nvpRxBytes.Load(),
		"nvp_rx_errors":         c.nvpRxErrors.Load(),
		"tun_write_packets":     c.tunWritePackets.Load(),
		"tun_write_bytes":       c.tunWriteBytes.Load(),
		"tun_write_errors":      c.tunWriteErrors.Load(),
		"tx_drop_non_ipv4":      c.txDropNonIPv4.Load(),
		"tx_drop_spoof":         c.txDropSpoof.Load(),
		"first_tun_read":        c.firstTunRead.Load(),
		"first_nvp_tx":          c.firstNvpTx.Load(),
		"first_nvp_rx":          c.firstNvpRx.Load(),
		"first_tun_write":       c.firstTunWrite.Load(),
		"tx_pump_alive":         c.txPumpAlive.Load(),
		"rx_path_alive":         c.rxPathAlive.Load(),
		"traffic_idle":          idle,
	}
}
