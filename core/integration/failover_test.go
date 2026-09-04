package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/nyxveil/nvp/auth/ticket"
	"github.com/nyxveil/nvp/controlplane/model"
	"github.com/nyxveil/nvp/failover"
	"github.com/nyxveil/nvp/internal/testutil"
	"github.com/nyxveil/nvp/server"
	"github.com/nyxveil/nvp/session"
	"github.com/nyxveil/nvp/transport"
	tlsstream "github.com/nyxveil/nvp/transport/tlsstream"
)

func startTestNode(t *testing.T, addr, nodeID string, verifier ticket.VerifierConfig, bundle *testutil.CertBundle) {
	t.Helper()
	cfg := &tls.Config{
		Certificates: []tls.Certificate{bundle.Cert},
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{"h2"},
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
				auth := server.NewAuthHandler(nodeID, verifier)
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
	buf := make([]byte, 65536)
	n, err := c.tlsConn.Read(buf)
	return buf[:n], err
}
func (c *tlsRawConn) Write(ctx context.Context, data []byte) error {
	_, err := c.tlsConn.Write(data)
	return err
}
func (c *tlsRawConn) Close() error               { return c.tlsConn.Close() }
func (c *tlsRawConn) LocalAddr() net.Addr        { return c.tlsConn.LocalAddr() }
func (c *tlsRawConn) RemoteAddr() net.Addr       { return c.tlsConn.RemoteAddr() }
func (c *tlsRawConn) Profile() transport.Profile { return transport.ProfileTLSTCP }

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

func TestTransportFailoverPolicy(t *testing.T) {
	policy := failover.DefaultConnectPolicy()
	if policy.TransportRacing.Primary != transport.ProfileQUICUDP {
		t.Fatal("primary should be QUIC")
	}
	if policy.TransportRacing.Fallback != transport.ProfileTLSTCP {
		t.Fatal("fallback should be TLS/TCP")
	}
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

	const n = 1000
	tokens := make([]string, n)
	for i := 0; i < n; i++ {
		tok, err := ticket.Issue(issuer, "lic_1", fmt.Sprintf("dev_%d", i), "user", "basic", nil, nil)
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
