package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/nyxveil/client-windows/internal/engine"
	"github.com/nyxveil/client-windows/internal/netcfg"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
	"github.com/nyxveil/nvp/core/tunnel"
)

// holeApplier reproduces the 1.0.1 failure mode: ApplyTunnel returns nil without
// confirming address/routes/DNS, yet Connect used to set Connected anyway.
type holeApplier struct {
	engine.NoopApplier
}

func (h *holeApplier) ApplyTunnel(p *engine.Plan) error {
	h.Steps = append(h.Steps, "tunnel-noop-success")
	// Deliberately do NOT set DefaultViaTUN / Gate.* вЂ” network apply absent.
	return nil
}

func TestConnectedRefusedWhenNetworkApplyAbsent(t *testing.T) {
	raw, keys, dpriv := catalogBundle(t)
	routes := &holeApplier{}
	routes.SkipMarkApplied = true
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
			return openTestSession(ctx, "n1", "fi-helsinki")
		},
	})
	err := mgr.Connect(context.Background(), connectReq(raw, keys, dpriv))
	if err == nil {
		t.Fatal("expected Connect to fail when network apply absent")
	}
	if !strings.Contains(err.Error(), "network apply absent") && !strings.Contains(err.Error(), "dataplane incomplete") && !strings.Contains(err.Error(), "verify") {
		t.Fatalf("want verify/dataplane error, got %v", err)
	}
	st, _ := mgr.State.Get()
	if st.String() == "Connected" {
		t.Fatal("Connected must not be set without successful network apply")
	}
}

func TestConnectedRequiresFullDataplaneGate(t *testing.T) {
	raw, keys, dpriv := catalogBundle(t)
	mgr := engine.NewManager(engine.Options{
		TUN: &instantTUN{},
		TypeConfigFn: func(ctx context.Context) (*netcfg.Message, error) {
			return &netcfg.Message{
				VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1280,
				Gateway: "10.66.0.1", DNSServers: []string{"10.66.0.1"},
			}, nil
		},
		SessionOpener: func(ctx context.Context, _ model.Catalog, _ engine.ConnectRequest) (*session.Session, transport.Conn, model.NodeRegistryEntry, error) {
			return openTestSession(ctx, "n1", "fi-helsinki")
		},
	})
	if err := mgr.Connect(context.Background(), connectReq(raw, keys, dpriv)); err != nil {
		t.Fatal(err)
	}
	st, last := mgr.State.Get()
	if st.String() != "Connected" {
		t.Fatalf("state=%s last=%q", st.String(), last)
	}
	// Double connect is idempotent (no "already connected" error).
	if err := mgr.Connect(context.Background(), connectReq(raw, keys, dpriv)); err != nil {
		t.Fatalf("idempotent Connect: %v", err)
	}
	_ = mgr.Disconnect(context.Background())
}

func TestTunOpenFailureNeverConnected(t *testing.T) {
	raw, keys, dpriv := catalogBundle(t)
	mgr := engine.NewManager(engine.Options{
		TUN: failingTUN{},
		TypeConfigFn: func(ctx context.Context) (*netcfg.Message, error) {
			return &netcfg.Message{
				VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1280,
				Gateway: "10.66.0.1", DNSServers: []string{"10.66.0.1"},
			}, nil
		},
		SessionOpener: func(ctx context.Context, _ model.Catalog, _ engine.ConnectRequest) (*session.Session, transport.Conn, model.NodeRegistryEntry, error) {
			return openTestSession(ctx, "n1", "")
		},
	})
	err := mgr.Connect(context.Background(), connectReq(raw, keys, dpriv))
	if err == nil {
		t.Fatal("expected TUN failure")
	}
	st, _ := mgr.State.Get()
	if st.String() == "Connected" {
		t.Fatal("Connected after TUN failure")
	}
}

type failingTUN struct{}

func (failingTUN) Open(context.Context, tunnel.Config) (tunnel.Device, error) {
	return nil, engine.ErrNotLinked
}
