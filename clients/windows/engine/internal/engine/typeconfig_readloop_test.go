package engine

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyxveil/client-windows/internal/netcfg"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
)

// stickyReadConn stays blocked after ctx cancel until SetReadDeadline(past).
type stickyReadConn struct {
	profile transport.Profile
	active  atomic.Int32
	max     atomic.Int32
	mu      atomic.Value // time.Time deadline
}

func newStickyConn(p transport.Profile) *stickyReadConn {
	c := &stickyReadConn{profile: p}
	c.mu.Store(time.Time{})
	return c
}

func (c *stickyReadConn) Read(ctx context.Context) ([]byte, error) {
	n := c.active.Add(1)
	for {
		if cur := c.max.Load(); n > cur {
			c.max.CompareAndSwap(cur, n)
		}
		dl := c.mu.Load().(time.Time)
		if !dl.IsZero() && !time.Now().Before(dl) {
			c.active.Add(-1)
			return nil, context.DeadlineExceeded
		}
		select {
		case <-ctx.Done():
			// Intentional: do NOT return on cancel (simulates TLS Read ignoring ctx).
			time.Sleep(5 * time.Millisecond)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (c *stickyReadConn) Write(context.Context, []byte) error { return nil }
func (c *stickyReadConn) Close() error                         { return nil }
func (c *stickyReadConn) LocalAddr() net.Addr                  { return &net.TCPAddr{} }
func (c *stickyReadConn) RemoteAddr() net.Addr                 { return &net.TCPAddr{} }
func (c *stickyReadConn) Profile() transport.Profile           { return c.profile }
func (c *stickyReadConn) SetReadDeadline(t time.Time) error {
	c.mu.Store(t)
	return nil
}
func (c *stickyReadConn) SetWriteDeadline(time.Time) error { return nil }

func validTypeConfig() *netcfg.Message {
	return &netcfg.Message{
		VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1280,
		Gateway: "10.66.0.1", DNSServers: []string{"10.66.0.1"},
	}
}

func runTypeConfigOwnership(t *testing.T, profile transport.Profile) {
	t.Helper()
	sticky := newStickyConn(profile)
	sig := make(chan *netcfg.Message, 1)
	typeConfigTestSignal = sig
	typeConfigReadLoopForTest = func(ctx context.Context, sess *session.Session) error {
		_, err := sticky.Read(ctx)
		return err
	}
	t.Cleanup(func() {
		typeConfigTestSignal = nil
		typeConfigReadLoopForTest = nil
	})

	sess := session.New(session.DefaultConfig(true))
	if err := sess.Connect(context.Background(), sticky); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(Options{})
	mgr.ConfigWait = 5 * time.Second

	done := make(chan struct{})
	var cfg *netcfg.Message
	var waitErr error
	go func() {
		cfg, waitErr = mgr.waitTypeConfig(context.Background(), sess, sticky)
		close(done)
	}()
	time.Sleep(40 * time.Millisecond) // temp reader inside sticky.Read
	if sticky.active.Load() != 1 {
		t.Fatalf("expected temp reader active, got %d", sticky.active.Load())
	}
	sig <- validTypeConfig()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("waitTypeConfig hung")
	}
	if waitErr != nil || cfg == nil {
		t.Fatalf("wait: %v cfg=%v", waitErr, cfg)
	}
	if sticky.active.Load() != 0 {
		t.Fatalf("temp reader still active after waitTypeConfig: %d", sticky.active.Load())
	}

	// Permanent reader must not overlap.
	permCtx, permCancel := context.WithCancel(context.Background())
	permDone := make(chan struct{})
	go func() {
		defer close(permDone)
		_, _ = sticky.Read(permCtx)
	}()
	time.Sleep(30 * time.Millisecond)
	if sticky.max.Load() > 1 {
		t.Fatalf("concurrent readers observed max=%d", sticky.max.Load())
	}
	permCancel()
	_ = sticky.SetReadDeadline(time.Now())
	<-permDone
	_ = sticky.SetReadDeadline(time.Time{})
}

func TestTypeConfigTempReaderExitsBeforePermanentTLS(t *testing.T) {
	runTypeConfigOwnership(t, transport.ProfileTLSTCP)
}

func TestTypeConfigTempReaderExitsBeforePermanentQUIC(t *testing.T) {
	runTypeConfigOwnership(t, transport.ProfileQUICUDP)
}

func TestTypeConfigDisconnectDuringWait(t *testing.T) {
	sticky := newStickyConn(transport.ProfileTLSTCP)
	typeConfigReadLoopForTest = func(ctx context.Context, sess *session.Session) error {
		_, err := sticky.Read(ctx)
		return err
	}
	t.Cleanup(func() { typeConfigReadLoopForTest = nil })

	sess := session.New(session.DefaultConfig(true))
	_ = sess.Connect(context.Background(), sticky)
	mgr := NewManager(Options{})
	mgr.ConfigWait = time.Minute

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := mgr.waitTypeConfig(ctx, sess, sticky)
		done <- err
	}()
	time.Sleep(40 * time.Millisecond)
	cancel() // Disconnect cancels connect ctx
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error on cancel")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout cleanup hung")
	}
	if sticky.active.Load() != 0 {
		t.Fatalf("reader leaked after cancel: %d", sticky.active.Load())
	}
}

func TestTypeConfigTimeoutCleanup(t *testing.T) {
	sticky := newStickyConn(transport.ProfileTLSTCP)
	typeConfigReadLoopForTest = func(ctx context.Context, sess *session.Session) error {
		_, err := sticky.Read(ctx)
		return err
	}
	t.Cleanup(func() { typeConfigReadLoopForTest = nil })

	sess := session.New(session.DefaultConfig(true))
	_ = sess.Connect(context.Background(), sticky)
	mgr := NewManager(Options{})
	mgr.ConfigWait = 80 * time.Millisecond

	_, err := mgr.waitTypeConfig(context.Background(), sess, sticky)
	if err == nil {
		t.Fatal("expected timeout")
	}
	if sticky.active.Load() != 0 {
		t.Fatalf("reader leaked after timeout: %d", sticky.active.Load())
	}
}

func TestStopTempTransportReaderUnblocksStickyRead(t *testing.T) {
	sticky := newStickyConn(transport.ProfileTLSTCP)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = sticky.Read(ctx)
	}()
	time.Sleep(30 * time.Millisecond)
	stopTempTransportReader(cancel, done, sticky)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stopTempTransportReader did not unblock")
	}
}
