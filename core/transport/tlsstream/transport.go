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

	"github.com/nyxveil/nvp/transport"
	"github.com/nyxveil/nvp/transport/ech"
)

// Transport implements TLS 1.3 over TCP/443 fallback transport.
type Transport struct{}

func NewTransport() *Transport { return &Transport{} }

func (t *Transport) Profile() transport.Profile { return transport.ProfileTLSTCP }

func (t *Transport) Dial(ctx context.Context, cfg transport.DialConfig) (transport.Conn, error) {
	addr := net.JoinHostPort(cfg.Endpoint.Host, fmt.Sprintf("%d", cfg.Endpoint.Port))
	dialer := &net.Dialer{Timeout: cfg.Timeout}
	if cfg.Timeout == 0 {
		dialer.Timeout = 10 * time.Second
	}

	raw, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("tcp dial: %w", err)
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		ServerName: cfg.ServerName,
		NextProtos: []string{"h2"}, // standards-compliant ALPN
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

	return &conn{tlsConn: tlsConn, profile: transport.ProfileTLSTCP}, nil
}

func (t *Transport) Listen(ctx context.Context, addr string, tlsConfig interface{}) (transport.Listener, error) {
	cfg, ok := tlsConfig.(*tls.Config)
	if !ok {
		return nil, fmt.Errorf("tls listen requires *tls.Config")
	}
	cfg.MinVersion = tls.VersionTLS13
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
	state := tlsConn.ConnectionState()
	if state.Version < tls.VersionTLS13 {
		tlsConn.Close()
		return nil, fmt.Errorf("tls downgrade rejected")
	}
	return &conn{tlsConn: tlsConn, profile: transport.ProfileTLSTCP}, nil
}

func (l *listener) Close() error   { return l.ln.Close() }
func (l *listener) Addr() net.Addr { return l.ln.Addr() }

type conn struct {
	tlsConn *tls.Conn
	profile transport.Profile
}

func (c *conn) Read(ctx context.Context) ([]byte, error) {
	var length uint32
	if err := binary.Read(c.tlsConn, binary.BigEndian, &length); err != nil {
		return nil, err
	}
	if length > 65536 {
		return nil, fmt.Errorf("frame too large")
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(c.tlsConn, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (c *conn) Write(ctx context.Context, data []byte) error {
	if len(data) > 65536 {
		return fmt.Errorf("frame too large")
	}
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))
	if _, err := c.tlsConn.Write(header); err != nil {
		return err
	}
	_, err := c.tlsConn.Write(data)
	return err
}

func (c *conn) Close() error               { return c.tlsConn.Close() }
func (c *conn) LocalAddr() net.Addr        { return c.tlsConn.LocalAddr() }
func (c *conn) RemoteAddr() net.Addr       { return c.tlsConn.RemoteAddr() }
func (c *conn) Profile() transport.Profile { return c.profile }
