package engine_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyxveil/client-windows/internal/engine"
	"github.com/nyxveil/client-windows/internal/ipc"
	"github.com/nyxveil/client-windows/internal/netcfg"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
)

func TestDisconnectWhileReconnectWaitingForTicketDoesNotReconnect(t *testing.T) {
	waiting := make(chan struct{}, 1)
	release := make(chan string, 1)
	var connects atomic.Int32
	raw, keys, dpriv := catalogBundle(t)
	mgr := engine.NewManager(engine.Options{
		RequestTicket: func(ctx context.Context, need ipc.NeedAccessTicket) (string, error) {
			select {
			case waiting <- struct{}{}:
			default:
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case t := <-release:
				return t, nil
			}
		},
		TUN: &instantTUN{},
		TypeConfigFn: func(ctx context.Context) (*netcfg.Message, error) {
			return &netcfg.Message{
				VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1280,
				Gateway: "10.66.0.1", DNSServers: []string{"10.66.0.1"},
			}, nil
		},
		SessionOpener: func(ctx context.Context, _ model.Catalog, _ engine.ConnectRequest) (*session.Session, transport.Conn, model.NodeRegistryEntry, error) {
			connects.Add(1)
			return openTestSession(ctx, "n1", "fi-hel")
		},
	})
	if err := mgr.Connect(context.Background(), connectReq(raw, keys, dpriv)); err != nil {
		t.Fatal(err)
	}
	first := connects.Load()
	lostDone := make(chan struct{})
	go func() {
		engine.TriggerSessionLostForTest(mgr)
		close(lostDone)
	}()
	select {
	case <-waiting:
	case <-time.After(3 * time.Second):
		t.Fatal("ticket wait not entered")
	}
	_ = mgr.Disconnect(context.Background())
	select {
	case release <- "should-not-use":
	default:
	}
	select {
	case <-lostDone:
	case <-time.After(3 * time.Second):
		t.Fatal("onSessionLost did not finish after Disconnect")
	}
	if connects.Load() > first {
		t.Fatalf("reconnect Connect after Disconnect during ticket wait (connects=%d first=%d)", connects.Load(), first)
	}
	st, _ := mgr.State.Get()
	if st.String() == "Connected" || st.String() == "Reconnecting" {
		t.Fatalf("state=%s after Disconnect during ticket wait", st)
	}
}

func TestDisconnectAfterTicketBeforeReconnectDoesNotReconnect(t *testing.T) {
	var connects atomic.Int32
	raw, keys, dpriv := catalogBundle(t)
	gate := make(chan struct{})
	engine.AfterReconnectTicketForTest = func() { <-gate }
	t.Cleanup(func() { engine.AfterReconnectTicketForTest = nil })

	mgr := engine.NewManager(engine.Options{
		RequestTicket: func(ctx context.Context, need ipc.NeedAccessTicket) (string, error) {
			return "fresh-ticket", nil
		},
		TUN: &instantTUN{},
		TypeConfigFn: func(ctx context.Context) (*netcfg.Message, error) {
			return &netcfg.Message{
				VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1280,
				Gateway: "10.66.0.1", DNSServers: []string{"10.66.0.1"},
			}, nil
		},
		SessionOpener: func(ctx context.Context, _ model.Catalog, _ engine.ConnectRequest) (*session.Session, transport.Conn, model.NodeRegistryEntry, error) {
			connects.Add(1)
			return openTestSession(ctx, "n1", "fi-hel")
		},
	})
	if err := mgr.Connect(context.Background(), connectReq(raw, keys, dpriv)); err != nil {
		t.Fatal(err)
	}
	first := connects.Load()
	done := make(chan struct{})
	go func() {
		engine.TriggerSessionLostForTest(mgr)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond) // ticket returned; blocked in AfterReconnectTicketForTest
	_ = mgr.Disconnect(context.Background())
	close(gate)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("onSessionLost did not finish")
	}
	time.Sleep(50 * time.Millisecond)
	if connects.Load() > first {
		t.Fatalf("reconnect Connect after Disconnect post-ticket (connects=%d first=%d)", connects.Load(), first)
	}
}

func TestSessionLostAfterManualDisconnectDoesNotReconnect(t *testing.T) {
	var tickets atomic.Int32
	raw, keys, dpriv := catalogBundle(t)
	mgr := engine.NewManager(engine.Options{
		RequestTicket: func(ctx context.Context, need ipc.NeedAccessTicket) (string, error) {
			tickets.Add(1)
			return "t", nil
		},
		TUN: &instantTUN{},
		TypeConfigFn: func(ctx context.Context) (*netcfg.Message, error) {
			return &netcfg.Message{
				VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1280,
				Gateway: "10.66.0.1", DNSServers: []string{"10.66.0.1"},
			}, nil
		},
		SessionOpener: func(ctx context.Context, _ model.Catalog, _ engine.ConnectRequest) (*session.Session, transport.Conn, model.NodeRegistryEntry, error) {
			return openTestSession(ctx, "n1", "fi-hel")
		},
	})
	if err := mgr.Connect(context.Background(), connectReq(raw, keys, dpriv)); err != nil {
		t.Fatal(err)
	}
	_ = mgr.Disconnect(context.Background())
	engine.TriggerSessionLostForTest(mgr)
	time.Sleep(100 * time.Millisecond)
	if tickets.Load() != 0 {
		t.Fatal("RequestTicket called after manual Disconnect")
	}
}

func TestRepeatedSessionLossSingleReconnect(t *testing.T) {
	waiting := make(chan struct{}, 2)
	release := make(chan string)
	var tickets atomic.Int32
	raw, keys, dpriv := catalogBundle(t)
	mgr := engine.NewManager(engine.Options{
		RequestTicket: func(ctx context.Context, need ipc.NeedAccessTicket) (string, error) {
			tickets.Add(1)
			waiting <- struct{}{}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case t := <-release:
				return t, nil
			}
		},
		TUN: &instantTUN{},
		TypeConfigFn: func(ctx context.Context) (*netcfg.Message, error) {
			return &netcfg.Message{
				VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1280,
				Gateway: "10.66.0.1", DNSServers: []string{"10.66.0.1"},
			}, nil
		},
		SessionOpener: func(ctx context.Context, _ model.Catalog, _ engine.ConnectRequest) (*session.Session, transport.Conn, model.NodeRegistryEntry, error) {
			return openTestSession(ctx, "n1", "fi-hel")
		},
	})
	if err := mgr.Connect(context.Background(), connectReq(raw, keys, dpriv)); err != nil {
		t.Fatal(err)
	}
	go engine.TriggerSessionLostForTest(mgr)
	select {
	case <-waiting:
	case <-time.After(3 * time.Second):
		t.Fatal("ticket wait not entered")
	}
	engine.TriggerSessionLostForTest(mgr) // second loss while first reconnect in flight (sync; must not start another ticket)
	time.Sleep(50 * time.Millisecond)
	if tickets.Load() != 1 {
		t.Fatalf("expected single reconnect ticket waiter, got %d", tickets.Load())
	}
	_ = mgr.Disconnect(context.Background())
}
