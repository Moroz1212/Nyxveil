package quictransport

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

	"github.com/nyxveil/nvp/core/protocol"
	"github.com/nyxveil/nvp/core/transport"
	"github.com/nyxveil/nvp/core/transport/ech"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// Transport implements QUIC/UDP primary transport via real HTTP/3 CONNECT.
// ALPN is genuine "h3" only; NVP frames ride on the CONNECT tunnel stream
// (length-prefixed) with optional HTTP/3 DATAGRAMs for DATA.
type Transport struct{}

func NewTransport() *Transport { return &Transport{} }

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

	session, err := quic.DialAddr(ctx, addr, tlsCfg, qcfg)
	if err != nil {
		return nil, fmt.Errorf("quic dial: %w", err)
	}
	state := session.ConnectionState().TLS
	if state.NegotiatedProtocol != http3.NextProtoH3 {
		session.CloseWithError(0, "alpn")
		return nil, fmt.Errorf("unexpected ALPN %q (want %s)", state.NegotiatedProtocol, http3.NextProtoH3)
	}
	if err := ech.VerifyNegotiated(cfg.ECHPolicy, state); err != nil {
		session.CloseWithError(0, "ech required")
		return nil, err
	}
	if err := transport.VerifySPKIPin(state, cfg.PinnedPubKey); err != nil {
		session.CloseWithError(0, "pin mismatch")
		return nil, err
	}

	rt := &http3.Transport{EnableDatagrams: true}
	cc := rt.NewClientConn(session)
	str, err := cc.OpenRequestStream(ctx)
	if err != nil {
		session.CloseWithError(0, "open request stream")
		return nil, fmt.Errorf("http3 open stream: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodConnect, "https://"+authority, nil)
	if err != nil {
		session.CloseWithError(0, "connect request")
		return nil, err
	}
	req.Host = authority

	if err := str.SendRequestHeader(req); err != nil {
		session.CloseWithError(0, "connect send")
		return nil, fmt.Errorf("http3 CONNECT send: %w", err)
	}
	resp, err := str.ReadResponse()
	if err != nil {
		session.CloseWithError(0, "connect response")
		return nil, fmt.Errorf("http3 CONNECT response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		session.CloseWithError(0, "connect rejected")
		return nil, fmt.Errorf("http3 CONNECT status %d", resp.StatusCode)
	}

	datagrams := session.ConnectionState().SupportsDatagrams
	return newConn(session, str, datagrams), nil
}

func (t *Transport) Listen(ctx context.Context, addr string, tlsConfig interface{}) (transport.Listener, error) {
	cfg, ok := tlsConfig.(*tls.Config)
	if !ok {
		return nil, fmt.Errorf("quic listen requires *tls.Config")
	}
	cfg.MinVersion = tls.VersionTLS13

	accepted := make(chan transport.Conn, 16)
	closed := make(chan struct{})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		streamer, ok := w.(http3.HTTPStreamer)
		if !ok {
			return
		}
		// HTTPStream takes over the stream; handler may return without closing it.
		str := streamer.HTTPStream()
		var hconn http3.Connection
		if hj, ok := w.(http3.Hijacker); ok {
			hconn = hj.Connection()
		}
		c := newServerConn(hconn, str)
		select {
		case accepted <- c:
		case <-closed:
			_ = c.Close()
		case <-r.Context().Done():
			_ = c.Close()
		}
	})

	qcfg := &quic.Config{
		MaxIdleTimeout:  60 * time.Second,
		EnableDatagrams: true,
	}
	server := &http3.Server{
		Handler:         handler,
		TLSConfig:       cfg,
		QUICConfig:      qcfg,
		EnableDatagrams: true,
	}

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, err
	}

	ln := &listener{
		server:   server,
		udp:      udpConn,
		accepted: accepted,
		closed:   closed,
	}
	go func() {
		_ = server.Serve(udpConn)
		close(accepted)
	}()
	return ln, nil
}

type listener struct {
	server    *http3.Server
	udp       *net.UDPConn
	accepted  chan transport.Conn
	closed    chan struct{}
	closeOnce sync.Once
}

func (l *listener) Accept(ctx context.Context) (transport.Conn, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.closed:
		return nil, net.ErrClosed
	case c, ok := <-l.accepted:
		if !ok {
			return nil, net.ErrClosed
		}
		return c, nil
	}
}

func (l *listener) Close() error {
	l.closeOnce.Do(func() {
		close(l.closed)
		_ = l.server.Close()
		_ = l.udp.Close()
	})
	return nil
}

func (l *listener) Addr() net.Addr { return l.udp.LocalAddr() }

type streamIO interface {
	io.ReadWriter
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
	Close() error
	SendDatagram([]byte) error
	ReceiveDatagram(context.Context) ([]byte, error)
}

type conn struct {
	session   quic.Connection
	hconn     http3.Connection
	stream    streamIO
	profile   transport.Profile
	datagrams bool
	mu        sync.Mutex // writes
	readMu    sync.Mutex // serialize stream reads (WaitEstablished vs ReadLoop)
}

func newConn(session quic.Connection, stream streamIO, datagrams bool) *conn {
	return &conn{
		session:   session,
		stream:    stream,
		profile:   transport.ProfileQUICUDP,
		datagrams: datagrams && session.ConnectionState().SupportsDatagrams,
	}
}

func newServerConn(hconn http3.Connection, stream streamIO) *conn {
	datagrams := false
	if hconn != nil {
		datagrams = hconn.ConnectionState().SupportsDatagrams
	}
	return &conn{
		hconn:     hconn,
		stream:    stream,
		profile:   transport.ProfileQUICUDP,
		datagrams: datagrams,
	}
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
	type res struct {
		b   []byte
		err error
	}
	ch := make(chan res, 1)
	go func() {
		b, err := c.stream.ReceiveDatagram(ctx)
		ch <- res{b, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.b, r.err
	}
}

func (c *conn) Close() error {
	_ = c.stream.Close()
	if c.session != nil {
		return c.session.CloseWithError(0, "closed")
	}
	if c.hconn != nil {
		return c.hconn.CloseWithError(0, "closed")
	}
	return nil
}

func (c *conn) LocalAddr() net.Addr {
	if c.session != nil {
		return c.session.LocalAddr()
	}
	if c.hconn != nil {
		return c.hconn.LocalAddr()
	}
	return nil
}

func (c *conn) RemoteAddr() net.Addr {
	if c.session != nil {
		return c.session.RemoteAddr()
	}
	if c.hconn != nil {
		return c.hconn.RemoteAddr()
	}
	return nil
}

func (c *conn) Profile() transport.Profile         { return c.profile }
func (c *conn) SetReadDeadline(t time.Time) error  { return c.stream.SetReadDeadline(t) }
func (c *conn) SetWriteDeadline(t time.Time) error { return c.stream.SetWriteDeadline(t) }

// ConnectionState returns the TLS connection state when available (for ECH checks).
func (c *conn) ConnectionState() tls.ConnectionState {
	if c.session != nil {
		return c.session.ConnectionState().TLS
	}
	if c.hconn != nil {
		return c.hconn.ConnectionState().TLS
	}
	return tls.ConnectionState{}
}

var (
	_ transport.Conn         = (*conn)(nil)
	_ transport.DatagramConn = (*conn)(nil)
)
