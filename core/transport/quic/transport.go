package quictransport

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
	"github.com/quic-go/quic-go"
)

// Transport implements QUIC / HTTP/3 capable UDP transport on port 443.
type Transport struct{}

func NewTransport() *Transport { return &Transport{} }

func (t *Transport) Profile() transport.Profile { return transport.ProfileQUICUDP }

func (t *Transport) Dial(ctx context.Context, cfg transport.DialConfig) (transport.Conn, error) {
	addr := net.JoinHostPort(cfg.Endpoint.Host, fmt.Sprintf("%d", cfg.Endpoint.Port))

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		ServerName: cfg.ServerName,
		NextProtos: []string{"h3"},
	}
	if pool, ok := cfg.RootCAs.(*x509.CertPool); ok && pool != nil {
		tlsCfg.RootCAs = pool
	}
	if err := ech.ApplyClientConfig(tlsCfg, cfg.ECHPolicy, cfg.ECHConfigList); err != nil {
		return nil, err
	}

	qcfg := &quic.Config{
		MaxIdleTimeout:  60 * time.Second,
		EnableDatagrams: false,
	}

	session, err := quic.DialAddr(ctx, addr, tlsCfg, qcfg)
	if err != nil {
		return nil, fmt.Errorf("quic dial: %w", err)
	}
	if err := ech.VerifyNegotiated(cfg.ECHPolicy, session.ConnectionState().TLS); err != nil {
		session.CloseWithError(0, "ech required")
		return nil, err
	}

	stream, err := session.OpenStreamSync(ctx)
	if err != nil {
		session.CloseWithError(0, "open stream failed")
		return nil, fmt.Errorf("quic open stream: %w", err)
	}

	return &conn{session: session, stream: stream, profile: transport.ProfileQUICUDP}, nil
}

func (t *Transport) Listen(ctx context.Context, addr string, tlsConfig interface{}) (transport.Listener, error) {
	cfg, ok := tlsConfig.(*tls.Config)
	if !ok {
		return nil, fmt.Errorf("quic listen requires *tls.Config")
	}
	cfg.MinVersion = tls.VersionTLS13
	if len(cfg.NextProtos) == 0 {
		cfg.NextProtos = []string{"h3"}
	}

	qcfg := &quic.Config{
		MaxIdleTimeout: 60 * time.Second,
	}

	ln, err := quic.ListenAddr(addr, cfg, qcfg)
	if err != nil {
		return nil, err
	}
	return &listener{ln: ln}, nil
}

type listener struct {
	ln *quic.Listener
}

func (l *listener) Accept(ctx context.Context) (transport.Conn, error) {
	session, err := l.ln.Accept(ctx)
	if err != nil {
		return nil, err
	}
	stream, err := session.AcceptStream(ctx)
	if err != nil {
		session.CloseWithError(0, "accept stream failed")
		return nil, err
	}
	return &conn{session: session, stream: stream, profile: transport.ProfileQUICUDP}, nil
}

func (l *listener) Close() error   { return l.ln.Close() }
func (l *listener) Addr() net.Addr { return l.ln.Addr() }

type conn struct {
	session quic.Connection
	stream  quic.Stream
	profile transport.Profile
}

func (c *conn) Read(ctx context.Context) ([]byte, error) {
	var length uint32
	if err := binary.Read(c.stream, binary.BigEndian, &length); err != nil {
		return nil, err
	}
	if length > 65536 {
		return nil, fmt.Errorf("frame too large")
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(c.stream, buf); err != nil {
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
	if _, err := c.stream.Write(header); err != nil {
		return err
	}
	_, err := c.stream.Write(data)
	return err
}

func (c *conn) Close() error {
	c.stream.Close()
	return c.session.CloseWithError(0, "closed")
}

func (c *conn) LocalAddr() net.Addr        { return c.session.LocalAddr() }
func (c *conn) RemoteAddr() net.Addr       { return c.session.RemoteAddr() }
func (c *conn) Profile() transport.Profile { return c.profile }
