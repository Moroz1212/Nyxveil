package engine_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyxveil/client-windows/internal/engine"
	"github.com/nyxveil/client-windows/internal/netcfg"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
	"github.com/nyxveil/nvp/core/tunnel"
)

func TestTwoSimultaneousConnectRejectsSecond(t *testing.T) {
	raw, keys, dpriv := catalogBundle(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	mgr := engine.NewManager(engine.Options{
		SessionOpener: func(ctx context.Context, _ model.Catalog, _ engine.ConnectRequest) (*session.Session, transport.Conn, model.NodeRegistryEntry, error) {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, nil, model.NodeRegistryEntry{}, ctx.Err()
			}
			return nil, nil, model.NodeRegistryEntry{}, context.Canceled
		},
	})
	var firstErr, secondErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		firstErr = mgr.Connect(context.Background(), connectReq(raw, keys, dpriv))
	}()
	<-entered
	go func() {
		defer wg.Done()
		secondErr = mgr.Connect(context.Background(), connectReq(raw, keys, dpriv))
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if !errors.Is(secondErr, engine.ErrConnectInProgress) {
		t.Fatalf("second=%v want ErrConnectInProgress", secondErr)
	}
	_ = firstErr
}

func TestDisconnectSupersedesInFlightConnectCommit(t *testing.T) {
	raw, keys, dpriv := catalogBundle(t)
	var commits atomic.Int32
	enteredTUN := make(chan struct{})
	mgr := engine.NewManager(engine.Options{
		TUN: &blockingTUN{entered: enteredTUN},
		TypeConfigFn: func(ctx context.Context) (*netcfg.Message, error) {
			return &netcfg.Message{
				VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1280,
				Gateway: "10.66.0.1", DNSServers: []string{"10.66.0.1"},
			}, nil
		},
		SessionOpener: func(ctx context.Context, _ model.Catalog, _ engine.ConnectRequest) (*session.Session, transport.Conn, model.NodeRegistryEntry, error) {
			return session.New(session.DefaultConfig(true)), nopConn{}, model.NodeRegistryEntry{NodeID: "a"}, nil
		},
	})
	done := make(chan error, 1)
	go func() {
		err := mgr.Connect(context.Background(), connectReq(raw, keys, dpriv))
		if err == nil {
			commits.Add(1)
		}
		done <- err
	}()
	<-enteredTUN
	_ = mgr.Disconnect(context.Background())
	err := <-done
	if err == nil {
		t.Fatal("expected stale/cancel error, got nil Connected")
	}
	if commits.Load() != 0 {
		t.Fatal("stale Connect committed Connected")
	}
	st, _ := mgr.State.Get()
	if st.String() == "Connected" {
		t.Fatal("state Connected after superseded Connect")
	}
}

func TestConnectAfterCancelAllowsNewConnect(t *testing.T) {
	raw, keys, dpriv := catalogBundle(t)
	block := make(chan struct{})
	mgr := engine.NewManager(engine.Options{
		SessionOpener: func(ctx context.Context, _ model.Catalog, _ engine.ConnectRequest) (*session.Session, transport.Conn, model.NodeRegistryEntry, error) {
			select {
			case <-block:
			case <-ctx.Done():
				return nil, nil, model.NodeRegistryEntry{}, ctx.Err()
			}
			return nil, nil, model.NodeRegistryEntry{}, context.Canceled
		},
	})
	go func() { _ = mgr.Connect(context.Background(), connectReq(raw, keys, dpriv)) }()
	time.Sleep(30 * time.Millisecond)
	_ = mgr.Disconnect(context.Background())
	time.Sleep(30 * time.Millisecond)

	mgr2Done := make(chan error, 1)
	mgrB := engine.NewManager(engine.Options{
		TUN: &instantTUN{},
		TypeConfigFn: func(ctx context.Context) (*netcfg.Message, error) {
			return &netcfg.Message{
				VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1280,
				Gateway: "10.66.0.1", DNSServers: []string{"10.66.0.1"},
			}, nil
		},
		SessionOpener: func(ctx context.Context, _ model.Catalog, _ engine.ConnectRequest) (*session.Session, transport.Conn, model.NodeRegistryEntry, error) {
			return session.New(session.DefaultConfig(true)), nopConn{}, model.NodeRegistryEntry{NodeID: "b"}, nil
		},
	})
	go func() { mgr2Done <- mgrB.Connect(context.Background(), connectReq(raw, keys, dpriv)) }()
	select {
	case err := <-mgr2Done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Connect B blocked")
	}
	_ = mgrB.Disconnect(context.Background())
	close(block)
}

func TestReconnectDoesNotOverlapManualConnect(t *testing.T) {
	raw, keys, dpriv := catalogBundle(t)
	entered := make(chan struct{}, 1)
	mgr := engine.NewManager(engine.Options{
		SessionOpener: func(ctx context.Context, _ model.Catalog, _ engine.ConnectRequest) (*session.Session, transport.Conn, model.NodeRegistryEntry, error) {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return nil, nil, model.NodeRegistryEntry{}, ctx.Err()
		},
	})
	go func() { _ = mgr.Connect(context.Background(), connectReq(raw, keys, dpriv)) }()
	<-entered
	err := mgr.Connect(context.Background(), connectReq(raw, keys, dpriv))
	if !errors.Is(err, engine.ErrConnectInProgress) {
		t.Fatalf("got %v", err)
	}
	_ = mgr.Disconnect(context.Background())
}

// Ensure tunnel.Device compile reference for race package.
var _ tunnel.Device = (*nopDevice)(nil)
