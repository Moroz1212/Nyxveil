// Package datapath bridges NVP sessions and the TUN interface.
package datapath

import (
	"context"
	"errors"
	"log"
	"sync"

	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/server/internal/sessions"
)

const defaultQueue = 64

// PacketWriter is the TUN (or test double) write side.
type PacketWriter interface {
	Write(p []byte) (int, error)
}

// PacketReader is the TUN (or test double) read side.
type PacketReader interface {
	Read(p []byte) (int, error)
}

// Device is a bidirectional TUN-like device.
type Device interface {
	PacketReader
	PacketWriter
}

// Bridge moves packets between sessions and TUN with bounded queues.
type Bridge struct {
	sessions *sessions.Manager
	dev      Device

	toTun   chan []byte
	queueSz int

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	running bool
}

// New creates a bridge. queueSize defaults to 64.
func New(mgr *sessions.Manager, dev Device, queueSize int) *Bridge {
	if queueSize <= 0 {
		queueSize = defaultQueue
	}
	return &Bridge{
		sessions: mgr,
		dev:      dev,
		queueSz:  queueSize,
	}
}

// Start begins TUN→session pump. Session→TUN uses AttachSession OnData callbacks.
func (b *Bridge) Start(parent context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		return nil
	}
	if b.dev == nil {
		return errors.New("datapath: nil device")
	}
	b.ctx, b.cancel = context.WithCancel(parent)
	b.toTun = make(chan []byte, b.queueSz)
	b.running = true
	b.wg.Add(2)
	go b.tunWriter()
	go b.tunReader()
	return nil
}

// Stop cancels pumps and waits for goroutines.
func (b *Bridge) Stop() {
	b.mu.Lock()
	if !b.running {
		b.mu.Unlock()
		return
	}
	b.running = false
	cancel := b.cancel
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	b.wg.Wait()
}

// AttachSession wires OnData: validate source IP, then enqueue to TUN.
func (b *Bridge) AttachSession(sess *session.Session) {
	sess.OnData(func(pkt []byte) error {
		if err := b.sessions.ValidateSource(sess, pkt); err != nil {
			return err
		}
		b.mu.Lock()
		ch := b.toTun
		running := b.running
		b.mu.Unlock()
		if !running || ch == nil {
			return errors.New("datapath: bridge not running")
		}
		cp := append([]byte(nil), pkt...)
		select {
		case ch <- cp:
			return nil
		default:
			// Bounded queue full — drop.
			return nil
		}
	})
}

func (b *Bridge) tunWriter() {
	defer b.wg.Done()
	for {
		select {
		case <-b.ctx.Done():
			return
		case pkt, ok := <-b.toTun:
			if !ok {
				return
			}
			if _, err := b.dev.Write(pkt); err != nil {
				log.Printf("datapath: tun write: %v", err)
			}
		}
	}
}

func (b *Bridge) tunReader() {
	defer b.wg.Done()
	buf := make([]byte, 65535)
	for {
		select {
		case <-b.ctx.Done():
			return
		default:
		}
		n, err := b.dev.Read(buf)
		if err != nil {
			select {
			case <-b.ctx.Done():
				return
			default:
				log.Printf("datapath: tun read: %v", err)
				return
			}
		}
		if n <= 0 {
			continue
		}
		pkt := append([]byte(nil), buf[:n]...)
		dst, err := sessions.DestIP(pkt)
		if err != nil {
			continue
		}
		rec, ok := b.sessions.LookupByIP(dst)
		if !ok || rec.Session == nil {
			continue
		}
		if err := rec.Session.WritePacket(b.ctx, pkt); err != nil {
			log.Printf("datapath: session write: %v", err)
		}
	}
}
