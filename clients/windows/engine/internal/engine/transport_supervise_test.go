package engine_test

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyxveil/client-windows/internal/engine"
	"github.com/nyxveil/client-windows/internal/netcfg"
	"github.com/nyxveil/client-windows/internal/state"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
)

// probeConn blocks in Read until Close or ctx cancel; records concurrent readers.
type probeConn struct {
	nopConn
	readers atomic.Int32
	closed  chan struct{}
}

func newProbeConn() *probeConn {
	return &probeConn{closed: make(chan struct{})}
}

func (c *probeConn) Read(ctx context.Context) ([]byte, error) {
	c.readers.Add(1)
	defer c.readers.Add(-1)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closed:
		return nil, io.EOF
	}
}

func (c *probeConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}

type assertReaderDuringApply struct {
	engine.NoopApplier
	conn        *probeConn
	sawReader   atomic.Bool
	applyDelay  time.Duration
}

func (a *assertReaderDuringApply) ApplyTunnel(p *engine.Plan) error {
	time.Sleep(a.applyDelay)
	if a.conn.readers.Load() > 0 {
		a.sawReader.Store(true)
	}
	p.Gate.AddressApplied = true
	p.Gate.DNSApplied = true
	p.Gate.RoutesApplied = true
	p.DefaultViaTUN = true
	return nil
}

func (a *assertReaderDuringApply) VerifyTunnel(p *engine.Plan) error { return nil }

func TestTransportReaderArmedDuringApplyTunnel(t *testing.T) {
	raw, keys, dpriv := catalogBundle(t)
	conn := newProbeConn()
	routes := &assertReaderDuringApply{conn: conn, applyDelay: 200 * time.Millisecond}
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
			sess := session.New(session.DefaultConfig(true))
			if err := sess.Connect(ctx, conn); err != nil {
				return nil, nil, model.NodeRegistryEntry{}, err
			}
			return sess, conn, model.NodeRegistryEntry{NodeID: "n1", LocationID: "fi-helsinki"}, nil
		},
	})
	if err := mgr.Connect(context.Background(), connectReq(raw, keys, dpriv)); err != nil {
		t.Fatal(err)
	}
	if !routes.sawReader.Load() {
		t.Fatal("expected active transport ReadLoop during ApplyTunnel (1.0.3 gap)")
	}
	st, _ := mgr.State.Get()
	if st != state.Connected {
		t.Fatalf("state=%s", st)
	}
	// Hold Connected briefly; transport must not tear down without cause.
	time.Sleep(300 * time.Millisecond)
	st, last := mgr.State.Get()
	if st != state.Connected {
		t.Fatalf("Connected not stable: state=%s last=%q", st, last)
	}
	_ = mgr.Disconnect(context.Background())
}

func TestTransportEOFDuringApplyFailsClosedBeforeConnected(t *testing.T) {
	raw, keys, dpriv := catalogBundle(t)
	conn := newProbeConn()
	routes := &assertReaderDuringApply{conn: conn, applyDelay: 400 * time.Millisecond}
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
			sess := session.New(session.DefaultConfig(true))
			if err := sess.Connect(ctx, conn); err != nil {
				return nil, nil, model.NodeRegistryEntry{}, err
			}
			return sess, conn, model.NodeRegistryEntry{NodeID: "n1", LocationID: "fi-helsinki"}, nil
		},
	})
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = conn.Close()
	}()
	err := mgr.Connect(context.Background(), connectReq(raw, keys, dpriv))
	if err == nil {
		t.Fatal("expected Connect fail when transport dies during ApplyTunnel")
	}
	st, last := mgr.State.Get()
	if st == state.Connected {
		t.Fatal("must not emit Connected after transport death during apply")
	}
	if last == "" && st != state.Error {
		t.Fatalf("expected Fail/LastError, state=%s last=%q err=%v", st, last, err)
	}
}
