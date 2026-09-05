package memory

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	"github.com/nyxveil/nvp/core/transport"
)

// Pair creates two connected in-memory transport conns for testing.
func Pair() (client transport.Conn, server transport.Conn) {
	c1, c2 := net.Pipe()
	return &conn{profile: transport.ProfileTLSTCP, rw: c1}, &conn{profile: transport.ProfileTLSTCP, rw: c2}
}

// Compile-time check: memory conn satisfies transport.Conn.
var _ transport.Conn = (*conn)(nil)

type conn struct {
	profile transport.Profile
	rw      net.Conn
	mu      sync.Mutex
	closed  bool
}

func (c *conn) Read(ctx context.Context) ([]byte, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, io.EOF
	}
	c.mu.Unlock()

	if deadline, ok := ctx.Deadline(); ok {
		_ = c.rw.SetReadDeadline(deadline)
		defer c.rw.SetReadDeadline(time.Time{})
	}

	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		buf := make([]byte, 65536)
		n, err := c.rw.Read(buf)
		if n > 0 {
			ch <- result{data: buf[:n], err: nil}
			return
		}
		ch <- result{err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		return res.data, res.err
	}
}

func (c *conn) Write(ctx context.Context, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return io.EOF
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.rw.SetWriteDeadline(deadline)
		defer c.rw.SetWriteDeadline(time.Time{})
	}
	_, err := c.rw.Write(data)
	return err
}

func (c *conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.rw.Close()
}

func (c *conn) LocalAddr() net.Addr                { return c.rw.LocalAddr() }
func (c *conn) RemoteAddr() net.Addr               { return c.rw.RemoteAddr() }
func (c *conn) Profile() transport.Profile         { return c.profile }
func (c *conn) SetReadDeadline(t time.Time) error  { return c.rw.SetReadDeadline(t) }
func (c *conn) SetWriteDeadline(t time.Time) error { return c.rw.SetWriteDeadline(t) }

// Transport implements in-memory transport for unit tests only.
type Transport struct{}

func NewTransport() *Transport { return &Transport{} }

func (t *Transport) Profile() transport.Profile { return transport.ProfileTLSTCP }

func (t *Transport) Dial(ctx context.Context, cfg transport.DialConfig) (transport.Conn, error) {
	return nil, io.ErrUnexpectedEOF
}

func (t *Transport) Listen(ctx context.Context, addr string, tlsConfig interface{}) (transport.Listener, error) {
	return nil, io.ErrUnexpectedEOF
}
