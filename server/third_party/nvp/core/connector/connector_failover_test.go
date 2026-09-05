package connector_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/connector"
	"github.com/nyxveil/nvp/core/control"
	"github.com/nyxveil/nvp/core/controlplane/api"
	"github.com/nyxveil/nvp/core/controlplane/catalog"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/failover"
	"github.com/nyxveil/nvp/core/internal/testutil"
	"github.com/nyxveil/nvp/core/nvperr"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
	tlsstream "github.com/nyxveil/nvp/core/transport/tlsstream"
)

type captureDialProvider struct {
	rootCAs    *x509.CertPool
	serverName string
	pin        []byte
	echPolicy  transport.ECHPolicy
	echList    []byte
	lastCfg    transport.DialConfig
	captured   bool
}

func (p *captureDialProvider) RootCAs() interface{} { return p.rootCAs }
func (p *captureDialProvider) ServerNameFor(_ model.NodeRegistryEntry) string {
	return p.serverName
}
func (p *captureDialProvider) PinnedPubKeyFor(_ model.NodeRegistryEntry) []byte {
	return append([]byte(nil), p.pin...)
}
func (p *captureDialProvider) ECHPolicy() transport.ECHPolicy { return p.echPolicy }
func (p *captureDialProvider) ECHConfigList() []byte {
	return append([]byte(nil), p.echList...)
}

type captureTransport struct {
	profile  transport.Profile
	provider *captureDialProvider
	dialErr  error
	conn     transport.Conn
}

func (t *captureTransport) Profile() transport.Profile { return t.profile }
func (t *captureTransport) Dial(_ context.Context, cfg transport.DialConfig) (transport.Conn, error) {
	if t.provider != nil {
		t.provider.lastCfg = cfg
		t.provider.captured = true
	}
	if t.dialErr != nil {
		return nil, t.dialErr
	}
	if t.conn != nil {
		return t.conn, nil
	}
	return nil, fmt.Errorf("no conn")
}
func (t *captureTransport) Listen(context.Context, string, interface{}) (transport.Listener, error) {
	return nil, fmt.Errorf("not implemented")
}

func spkiPin(cert tls.Certificate) []byte {
	var der []byte
	if len(cert.Certificate) > 0 {
		der = cert.Certificate[0]
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return nil
	}
	sum := sha256.Sum256(parsed.RawSubjectPublicKeyInfo)
	return sum[:]
}

func startTLSNode(t *testing.T, bundle *testutil.CertBundle) (addr string, closeFn func()) {
	t.Helper()
	return startTLSNodeWithAuth(t, bundle, nil)
}

func startTLSNodeWithAuth(t *testing.T, bundle *testutil.CertBundle, onAuth func(ctx context.Context, sess *session.Session, payload []byte) error) (addr string, closeFn func()) {
	t.Helper()
	cfg := &tls.Config{
		Certificates: []tls.Certificate{bundle.Cert},
		MinVersion:   tls.VersionTLS13,
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
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
				tlsConn, ok := c.(*tls.Conn)
				if !ok {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := tlsConn.HandshakeContext(ctx); err != nil {
					return
				}
				conn := &tlsFrameConn{c: tlsConn}
				sess := session.New(session.DefaultConfig(false))
				sess.OnControl(func(msgType byte, payload []byte) error {
					if msgType != control.TypeAuth {
						return nil
					}
					if onAuth != nil {
						return onAuth(ctx, sess, payload)
					}
					if err := sess.MarkEstablished(); err != nil {
						return err
					}
					return sess.HandleAuthOK(ctx)
				})
				_ = sess.Connect(ctx, conn)
				_ = sess.RunHandshake(ctx)
				_ = sess.ReadLoop(ctx)
			}(raw)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

type tlsFrameConn struct {
	c *tls.Conn
}

func (c *tlsFrameConn) Read(ctx context.Context) ([]byte, error) {
	if d, ok := ctx.Deadline(); ok {
		_ = c.c.SetReadDeadline(d)
		defer c.c.SetReadDeadline(time.Time{})
	}
	var length [4]byte
	if _, err := c.c.Read(length[:]); err != nil {
		// use framing identical to tlsstream via binary — fall through to stream helper
		return nil, err
	}
	// Reuse tlsstream framing by reading through transport package pattern:
	n := int(uint32(length[0])<<24 | uint32(length[1])<<16 | uint32(length[2])<<8 | uint32(length[3]))
	if n <= 0 || n > 1<<20 {
		return nil, fmt.Errorf("bad len")
	}
	buf := make([]byte, n)
	off := 0
	for off < n {
		nn, err := c.c.Read(buf[off:])
		off += nn
		if err != nil {
			return buf[:off], err
		}
	}
	return buf, nil
}

func (c *tlsFrameConn) Write(ctx context.Context, data []byte) error {
	if d, ok := ctx.Deadline(); ok {
		_ = c.c.SetWriteDeadline(d)
		defer c.c.SetWriteDeadline(time.Time{})
	}
	frame := make([]byte, 4+len(data))
	frame[0] = byte(len(data) >> 24)
	frame[1] = byte(len(data) >> 16)
	frame[2] = byte(len(data) >> 8)
	frame[3] = byte(len(data))
	copy(frame[4:], data)
	_, err := c.c.Write(frame)
	return err
}
func (c *tlsFrameConn) Close() error                       { return c.c.Close() }
func (c *tlsFrameConn) LocalAddr() net.Addr                { return c.c.LocalAddr() }
func (c *tlsFrameConn) RemoteAddr() net.Addr               { return c.c.RemoteAddr() }
func (c *tlsFrameConn) Profile() transport.Profile         { return transport.ProfileTLSTCP }
func (c *tlsFrameConn) SetReadDeadline(t time.Time) error  { return c.c.SetReadDeadline(t) }
func (c *tlsFrameConn) SetWriteDeadline(t time.Time) error { return c.c.SetWriteDeadline(t) }

func mustPort(addr string) int {
	_, p, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(p, "%d", &port)
	return port
}

func mustDeviceKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func catalogServer(t *testing.T, signed model.SignedCatalog, onIssue func(api.TicketIssueRequest)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/license/validate":
			_ = json.NewEncoder(w).Encode(api.LicenseValidateResponse{Valid: true})
		case "/api/v1/catalog":
			_ = json.NewEncoder(w).Encode(signed)
		case "/api/v1/ticket/issue":
			var req api.TicketIssueRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if onIssue != nil {
				onIssue(req)
			}
			_ = json.NewEncoder(w).Encode(api.TicketIssueResponse{AccessTicket: "tok"})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestProductionConnectorUsesECHConfig(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "cat-key-1", PrivateKey: priv}
	signed, err := signer.Sign(model.Catalog{
		Version: "1",
		Nodes: []model.NodeRegistryEntry{{
			NodeID: "n1", LocationID: "fi-hel", Enabled: true, Capacity: 10,
			SPKIPin: []byte{1}, Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: 1}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := catalogServer(t, signed, nil)
	defer ts.Close()

	capProv := &captureDialProvider{echPolicy: transport.ECHPreferred, echList: []byte{0xee}}
	reg := transport.NewRegistry()
	reg.Register(&captureTransport{
		profile:  transport.ProfileTLSTCP,
		provider: capProv,
		dialErr:  fmt.Errorf("stop after capture"),
	})
	// Also register as primary so racing hits our capture.
	reg.Register(&captureTransport{
		profile:  transport.ProfileQUICUDP,
		provider: capProv,
		dialErr:  fmt.Errorf("stop after capture"),
	})

	c := &connector.Connector{
		CP:                connector.NewControlPlaneClient(ts.URL),
		Registry:          reg,
		Provider:          capProv,
		CatalogVerifyKeys: catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"cat-key-1": pub}},
		ECHPolicy:         transport.ECHRequired,
		ECHConfigList:     []byte{0x01, 0x02, 0x03},
		Policy: failover.ConnectPolicy{
			MaxNodeAttempts: 1,
			TransportRacing: transport.RacingConfig{
				Primary: transport.ProfileQUICUDP, Fallback: transport.ProfileTLSTCP,
				FallbackDelay: time.Millisecond,
			},
			RetryDelay: time.Millisecond,
		},
	}
	_, _, _, err = c.OpenSession(context.Background(), connector.ConnectConfig{
		LicenseToken: "lic", DeviceID: "dev", LocationID: "fi-hel", Role: "user",
	})
	if err == nil {
		t.Fatal("expected dial failure after capture")
	}
	if !capProv.captured {
		t.Fatal("expected DialConfig capture")
	}
	if capProv.lastCfg.ECHPolicy != transport.ECHRequired {
		t.Fatalf("ECHPolicy=%q", capProv.lastCfg.ECHPolicy)
	}
	if len(capProv.lastCfg.ECHConfigList) == 0 {
		t.Fatal("expected non-nil ECHConfigList on dial")
	}
}

func TestNormalSessionDoesNotPerformVPNProbeConnection(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "cat-key-1", PrivateKey: priv}
	signed, err := signer.Sign(model.Catalog{
		Version: "1",
		Nodes: []model.NodeRegistryEntry{{
			NodeID: "n1", LocationID: "fi-hel", Enabled: true, Capacity: 10,
			SPKIPin: []byte{1}, Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: 1}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/license/validate":
			_ = json.NewEncoder(w).Encode(api.LicenseValidateResponse{Valid: true})
		case "/api/v1/catalog":
			_ = json.NewEncoder(w).Encode(signed)
		case "/api/v1/ticket/issue":
			_ = json.NewEncoder(w).Encode(api.TicketIssueResponse{AccessTicket: "tok"})
		default:
			t.Errorf("unexpected probe-like path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	c := &connector.Connector{
		CP:                connector.NewControlPlaneClient(ts.URL),
		CatalogVerifyKeys: catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"cat-key-1": pub}},
	}
	_, _, err = c.PrepareSelection(context.Background(), connector.ConnectConfig{
		LicenseToken: "lic", DeviceID: "dev", LocationID: "fi-hel",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if p == "/api/v1/probe" || p == "/probe" || p == "/vpn/probe" {
			t.Fatalf("PrepareSelection must not probe VPN: %s", p)
		}
	}
}

func TestProductionConnectorRejectsWrongSPKI(t *testing.T) {
	bundle, err := testutil.GenerateCertBundle("localhost")
	if err != nil {
		t.Fatal(err)
	}
	addr, closeFn := startTLSNode(t, bundle)
	defer closeFn()

	pub, priv, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "cat-key-1", PrivateKey: priv}
	wrongPin := make([]byte, 32)
	wrongPin[0] = 0xff
	signed, err := signer.Sign(model.Catalog{
		Version: "1",
		Nodes: []model.NodeRegistryEntry{{
			NodeID: "n1", LocationID: "fi-hel", Enabled: true, Capacity: 10,
			ServerName: bundle.ServerName, SPKIPin: wrongPin,
			Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: mustPort(addr), Profiles: []transport.Profile{transport.ProfileTLSTCP}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := catalogServer(t, signed, nil)
	defer ts.Close()

	reg := transport.NewRegistry()
	reg.Register(tlsstream.NewTransport())
	c := &connector.Connector{
		CP:                connector.NewControlPlaneClient(ts.URL),
		Registry:          reg,
		RequirePin:        true,
		CatalogVerifyKeys: catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"cat-key-1": pub}},
		Provider:          &staticProvider{pool: bundle.CAPool, sn: bundle.ServerName},
		Policy: failover.ConnectPolicy{
			MaxNodeAttempts: 1,
			TransportRacing: transport.RacingConfig{Primary: transport.ProfileTLSTCP, Fallback: transport.ProfileTLSTCP},
			RetryDelay:      time.Millisecond,
		},
	}
	_, _, _, err = c.OpenSession(context.Background(), connector.ConnectConfig{
		LicenseToken: "lic", DeviceID: "dev", LocationID: "fi-hel",
	})
	if err == nil {
		t.Fatal("expected SPKI pin rejection")
	}
}

func TestProductionConnectorAcceptsCorrectSPKI(t *testing.T) {
	bundle, err := testutil.GenerateCertBundle("localhost")
	if err != nil {
		t.Fatal(err)
	}
	addr, closeFn := startTLSNode(t, bundle)
	defer closeFn()
	pin := spkiPin(bundle.Cert)

	pub, priv, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "cat-key-1", PrivateKey: priv}
	signed, err := signer.Sign(model.Catalog{
		Version: "1",
		Nodes: []model.NodeRegistryEntry{{
			NodeID: "n1", LocationID: "fi-hel", Enabled: true, Capacity: 10,
			ServerName: bundle.ServerName, SPKIPin: pin,
			Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: mustPort(addr), Profiles: []transport.Profile{transport.ProfileTLSTCP}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := catalogServer(t, signed, nil)
	defer ts.Close()

	reg := transport.NewRegistry()
	reg.Register(tlsstream.NewTransport())
	c := &connector.Connector{
		CP:                connector.NewControlPlaneClient(ts.URL),
		Registry:          reg,
		RequirePin:        true,
		CatalogVerifyKeys: catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"cat-key-1": pub}},
		Provider:          &staticProvider{pool: bundle.CAPool, sn: bundle.ServerName},
		Policy: failover.ConnectPolicy{
			MaxNodeAttempts: 1,
			TransportRacing: transport.RacingConfig{Primary: transport.ProfileTLSTCP, Fallback: transport.ProfileTLSTCP},
			RetryDelay:      time.Millisecond,
		},
	}
	sess, conn, node, err := c.OpenSession(context.Background(), connector.ConnectConfig{
		LicenseToken: "lic", DeviceID: "dev", LocationID: "fi-hel",
		DevicePrivateKey: mustDeviceKey(t),
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer conn.Close()
	if node.NodeID != "n1" || sess == nil {
		t.Fatalf("node=%s sess=%v", node.NodeID, sess)
	}
	if sess.State() != session.StateEstablished {
		t.Fatalf("expected ESTABLISHED, got %s", sess.State())
	}
}

type staticProvider struct {
	pool *x509.CertPool
	sn   string
	pin  []byte
}

func (p *staticProvider) RootCAs() interface{}                           { return p.pool }
func (p *staticProvider) ServerNameFor(model.NodeRegistryEntry) string   { return p.sn }
func (p *staticProvider) PinnedPubKeyFor(model.NodeRegistryEntry) []byte { return p.pin }
func (p *staticProvider) ECHPolicy() transport.ECHPolicy                 { return "" }
func (p *staticProvider) ECHConfigList() []byte                          { return nil }

// TestConnectorDoesNotAutomaticallyCrossLocations: FI NodeA down, DE NodeB up,
// DesiredLocation=FI → ErrNoHealthyNodes (not NodeB).
func TestConnectorDoesNotAutomaticallyCrossLocations(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "cat-key-1", PrivateKey: priv}
	signed, err := signer.Sign(model.Catalog{
		Version: "1",
		Nodes: []model.NodeRegistryEntry{
			{
				NodeID: "fi-a", LocationID: "fi-hel", Enabled: true, Capacity: 10,
				Health: model.HealthInfo{Healthy: false}, LastSeen: time.Now(),
				SPKIPin:   []byte{1},
				Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: 1, Profiles: []transport.Profile{transport.ProfileTLSTCP}}},
			},
			{
				NodeID: "de-b", LocationID: "de-fra", Enabled: true, Capacity: 10,
				SPKIPin:   []byte{2},
				Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: 2, Profiles: []transport.Profile{transport.ProfileTLSTCP}}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := catalogServer(t, signed, nil)
	defer ts.Close()

	reg := transport.NewRegistry()
	reg.Register(tlsstream.NewTransport())
	c := &connector.Connector{
		CP:                connector.NewControlPlaneClient(ts.URL),
		Registry:          reg,
		CatalogVerifyKeys: catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"cat-key-1": pub}},
		Policy: failover.ConnectPolicy{
			MaxNodeAttempts: 3,
			TransportRacing: transport.RacingConfig{Primary: transport.ProfileTLSTCP, Fallback: transport.ProfileTLSTCP},
			RetryDelay:      time.Millisecond,
		},
	}
	_, _, node, err := c.OpenSession(context.Background(), connector.ConnectConfig{
		LicenseToken: "lic", DeviceID: "dev", LocationID: "fi-hel", Role: "user",
		DevicePrivateKey: mustDeviceKey(t),
	})
	if err == nil {
		t.Fatalf("expected ErrNoHealthyNodes, got node %s", node.NodeID)
	}
	if !errors.Is(err, nvperr.ErrNoHealthyNodes) {
		t.Fatalf("want ErrNoHealthyNodes, got %v", err)
	}
	if node.NodeID == "de-b" {
		t.Fatal("must not select cross-location de-b")
	}
}

// TestNodeScopedTicketPreventsFailoverToOtherNode: AllowedNodeIDs=[A], A down B up → fail.
func TestNodeScopedTicketPreventsFailoverToOtherNode(t *testing.T) {
	bundle, err := testutil.GenerateCertBundle("localhost")
	if err != nil {
		t.Fatal(err)
	}
	addrB, closeB := startTLSNode(t, bundle)
	defer closeB()

	pub, priv, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "cat-key-1", PrivateKey: priv}
	pin := spkiPin(bundle.Cert)
	signed, err := signer.Sign(model.Catalog{
		Version: "1",
		Nodes: []model.NodeRegistryEntry{
			{
				NodeID: "node-a", LocationID: "fi-hel", Enabled: true, Capacity: 10, SPKIPin: pin,
				ServerName: bundle.ServerName,
				Endpoints:  []transport.Endpoint{{Host: "127.0.0.1", Port: 1, Profiles: []transport.Profile{transport.ProfileTLSTCP}}},
			},
			{
				NodeID: "node-b", LocationID: "fi-hel", Enabled: true, Capacity: 10, SPKIPin: pin,
				ServerName: bundle.ServerName,
				Endpoints:  []transport.Endpoint{{Host: "127.0.0.1", Port: mustPort(addrB), Profiles: []transport.Profile{transport.ProfileTLSTCP}}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := catalogServer(t, signed, nil)
	defer ts.Close()

	reg := transport.NewRegistry()
	reg.Register(tlsstream.NewTransport())
	c := &connector.Connector{
		CP:                connector.NewControlPlaneClient(ts.URL),
		Registry:          reg,
		CatalogVerifyKeys: catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"cat-key-1": pub}},
		Provider:          &staticProvider{pool: bundle.CAPool, sn: bundle.ServerName},
		Policy: failover.ConnectPolicy{
			MaxNodeAttempts: 3,
			AllowedNodeIDs:  []string{"node-a"},
			TransportRacing: transport.RacingConfig{Primary: transport.ProfileTLSTCP, Fallback: transport.ProfileTLSTCP},
			RetryDelay:      time.Millisecond,
		},
	}
	_, _, node, err := c.OpenSession(context.Background(), connector.ConnectConfig{
		LicenseToken: "lic", DeviceID: "dev", LocationID: "fi-hel", Role: "user",
		DevicePrivateKey: mustDeviceKey(t),
	})
	if err == nil {
		t.Fatalf("expected scoped failover failure, got node %s", node.NodeID)
	}
	if node.NodeID == "node-b" {
		t.Fatal("must not failover to node-b outside NodeScope")
	}
}

// TestLocationScopedTicketAllowsSameLocationNodeFailover: empty NodeScope, A down B up → B.
func TestLocationScopedTicketAllowsSameLocationNodeFailover(t *testing.T) {
	bundle, err := testutil.GenerateCertBundle("localhost")
	if err != nil {
		t.Fatal(err)
	}
	addrB, closeB := startTLSNode(t, bundle)
	defer closeB()

	pub, priv, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "cat-key-1", PrivateKey: priv}
	pin := spkiPin(bundle.Cert)
	signed, err := signer.Sign(model.Catalog{
		Version: "1",
		Nodes: []model.NodeRegistryEntry{
			{
				NodeID: "node-a", LocationID: "fi-hel", Enabled: true, Capacity: 10, SPKIPin: pin,
				ServerName: bundle.ServerName,
				Endpoints:  []transport.Endpoint{{Host: "127.0.0.1", Port: 1, Profiles: []transport.Profile{transport.ProfileTLSTCP}}},
			},
			{
				NodeID: "node-b", LocationID: "fi-hel", Enabled: true, Capacity: 10, SPKIPin: pin,
				ServerName: bundle.ServerName,
				Endpoints:  []transport.Endpoint{{Host: "127.0.0.1", Port: mustPort(addrB), Profiles: []transport.Profile{transport.ProfileTLSTCP}}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := catalogServer(t, signed, nil)
	defer ts.Close()

	reg := transport.NewRegistry()
	reg.Register(tlsstream.NewTransport())
	c := &connector.Connector{
		CP:                connector.NewControlPlaneClient(ts.URL),
		Registry:          reg,
		CatalogVerifyKeys: catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"cat-key-1": pub}},
		Provider:          &staticProvider{pool: bundle.CAPool, sn: bundle.ServerName},
		Policy: failover.ConnectPolicy{
			MaxNodeAttempts: 3,
			TransportRacing: transport.RacingConfig{Primary: transport.ProfileTLSTCP, Fallback: transport.ProfileTLSTCP},
			RetryDelay:      time.Millisecond,
		},
	}
	_, conn, node, err := c.OpenSession(context.Background(), connector.ConnectConfig{
		LicenseToken: "lic", DeviceID: "dev", LocationID: "fi-hel", Role: "user",
		DevicePrivateKey: mustDeviceKey(t),
	})
	if err != nil {
		t.Fatalf("location-scoped same-location failover: %v", err)
	}
	defer conn.Close()
	if node.NodeID != "node-b" {
		t.Fatalf("expected node-b, got %s", node.NodeID)
	}
}

func TestConnectorNodeFailover(t *testing.T) {
	bundle, err := testutil.GenerateCertBundle("localhost")
	if err != nil {
		t.Fatal(err)
	}
	addrB, closeB := startTLSNode(t, bundle)
	defer closeB()

	pub, priv, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "cat-key-1", PrivateKey: priv}
	pin := spkiPin(bundle.Cert)
	signed, err := signer.Sign(model.Catalog{
		Version: "1",
		Nodes: []model.NodeRegistryEntry{
			{
				NodeID: "node-a", LocationID: "fi-hel", Enabled: true, Capacity: 10, SPKIPin: pin,
				ServerName: bundle.ServerName,
				Endpoints:  []transport.Endpoint{{Host: "127.0.0.1", Port: 1, Profiles: []transport.Profile{transport.ProfileTLSTCP}}},
			},
			{
				NodeID: "node-b", LocationID: "fi-hel", Enabled: true, Capacity: 10, SPKIPin: pin,
				ServerName: bundle.ServerName,
				Endpoints:  []transport.Endpoint{{Host: "127.0.0.1", Port: mustPort(addrB), Profiles: []transport.Profile{transport.ProfileTLSTCP}}},
			},
			{
				NodeID: "node-c", LocationID: "fi-hel", Enabled: true, Capacity: 10, SPKIPin: pin,
				ServerName: bundle.ServerName,
				Endpoints:  []transport.Endpoint{{Host: "127.0.0.1", Port: 2, Profiles: []transport.Profile{transport.ProfileTLSTCP}}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := catalogServer(t, signed, nil)
	defer ts.Close()

	reg := transport.NewRegistry()
	reg.Register(tlsstream.NewTransport())
	c := &connector.Connector{
		CP:                connector.NewControlPlaneClient(ts.URL),
		Registry:          reg,
		CatalogVerifyKeys: catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"cat-key-1": pub}},
		Provider:          &staticProvider{pool: bundle.CAPool, sn: bundle.ServerName},
		Policy: failover.ConnectPolicy{
			MaxNodeAttempts: 3,
			TransportRacing: transport.RacingConfig{Primary: transport.ProfileTLSTCP, Fallback: transport.ProfileTLSTCP},
			RetryDelay:      time.Millisecond,
		},
	}
	_, conn, node, err := c.OpenSession(context.Background(), connector.ConnectConfig{
		LicenseToken: "lic", DeviceID: "dev", LocationID: "fi-hel", Role: "user",
		DevicePrivateKey: mustDeviceKey(t),
	})
	if err != nil {
		t.Fatalf("failover: %v", err)
	}
	defer conn.Close()
	if node.NodeID != "node-b" {
		t.Fatalf("expected node-b, got %s", node.NodeID)
	}
}

func TestConnectorTransportAndNodeFailover(t *testing.T) {
	bundle, err := testutil.GenerateCertBundle("localhost")
	if err != nil {
		t.Fatal(err)
	}
	addrB, closeB := startTLSNode(t, bundle)
	defer closeB()

	pub, priv, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "cat-key-1", PrivateKey: priv}
	pin := spkiPin(bundle.Cert)
	signed, err := signer.Sign(model.Catalog{
		Version: "1",
		Nodes: []model.NodeRegistryEntry{
			{
				NodeID: "node-a", LocationID: "fi-hel", Enabled: true, Capacity: 5, SPKIPin: pin,
				ServerName: bundle.ServerName,
				// QUIC will fail (nothing listening UDP); TLS port also dead.
				Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: 1, Profiles: []transport.Profile{transport.ProfileQUICUDP, transport.ProfileTLSTCP}}},
			},
			{
				NodeID: "node-b", LocationID: "fi-hel", Enabled: true, Capacity: 10, SPKIPin: pin,
				ServerName: bundle.ServerName,
				Endpoints:  []transport.Endpoint{{Host: "127.0.0.1", Port: mustPort(addrB), Profiles: []transport.Profile{transport.ProfileQUICUDP, transport.ProfileTLSTCP}}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := catalogServer(t, signed, nil)
	defer ts.Close()

	reg := transport.NewRegistry()
	reg.Register(tlsstream.NewTransport())
	reg.Register(&captureTransport{profile: transport.ProfileQUICUDP, dialErr: fmt.Errorf("quic down")})

	c := &connector.Connector{
		CP:                connector.NewControlPlaneClient(ts.URL),
		Registry:          reg,
		CatalogVerifyKeys: catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"cat-key-1": pub}},
		Provider:          &staticProvider{pool: bundle.CAPool, sn: bundle.ServerName},
		Policy: failover.ConnectPolicy{
			MaxNodeAttempts: 2,
			TransportRacing: transport.RacingConfig{
				Primary: transport.ProfileQUICUDP, Fallback: transport.ProfileTLSTCP,
				FallbackDelay: 20 * time.Millisecond,
			},
			RetryDelay: time.Millisecond,
		},
	}
	_, conn, node, err := c.OpenSession(context.Background(), connector.ConnectConfig{
		LicenseToken: "lic", DeviceID: "dev", LocationID: "fi-hel",
		DevicePrivateKey: mustDeviceKey(t),
	})
	if err != nil {
		t.Fatalf("transport+node failover: %v", err)
	}
	defer conn.Close()
	if node.NodeID != "node-b" {
		t.Fatalf("expected node-b, got %s", node.NodeID)
	}
	if conn.Profile() != transport.ProfileTLSTCP {
		t.Fatalf("expected TLS fallback, got %s", conn.Profile())
	}
}
