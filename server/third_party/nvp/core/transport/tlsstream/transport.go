package tlsstream

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/nyxveil/nvp/core/protocol"
	"github.com/nyxveil/nvp/core/transport"
	"github.com/nyxveil/nvp/core/transport/ech"
)

// Transport implements TLS 1.3 over TCP fallback.
// NextProtos is left empty (no application ALPN); NVP version negotiation
// stays inside the encrypted session handshake.
type Transport struct{}

func NewTransport() *Transport { return &Transport{} }

func (t *Transport) Profile() transport.Profile { return transport.ProfileTLSTCP }

func (t *Transport) Dial(ctx context.Context, cfg transport.DialConfig) (transport.Conn, error) {
	addr := net.JoinHostPort(cfg.Endpoint.Host, fmt.Sprintf("%d", cfg.Endpoint.Port))
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout}

	raw, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("tcp dial: %w", err)
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		ServerName: cfg.ServerName,
		// Intentionally empty NextProtos: no custom or fake ALPN on TLS path.
	}
	if pool, ok := cfg.RootCAs.(*x509.CertPool); ok && pool != nil {
		tlsCfg.RootCAs = pool
	}
	if err := ech.ApplyClientConfig(tlsCfg, cfg.ECHPolicy, cfg.ECHConfigList); err != nil {
		raw.Close()
		return nil, err
	}

	tlsConn := tls.Client(raw, tlsCfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, fmt.Errorf("tls handshake: %w", err)
	}

	state := tlsConn.ConnectionState()
	if state.Version < tls.VersionTLS13 {
		raw.Close()
		return nil, fmt.Errorf("tls downgrade rejected: version %x", state.Version)
	}
	if err := ech.VerifyNegotiated(cfg.ECHPolicy, state); err != nil {
		raw.Close()
		return nil, err
	}
	if err := transport.VerifySPKIPin(state, cfg.PinnedPubKey); err != nil {
		raw.Close()
		return nil, err
	}

	return &conn{tlsConn: tlsConn, profile: transport.ProfileTLSTCP}, nil
}

func (t *Transport) Listen(ctx context.Context, addr string, tlsConfig interface{}) (transport.Listener, error) {
	cfg, ok := tlsConfig.(*tls.Config)
	if !ok {
		return nil, fmt.Errorf("tls listen requires *tls.Config")
	}
	cfg.MinVersion = tls.VersionTLS13
	// Do not inject an application ALPN. Leave NextProtos as configured by caller
	// (empty is correct for NVP TLS).
	ln, err := tls.Listen("tcp", addr, cfg)
	if err != nil {
		return nil, err
	}
	return &listener{ln: ln}, nil
}

type listener struct {
	ln net.Listener
}

func (l *listener) Accept(ctx context.Context) (transport.Conn, error) {
	raw, err := l.ln.Accept()
	if err != nil {
		return nil, err
	}
	tlsConn, ok := raw.(*tls.Conn)
	if !ok {
		raw.Close()
		return nil, fmt.Errorf("expected tls conn")
	}
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		tlsConn.Close()
		return nil, err
	}
	state := tlsConn.ConnectionState()
	if state.Version < tls.VersionTLS13 {
		tlsConn.Close()
		return nil, fmt.Errorf("tls downgrade rejected")
	}
	return &conn{tlsConn: tlsConn, profile: transport.ProfileTLSTCP}, nil
}

func (l *listener) Close() error   { return l.ln.Close() }
func (l *listener) Addr() net.Addr { return l.ln.Addr() }

// Compile-time check: tlsstream conn satisfies transport.Conn.
var _ transport.Conn = (*conn)(nil)

type conn struct {
	tlsConn *tls.Conn
	profile transport.Profile
}

func (c *conn) Read(ctx context.Context) ([]byte, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.tlsConn.SetReadDeadline(deadline)
		defer c.tlsConn.SetReadDeadline(time.Time{})
	}
	var length uint32
	if err := binary.Read(c.tlsConn, binary.BigEndian, &length); err != nil {
		return nil, err
	}
	if length == 0 || int(length) > protocol.MaxFrameSize {
		return nil, fmt.Errorf("invalid frame length %d", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(c.tlsConn, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (c *conn) Write(ctx context.Context, data []byte) error {
	if len(data) == 0 || len(data) > protocol.MaxFrameSize {
		return fmt.Errorf("invalid frame length %d", len(data))
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.tlsConn.SetWriteDeadline(deadline)
		defer c.tlsConn.SetWriteDeadline(time.Time{})
	}
	// Single write: length || payload — avoids deterministic 4-byte TLS record split.
	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(data)))
	copy(frame[4:], data)
	_, err := c.tlsConn.Write(frame)
	return err
}

func (c *conn) Close() error                       { return c.tlsConn.Close() }
func (c *conn) LocalAddr() net.Addr                { return c.tlsConn.LocalAddr() }
func (c *conn) RemoteAddr() net.Addr               { return c.tlsConn.RemoteAddr() }
func (c *conn) Profile() transport.Profile         { return c.profile }
func (c *conn) SetReadDeadline(t time.Time) error  { return c.tlsConn.SetReadDeadline(t) }
func (c *conn) SetWriteDeadline(t time.Time) error { return c.tlsConn.SetWriteDeadline(t) }
func (c *conn) ConnectionState() tls.ConnectionState {
	return c.tlsConn.ConnectionState()
}
