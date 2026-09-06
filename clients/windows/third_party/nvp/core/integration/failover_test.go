package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/authhandler"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/failover"
	"github.com/nyxveil/nvp/core/internal/testutil"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
	tlsstream "github.com/nyxveil/nvp/core/transport/tlsstream"
)

func startTestNode(t *testing.T, addr, nodeID string, verifier ticket.VerifierConfig, bundle *testutil.CertBundle) {
	t.Helper()
	cfg := &tls.Config{
		Certificates: []tls.Certificate{bundle.Cert},
		MinVersion:   tls.VersionTLS13,
		// TLS path: no application ALPN.
	}
	ln, err := tls.Listen("tcp", addr, cfg)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				tlsConn := tls.Server(c, cfg)
				_ = tlsConn.Handshake()
				conn := &tlsRawConn{tlsConn: tlsConn}
				sess := session.New(session.DefaultConfig(false))
				_ = sess.Connect(ctx, conn)
				_ = sess.RunHandshake(ctx)
				auth := authhandler.NewAuthHandler(nodeID, "fi-hel", verifier)
				_ = sess.ReadLoop(ctx)
				_ = auth
			}(raw)
		}
	}()
}

type tlsRawConn struct {
	tlsConn *tls.Conn
}

func (c *tlsRawConn) Read(ctx context.Context) ([]byte, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.tlsConn.SetReadDeadline(deadline)
		defer c.tlsConn.SetReadDeadline(time.Time{})
	}
	buf := make([]byte, 65536)
	n, err := c.tlsConn.Read(buf)
	return buf[:n], err
}
func (c *tlsRawConn) Write(ctx context.Context, data []byte) error {
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.tlsConn.SetWriteDeadline(deadline)
		defer c.tlsConn.SetWriteDeadline(time.Time{})
	}
	_, err := c.tlsConn.Write(data)
	return err
}
func (c *tlsRawConn) Close() error                       { return c.tlsConn.Close() }
func (c *tlsRawConn) LocalAddr() net.Addr                { return c.tlsConn.LocalAddr() }
func (c *tlsRawConn) RemoteAddr() net.Addr               { return c.tlsConn.RemoteAddr() }
func (c *tlsRawConn) Profile() transport.Profile         { return transport.ProfileTLSTCP }
func (c *tlsRawConn) SetReadDeadline(t time.Time) error  { return c.tlsConn.SetReadDeadline(t) }
func (c *tlsRawConn) SetWriteDeadline(t time.Time) error { return c.tlsConn.SetWriteDeadline(t) }

func TestNodeFailover(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	issuer := ticket.IssuerConfig{
		Issuer: "https://control.nyxveil.test", Audience: "nvp-node",
		KeyID: "cp-key-1", PrivateKey: priv, TTL: 15 * time.Minute,
	}
	verifier := ticket.VerifierConfig{
		Issuer: issuer.Issuer, Audience: issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
	}
	bundle, _ := testutil.GenerateCertBundle("localhost")

	// Node A - closed port (unavailable)
	// Node B - listening
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addrB := ln.Addr().String()
	ln.Close()

	startTestNode(t, addrB, "node-b", verifier, bundle)

	catalog := model.Catalog{
		Locations: []model.Location{{LocationID: "fi-hel", Country: "FI", City: "Helsinki", Enabled: true}},
		Nodes: []model.NodeRegistryEntry{
			{
				NodeID: "node-a", LocationID: "fi-hel", Enabled: true,
				Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: 1, Profiles: []transport.Profile{transport.ProfileTLSTCP}}},
				Health:    model.HealthInfo{Healthy: true}, Capacity: 100,
			},
			{
				NodeID: "node-b", LocationID: "fi-hel", Enabled: true,
				Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: mustPort(addrB), Profiles: []transport.Profile{transport.ProfileTLSTCP}}},
				Health:    model.HealthInfo{Healthy: true}, Capacity: 100,
			},
		},
	}

	sel := &failover.Selector{Catalog: catalog, Role: "user", LocationID: "fi-hel"}
	reg := transport.NewRegistry()
	reg.Register(tlsstream.NewTransport())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	policy := failover.DefaultConnectPolicy()
	policy.TransportRacing = transport.RacingConfig{
		Primary:       transport.ProfileTLSTCP,
		Fallback:      transport.ProfileTLSTCP,
		FallbackDelay: 100 * time.Millisecond,
	}
	conn, node, err := failover.ConnectWithFailover(ctx, sel, reg, policy, &testDialProvider{bundle: bundle})
	if err != nil {
		t.Fatalf("failover connect: %v", err)
	}
	if node.NodeID != "node-b" {
		t.Fatalf("expected node-b, got %s", node.NodeID)
	}
	conn.Close()
}

func mustPort(addr string) int {
	_, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return port
}

type testDialProvider struct {
	bundle *testutil.CertBundle
}

func (p *testDialProvider) RootCAs() interface{} { return p.bundle.CAPool }
func (p *testDialProvider) ServerNameFor(_ model.NodeRegistryEntry) string {
	return p.bundle.ServerName
}
func (p *testDialProvider) PinnedPubKeyFor(_ model.NodeRegistryEntry) []byte { return nil }
func (p *testDialProvider) ECHPolicy() transport.ECHPolicy                   { return "" }
func (p *testDialProvider) ECHConfigList() []byte                            { return nil }

func TestTransportFailoverPolicy(t *testing.T) {
	policy := failover.DefaultConnectPolicy()
	if policy.TransportRacing.Primary != transport.ProfileQUICUDP {
		t.Fatal("primary should be QUIC")
	}
	if policy.TransportRacing.Fallback != transport.ProfileTLSTCP {
		t.Fatal("fallback should be TLS/TCP")
	}
}

func TestFailoverTimingSlowPrimary(t *testing.T) {
	const (
		primaryDelay  = 2 * time.Second
		fallbackDelay = 50 * time.Millisecond
		primaryTO     = 10 * time.Second
	)

	var closed atomic.Int32
	reg := transport.NewRegistry()
	reg.Register(&scriptedTransport{
		profile: transport.ProfileQUICUDP,
		dial: func(ctx context.Context, _ transport.DialConfig) (transport.Conn, error) {
			timer := time.NewTimer(primaryDelay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
				return &countingConn{profile: transport.ProfileQUICUDP, onClose: func() { closed.Add(1) }}, nil
			}
		},
	})
	reg.Register(&scriptedTransport{
		profile: transport.ProfileTLSTCP,
		dial: func(ctx context.Context, _ transport.DialConfig) (transport.Conn, error) {
			return &countingConn{profile: transport.ProfileTLSTCP, onClose: func() { closed.Add(1) }}, nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	conn, err := reg.DialWithRacing(ctx, transport.DialConfig{
		Endpoint: transport.Endpoint{Host: "127.0.0.1", Port: 443},
		Timeout:  primaryTO,
	}, transport.RacingConfig{
		Primary:       transport.ProfileQUICUDP,
		Fallback:      transport.ProfileTLSTCP,
		FallbackDelay: fallbackDelay,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("racing dial: %v", err)
	}
	if conn.Profile() != transport.ProfileTLSTCP {
		t.Fatalf("expected fallback win, got %s", conn.Profile())
	}
	if elapsed > primaryDelay/2 {
		t.Fatalf("elapsed %v not << primary delay %v (should not wait for slow primary)", elapsed, primaryDelay)
	}
	_ = conn.Close()

	// Best-effort: loser primary should be cancelled/closed without hanging.
	deadline := time.Now().Add(3 * time.Second)
	for closed.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
}

type scriptedTransport struct {
	profile transport.Profile
	dial    func(context.Context, transport.DialConfig) (transport.Conn, error)
}

func (t *scriptedTransport) Profile() transport.Profile { return t.profile }
func (t *scriptedTransport) Dial(ctx context.Context, cfg transport.DialConfig) (transport.Conn, error) {
	return t.dial(ctx, cfg)
}
func (t *scriptedTransport) Listen(context.Context, string, interface{}) (transport.Listener, error) {
	return nil, fmt.Errorf("not implemented")
}

type countingConn struct {
	profile transport.Profile
	onClose func()
	closed  atomic.Bool
}

func (c *countingConn) Read(context.Context) ([]byte, error) { return nil, context.Canceled }
func (c *countingConn) Write(context.Context, []byte) error  { return nil }
func (c *countingConn) LocalAddr() net.Addr                  { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (c *countingConn) RemoteAddr() net.Addr                 { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (c *countingConn) Profile() transport.Profile           { return c.profile }
func (c *countingConn) SetReadDeadline(time.Time) error      { return nil }
func (c *countingConn) SetWriteDeadline(time.Time) error     { return nil }
func (c *countingConn) Close() error {
	if c.closed.CompareAndSwap(false, true) && c.onClose != nil {
		c.onClose()
	}
	return nil
}

func TestAuthScaleOfflineVerification(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	issuer := ticket.IssuerConfig{
		Issuer: "https://control.nyxveil.test", Audience: "nvp-node",
		KeyID: "cp-key-1", PrivateKey: priv, TTL: 15 * time.Minute,
	}
	cfg := ticket.VerifierConfig{
		Issuer: issuer.Issuer, Audience: issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
	}

	devPub, _, _ := ed25519.GenerateKey(nil)
	const n = 1000
	tokens := make([]string, n)
	for i := 0; i < n; i++ {
		tok, err := ticket.IssueWithDevice(issuer, "lic_1", fmt.Sprintf("dev_%d", i), "user", "basic", nil, nil, devPub)
		if err != nil {
			t.Fatal(err)
		}
		tokens[i] = tok
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(tok string, idx int) {
			defer wg.Done()
			_, err := ticket.Verify(cfg, tok, fmt.Sprintf("dev_%d", idx), "")
			if err != nil {
				t.Error(err)
			}
		}(tokens[i], i)
	}
	wg.Wait()
}
