package testutil

import (
	"context"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/nyxveil/nvp/core/transport"
)

// ChaosConn wraps a transport connection with network impairment.
type ChaosConn struct {
	inner       transport.Conn
	profile     transport.Profile
	mu          sync.Mutex
	lossRate    float64
	dupRate     float64
	delay       time.Duration
	jitter      time.Duration
	reorderRate float64
	pending     [][]byte
}

// ChaosConfig configures network chaos injection.
type ChaosConfig struct {
	LossRate    float64
	DupRate     float64
	Delay       time.Duration
	Jitter      time.Duration
	ReorderRate float64
}

// WrapChaos wraps transport connection with chaos injection.
func WrapChaos(inner transport.Conn, cfg ChaosConfig) transport.Conn {
	return &ChaosConn{
		inner:       inner,
		profile:     inner.Profile(),
		lossRate:    cfg.LossRate,
		dupRate:     cfg.DupRate,
		delay:       cfg.Delay,
		jitter:      cfg.Jitter,
		reorderRate: cfg.ReorderRate,
	}
}

func (c *ChaosConn) Read(ctx context.Context) ([]byte, error) {
	c.mu.Lock()
	if len(c.pending) > 0 {
		data := c.pending[0]
		c.pending = c.pending[1:]
		c.mu.Unlock()
		return data, nil
	}
	c.mu.Unlock()

	data, err := c.inner.Read(ctx)
	if err != nil {
		return nil, err
	}

	c.applyDelay()
	c.mu.Lock()
	loss := c.lossRate
	dup := c.dupRate
	c.mu.Unlock()
	if rand.Float64() < loss {
		return c.Read(ctx)
	}
	if rand.Float64() < dup {
		c.mu.Lock()
		dupData := make([]byte, len(data))
		copy(dupData, data)
		c.pending = append(c.pending, dupData)
		c.mu.Unlock()
	}
	return data, nil
}

func (c *ChaosConn) Write(ctx context.Context, data []byte) error {
	c.applyDelay()
	c.mu.Lock()
	loss := c.lossRate
	dup := c.dupRate
	c.mu.Unlock()
	if rand.Float64() < loss {
		return nil
	}
	err := c.inner.Write(ctx, data)
	if rand.Float64() < dup {
		_ = c.inner.Write(ctx, data)
	}
	return err
}

// SetLossRate updates packet loss rate (0..1). Safe for tests after handshake.
func (c *ChaosConn) SetLossRate(rate float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lossRate = rate
}

// AsChaos returns the concrete ChaosConn when wrapped via WrapChaos.
func AsChaos(conn transport.Conn) *ChaosConn {
	if c, ok := conn.(*ChaosConn); ok {
		return c
	}
	return nil
}

func (c *ChaosConn) Close() error               { return c.inner.Close() }
func (c *ChaosConn) LocalAddr() net.Addr        { return c.inner.LocalAddr() }
func (c *ChaosConn) RemoteAddr() net.Addr       { return c.inner.RemoteAddr() }
func (c *ChaosConn) Profile() transport.Profile { return c.profile }
func (c *ChaosConn) SetReadDeadline(t time.Time) error {
	return c.inner.SetReadDeadline(t)
}
func (c *ChaosConn) SetWriteDeadline(t time.Time) error {
	return c.inner.SetWriteDeadline(t)
}

type rwConn interface {
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, data []byte) error
}

func (c *ChaosConn) applyDelay() {
	d := c.delay
	if c.jitter > 0 {
		d += time.Duration(rand.Int63n(int64(c.jitter)))
	}
	if d > 0 {
		time.Sleep(d)
	}
}

// MITMProxy simulates a man-in-the-middle for security tests.
type MITMProxy struct {
	InterceptRead  func([]byte) []byte
	InterceptWrite func([]byte) []byte
	inner          rwConn
}

func NewMITM(inner rwConn) *MITMProxy {
	return &MITMProxy{inner: inner}
}

func (m *MITMProxy) Read(ctx context.Context) ([]byte, error) {
	data, err := m.inner.Read(ctx)
	if err != nil {
		return nil, err
	}
	if m.InterceptRead != nil {
		data = m.InterceptRead(data)
	}
	return data, nil
}

func (m *MITMProxy) Write(ctx context.Context, data []byte) error {
	if m.InterceptWrite != nil {
		data = m.InterceptWrite(data)
	}
	return m.inner.Write(ctx, data)
}
