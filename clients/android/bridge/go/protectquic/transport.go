package protectquic

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/nyxveil/client-android/bridge/protectnet"
	"github.com/nyxveil/nvp/core/protocol"
	"github.com/nyxveil/nvp/core/transport"
	"github.com/nyxveil/nvp/core/transport/ech"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// Transport is QUIC/UDP with Android VpnService.protect on the UDP socket before dial.
type Transport struct {
	Protect protectnet.ProtectFunc
}

func New(protect protectnet.ProtectFunc) *Transport {
	return &Transport{Protect: protect}
}

func (t *Transport) Profile() transport.Profile { return transport.ProfileQUICUDP }

func (t *Transport) Dial(ctx context.Context, cfg transport.DialConfig) (transport.Conn, error) {
	addr := net.JoinHostPort(cfg.Endpoint.Host, fmt.Sprintf("%d", cfg.Endpoint.Port))
	authority := addr
	if cfg.ServerName != "" {
		authority = net.JoinHostPort(cfg.ServerName, fmt.Sprintf("%d", cfg.Endpoint.Port))
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		ServerName: cfg.ServerName,
		NextProtos: []string{http3.NextProtoH3},
	}
	if pool, ok := cfg.RootCAs.(*x509.CertPool); ok && pool != nil {
		tlsCfg.RootCAs = pool
	}
	if err := ech.ApplyClientConfig(tlsCfg, cfg.ECHPolicy, cfg.ECHConfigList); err != nil {
		return nil, err
	}

	qcfg := &quic.Config{
		MaxIdleTimeout:  60 * time.Second,
		EnableDatagrams: true,
	}

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("resolve udp: %w", err)
	}
	lc := net.ListenConfig{Control: protectnet.Control(t.Protect)}
	pc, err := lc.ListenPacket(ctx, "udp", ":0")
	if err != nil {
		return nil, fmt.Errorf("protected udp listen: %w", err)
	}

	session, err := quic.Dial(ctx, pc, udpAddr, tlsCfg, qcfg)
	if err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("quic dial: %w", err)
	}
	state := session.ConnectionState().TLS
	if state.NegotiatedProtocol != http3.NextProtoH3 {
		_ = session.CloseWithError(0, "alpn")
		_ = pc.Close()
		return nil, fmt.Errorf("unexpected ALPN %q (want %s)", state.NegotiatedProtocol, http3.NextProtoH3)
	}
	if err := ech.VerifyNegotiated(cfg.ECHPolicy, state); err != nil {
		_ = session.CloseWithError(0, "ech required")
		_ = pc.Close()
		return nil, err
	}
	if err := transport.VerifySPKIPin(state, cfg.PinnedPubKey); err != nil {
		_ = session.CloseWithError(0, "pin mismatch")
		_ = pc.Close()
		return nil, err
	}

	rt := &http3.Transport{EnableDatagrams: true}
	cc := rt.NewClientConn(session)
	str, err := cc.OpenRequestStream(ctx)
	if err != nil {
		_ = session.CloseWithError(0, "open request stream")
		_ = pc.Close()
		return nil, fmt.Errorf("http3 open stream: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodConnect, "https://"+authority, nil)
	if err != nil {
		_ = session.CloseWithError(0, "connect request")
		_ = pc.Close()
		return nil, err
	}
	req.Host = authority
	if err := str.SendRequestHeader(req); err != nil {
		_ = session.CloseWithError(0, "connect send")
		_ = pc.Close()
		return nil, fmt.Errorf("http3 CONNECT send: %w", err)
	}
	resp, err := str.ReadResponse()
	if err != nil {
		_ = session.CloseWithError(0, "connect response")
		_ = pc.Close()
		return nil, fmt.Errorf("http3 CONNECT response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = session.CloseWithError(0, "connect rejected")
		_ = pc.Close()
		return nil, fmt.Errorf("http3 CONNECT status %d", resp.StatusCode)
	}

	datagrams := session.ConnectionState().SupportsDatagrams
	return &conn{session: session, stream: str, packetConn: pc, datagrams: datagrams}, nil
}

func (t *Transport) Listen(ctx context.Context, addr string, tlsConfig interface{}) (transport.Listener, error) {
	return nil, fmt.Errorf("protectquic: listen not supported on Android client")
}

type streamIO interface {
	io.ReadWriter
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
	Close() error
	SendDatagram([]byte) error
	ReceiveDatagram(context.Context) ([]byte, error)
}

type conn struct {
	session    quic.Connection
	stream     streamIO
	packetConn net.PacketConn
	datagrams  bool
	mu         sync.Mutex
	readMu     sync.Mutex
}

func (c *conn) Read(ctx context.Context) ([]byte, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.stream.SetReadDeadline(deadline)
		defer c.stream.SetReadDeadline(time.Time{})
	}
	var length uint32
	if err := binary.Read(c.stream, binary.BigEndian, &length); err != nil {
		return nil, err
	}
	if length == 0 || int(length) > protocol.MaxFrameSize {
		return nil, fmt.Errorf("invalid frame length %d", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(c.stream, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (c *conn) Write(ctx context.Context, data []byte) error {
	if len(data) == 0 || len(data) > protocol.MaxFrameSize {
		return fmt.Errorf("invalid frame length %d", len(data))
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.stream.SetWriteDeadline(deadline)
		defer c.stream.SetWriteDeadline(time.Time{})
	}
	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(data)))
	copy(frame[4:], data)
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.stream.Write(frame)
	return err
}

func (c *conn) DatagramsEnabled() bool { return c.datagrams }

func (c *conn) WriteDatagram(ctx context.Context, data []byte) error {
	if !c.datagrams {
		return c.Write(ctx, data)
	}
	if len(data) == 0 || len(data) > protocol.MaxFrameSize {
		return fmt.Errorf("invalid datagram length %d", len(data))
	}
	return c.stream.SendDatagram(data)
}

func (c *conn) ReadDatagram(ctx context.Context) ([]byte, error) {
	if !c.datagrams {
		return c.Read(ctx)
	}
	return c.stream.ReceiveDatagram(ctx)
}

func (c *conn) Close() error {
	_ = c.stream.Close()
	var err error
	if c.session != nil {
		err = c.session.CloseWithError(0, "closed")
	}
	if c.packetConn != nil {
		_ = c.packetConn.Close()
	}
	return err
}

func (c *conn) LocalAddr() net.Addr {
	if c.session != nil {
		return c.session.LocalAddr()
	}
	return nil
}
func (c *conn) RemoteAddr() net.Addr {
	if c.session != nil {
		return c.session.RemoteAddr()
	}
	return nil
}
func (c *conn) Profile() transport.Profile         { return transport.ProfileQUICUDP }
func (c *conn) SetReadDeadline(t time.Time) error  { return c.stream.SetReadDeadline(t) }
func (c *conn) SetWriteDeadline(t time.Time) error { return c.stream.SetWriteDeadline(t) }

var (
	_ transport.Conn         = (*conn)(nil)
	_ transport.DatagramConn = (*conn)(nil)
)
