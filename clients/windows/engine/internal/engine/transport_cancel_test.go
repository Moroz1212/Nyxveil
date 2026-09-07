package engine_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyxveil/client-windows/internal/engine"
	"github.com/nyxveil/client-windows/internal/netcfg"
	"github.com/nyxveil/nvp/core/controlplane/catalog"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/failover"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
	"github.com/nyxveil/nvp/core/tunnel"
)

func catalogBundle(t *testing.T) (raw []byte, keys map[string]string, dpriv ed25519.PrivateKey) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "k1", PrivateKey: priv}
	signed, err := signer.Sign(model.Catalog{
		Version: "1",
		Nodes: []model.NodeRegistryEntry{{
			NodeID: "a", LocationID: "fi-hel", Enabled: true, Capacity: 10,
			ServerName: "127.0.0.1", SPKIPin: make([]byte, 32),
			Endpoints: []transport.Endpoint{{
				Host: "127.0.0.1", Port: 1,
				Profiles: []transport.Profile{transport.ProfileTLSTCP, transport.ProfileQUICUDP},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(signed)
	_, dpriv, _ = ed25519.GenerateKey(nil)
	keys = map[string]string{"k1": base64.StdEncoding.EncodeToString(pub)}
	return raw, keys, dpriv
}

func connectReq(raw []byte, keys map[string]string, dpriv ed25519.PrivateKey) engine.ConnectRequest {
	return engine.ConnectRequest{
		DesiredLocationID: "fi-hel",
		AccessTicket:      "x",
		SignedCatalogJSON: raw,
		CatalogKeys:       keys,
		DevicePrivateKey:  dpriv,
		ControlPlaneHost:  "203.0.113.1",
	}
}

func assertDisconnectUnblocks(t *testing.T, mgr *engine.Manager) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		_ = mgr.Disconnect(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Disconnect blocked during Connect")
	}
}

func TestRegistryHasQUICAndTLS(t *testing.T) {
	mgr := engine.NewManager(engine.Options{})
	reg := mgr.Connector.Registry
	if _, ok := reg.Get(transport.ProfileQUICUDP); !ok {
		t.Fatal("QUIC transport not registered")
	}
	if _, ok := reg.Get(transport.ProfileTLSTCP); !ok {
		t.Fatal("TLS transport not registered")
	}
	rc := transport.DefaultRacingConfig()
	if rc.Primary != transport.ProfileQUICUDP {
		t.Fatalf("primary=%s want quic", rc.Primary)
	}
	if rc.Fallback != transport.ProfileTLSTCP {
		t.Fatalf("fallback=%s want tls", rc.Fallback)
	}
}

func TestConnectCancelDuringDial(t *testing.T) {
	raw, keys, dpriv := catalogBundle(t)
	mgr := engine.NewManager(engine.Options{
		Policy: failover.ConnectPolicy{MaxNodeAttempts: 8, RetryDelay: time.Hour},
	})
	var started atomic.Bool
	go func() {
		started.Store(true)
		_ = mgr.Connect(context.Background(), connectReq(raw, keys, dpriv))
	}()
	for !started.Load() {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(40 * time.Millisecond)
	assertDisconnectUnblocks(t, mgr)
}

func TestConnectCancelDuringAuth(t *testing.T) {
	raw, keys, dpriv := catalogBundle(t)
	entered := make(chan struct{})
	mgr := engine.NewManager(engine.Options{
		SessionOpener: func(ctx context.Context, _ model.Catalog, _ engine.ConnectRequest) (*session.Session, transport.Conn, model.NodeRegistryEntry, error) {
			close(entered)
			<-ctx.Done()
			return nil, nil, model.NodeRegistryEntry{}, ctx.Err()
		},
	})
	go func() { _ = mgr.Connect(context.Background(), connectReq(raw, keys, dpriv)) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("auth phase not entered")
	}
	assertDisconnectUnblocks(t, mgr)
}

func TestConnectCancelDuringTypeConfig(t *testing.T) {
	raw, keys, dpriv := catalogBundle(t)
	entered := make(chan struct{})
	mgr := engine.NewManager(engine.Options{
		Hooks: &engine.LifecycleHooks{
			OnEnterPhase: func(phase engine.ConnectPhase, ctx context.Context) error {
				if phase == engine.PhaseTypeConfig {
					close(entered)
					<-ctx.Done()
					return ctx.Err()
				}
				return nil
			},
		},
		SessionOpener: func(ctx context.Context, _ model.Catalog, _ engine.ConnectRequest) (*session.Session, transport.Conn, model.NodeRegistryEntry, error) {
			return openTestSession(ctx, "a", "")
		},
	})
	go func() { _ = mgr.Connect(context.Background(), connectReq(raw, keys, dpriv)) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("typeconfig phase not entered")
	}
	assertDisconnectUnblocks(t, mgr)
}

func TestConnectCancelDuringTUN(t *testing.T) {
	raw, keys, dpriv := catalogBundle(t)
	entered := make(chan struct{})
	mgr := engine.NewManager(engine.Options{
		TUN: &blockingTUN{entered: entered},
		TypeConfigFn: func(ctx context.Context) (*netcfg.Message, error) {
			return &netcfg.Message{
				VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1280,
				Gateway: "10.66.0.1", DNSServers: []string{"10.66.0.1"},
			}, nil
		},
		SessionOpener: func(ctx context.Context, _ model.Catalog, _ engine.ConnectRequest) (*session.Session, transport.Conn, model.NodeRegistryEntry, error) {
			return openTestSession(ctx, "a", "")
		},
	})
	go func() { _ = mgr.Connect(context.Background(), connectReq(raw, keys, dpriv)) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("tun phase not entered")
	}
	assertDisconnectUnblocks(t, mgr)
}

func TestDisconnectRestoresRoutesAfterConnectTeardown(t *testing.T) {
	raw, keys, dpriv := catalogBundle(t)
	routes := &RecordingApplier{}
	mgr := engine.NewManager(engine.Options{
		Routes: routes,
		TUN:    &instantTUN{},
		TypeConfigFn: func(ctx context.Context) (*netcfg.Message, error) {
			return &netcfg.Message{
				VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1280,
				Gateway: "10.66.0.1", DNSServers: []string{"10.66.0.1"},
			}, nil
		},
		SessionOpener: func(ctx context.Context, _ model.Catalog, _ engine.ConnectRequest) (*session.Session, transport.Conn, model.NodeRegistryEntry, error) {
			return openTestSession(ctx, "a", "")
		},
	})
	if err := mgr.Connect(context.Background(), connectReq(raw, keys, dpriv)); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := mgr.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !containsAll(routes.Steps, "capture", "bypass", "tunnel") {
		t.Fatalf("missing apply steps: %v", routes.Steps)
	}
	if !containsAll(routes.Steps, "undo:tunnel", "undo:bypass", "undo:capture") && !containsStep(routes.Steps, "restore") {
		// RecordingApplier Restore undoes stack with undo: prefixes
		joined := routes.Steps
		foundUndo := false
		for _, s := range joined {
			if len(s) >= 5 && s[:5] == "undo:" {
				foundUndo = true
				break
			}
		}
		if !foundUndo {
			t.Fatalf("expected route restore on disconnect, steps=%v", routes.Steps)
		}
	}
}

func containsStep(steps []string, want string) bool {
	for _, s := range steps {
		if s == want {
			return true
		}
	}
	return false
}

type blockingTUN struct{ entered chan struct{} }

func (b *blockingTUN) Open(ctx context.Context, _ tunnel.Config) (tunnel.Device, error) {
	close(b.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

type instantTUN struct{}

func (instantTUN) Open(ctx context.Context, _ tunnel.Config) (tunnel.Device, error) {
	return &nopDevice{}, nil
}

type nopConn struct{}

func (nopConn) Read(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (nopConn) Write(context.Context, []byte) error { return nil }
func (nopConn) Close() error                        { return nil }
func (nopConn) LocalAddr() net.Addr                 { return &net.TCPAddr{} }
func (nopConn) RemoteAddr() net.Addr                { return &net.TCPAddr{} }
func (nopConn) Profile() transport.Profile          { return transport.ProfileTLSTCP }
func (nopConn) SetReadDeadline(time.Time) error     { return nil }
func (nopConn) SetWriteDeadline(time.Time) error    { return nil }

// openTestSession returns a Session with conn bound (required now that Connect arms
// ReadLoop before ApplyTunnel). Skips AUTH/handshake for unit tests.
func openTestSession(ctx context.Context, nodeID, loc string) (*session.Session, transport.Conn, model.NodeRegistryEntry, error) {
	conn := nopConn{}
	sess := session.New(session.DefaultConfig(true))
	if err := sess.Connect(ctx, conn); err != nil {
		return nil, nil, model.NodeRegistryEntry{}, err
	}
	return sess, conn, model.NodeRegistryEntry{NodeID: nodeID, LocationID: loc}, nil
}

type nopDevice struct{}

func (*nopDevice) Read([]byte) (int, error)  { return 0, context.Canceled }
func (*nopDevice) Write([]byte) (int, error) { return 0, nil }
func (*nopDevice) Close() error              { return nil }
func (*nopDevice) Name() string              { return "Nyxveil" }
func (*nopDevice) MTU() int                  { return 1280 }

