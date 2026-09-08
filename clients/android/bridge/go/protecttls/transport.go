package protecttls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/nyxveil/client-android/bridge/protectnet"
	"github.com/nyxveil/nvp/core/protocol"
	"github.com/nyxveil/nvp/core/transport"
	"github.com/nyxveil/nvp/core/transport/ech"
)

// Transport is TLS/TCP with Android VpnService.protect on the TCP socket before dial.
type Transport struct {
	Protect protectnet.ProtectFunc
}

func New(protect protectnet.ProtectFunc) *Transport {
	return &Transport{Protect: protect}
}

func (t *Transport) Profile() transport.Profile { return transport.ProfileTLSTCP }

func (t *Transport) Dial(ctx context.Context, cfg transport.DialConfig) (transport.Conn, error) {
	addr := net.JoinHostPort(cfg.Endpoint.Host, fmt.Sprintf("%d", cfg.Endpoint.Port))
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	dialer := &net.Dialer{
		Timeout: timeout,
		Control: protectnet.Control(t.Protect),
	}
	raw, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("protected tcp dial: %w", err)
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		ServerName: cfg.ServerName,
	}
	if pool, ok := cfg.RootCAs.(*x509.CertPool); ok && pool != nil {
		tlsCfg.RootCAs = pool
	}
	if err := ech.ApplyClientConfig(tlsCfg, cfg.ECHPolicy, cfg.ECHConfigList); err != nil {
		_ = raw.Close()
		return nil, err
	}

	tlsConn := tls.Client(raw, tlsCfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("tls handshake: %w", err)
	}
	state := tlsConn.ConnectionState()
	if state.Version < tls.VersionTLS13 {
		_ = raw.Close()
		return nil, fmt.Errorf("tls downgrade rejected: version %x", state.Version)
	}
	if err := ech.VerifyNegotiated(cfg.ECHPolicy, state); err != nil {
		_ = raw.Close()
		return nil, err
	}
	if err := transport.VerifySPKIPin(state, cfg.PinnedPubKey); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return &conn{tlsConn: tlsConn}, nil
}

func (t *Transport) Listen(ctx context.Context, addr string, tlsConfig interface{}) (transport.Listener, error) {
	return nil, fmt.Errorf("protecttls: listen not supported on Android client")
}

type conn struct {
	tlsConn *tls.Conn
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
	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(data)))
	copy(frame[4:], data)
	_, err := c.tlsConn.Write(frame)
	return err
}

func (c *conn) Close() error                       { return c.tlsConn.Close() }
func (c *conn) LocalAddr() net.Addr                { return c.tlsConn.LocalAddr() }
func (c *conn) RemoteAddr() net.Addr               { return c.tlsConn.RemoteAddr() }
func (c *conn) Profile() transport.Profile         { return transport.ProfileTLSTCP }
func (c *conn) SetReadDeadline(t time.Time) error  { return c.tlsConn.SetReadDeadline(t) }
func (c *conn) SetWriteDeadline(t time.Time) error { return c.tlsConn.SetWriteDeadline(t) }

var _ transport.Conn = (*conn)(nil)
