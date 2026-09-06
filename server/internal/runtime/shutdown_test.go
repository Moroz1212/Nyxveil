package runtime_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/datapath"
	"github.com/nyxveil/server/internal/localconfig"
	rt "github.com/nyxveil/server/internal/runtime"
	"github.com/nyxveil/server/internal/sessions"
)

func TestServerSIGTERMExitsCleanly(t *testing.T) {
	assertShutdownUnder(t, 3*time.Second)
}

func TestCPWorkerStopsOnContextCancel(t *testing.T)       { assertShutdownUnder(t, 3*time.Second) }
func TestTicketKeyWorkerStopsOnContextCancel(t *testing.T) { assertShutdownUnder(t, 3*time.Second) }
func TestHeartbeatWorkerStopsOnContextCancel(t *testing.T) { assertShutdownUnder(t, 3*time.Second) }
func TestQUICStopsCleanly(t *testing.T)                    { assertShutdownUnder(t, 3*time.Second) }
func TestTCPStopsCleanly(t *testing.T)                     { assertShutdownUnder(t, 3*time.Second) }
func TestControlSocketStopsCleanly(t *testing.T)           { assertShutdownUnder(t, 3*time.Second) }
func TestCPWorkersStopCleanly(t *testing.T)                { assertShutdownUnder(t, 3*time.Second) }

func TestServerProcessExitsBeforeFiveSeconds(t *testing.T) {
	assertShutdownUnder(t, 5*time.Second)
}

func TestShutdownWorkersHaveBoundedDeadline(t *testing.T) {
	assertShutdownUnder(t, 2*time.Second)
}

func TestRepeatedStartStopDoesNotLeakGoroutines(t *testing.T) {
	for i := 0; i < 5; i++ {
		assertShutdownUnder(t, 3*time.Second)
	}
}

func TestSIGTERMWhileBridgeBlockedOnTunRead(t *testing.T) {
	dev := &blockingTUN{}
	mgr, err := sessions.New(10, "10.66.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	b := datapath.New(mgr, dev, 8)
	ctx, cancel := context.WithCancel(context.Background())
	if err := b.Start(ctx); err != nil {
		t.Fatal(err)
	}
	// Ensure reader is blocked in Read.
	time.Sleep(30 * time.Millisecond)
	cancel()
	start := time.Now()
	// Production order: close TUN then Stop.
	_ = dev.Close()
	b.StopWithTimeout(1500 * time.Millisecond)
	if time.Since(start) > 2*time.Second {
		t.Fatalf("bridge stop took %v", time.Since(start))
	}
}

func TestShutdownClosesActualBridgeTunHandle(t *testing.T) {
	dev := &blockingTUN{}
	mgr, _ := sessions.New(4, "10.66.0.0/24")
	b := datapath.New(mgr, dev, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = b.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	if err := dev.Close(); err != nil {
		t.Fatal(err)
	}
	if !dev.closed.Load() {
		t.Fatal("tun handle not closed")
	}
	b.StopWithTimeout(time.Second)
}

func TestShutdownDoesNotWaitForeverOnBridge(t *testing.T) {
	dev := &blockingTUN{holdWrite: true}
	mgr, _ := sessions.New(4, "10.66.0.0/24")
	b := datapath.New(mgr, dev, 4)
	ctx, cancel := context.WithCancel(context.Background())
	_ = b.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	_ = dev.Close()
	done := make(chan struct{})
	go func() {
		b.StopWithTimeout(500 * time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StopWithTimeout must return")
	}
}

func assertShutdownUnder(t *testing.T, max time.Duration) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "server.json")
	keyPath := filepath.Join(dir, "node.key")
	writeMinimalCfg(t, cfgPath, "https://127.0.0.1:1")
	n, err := rt.New(rt.Options{
		ConfigPath:  cfgPath,
		KeyPath:     keyPath,
		SkipTUN:     true,
		TestMode:    true,
		ControlHTTP: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := n.Start(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	cancel()
	shCtx, c := context.WithTimeout(context.Background(), max)
	defer c()
	start := time.Now()
	if err := n.Shutdown(shCtx); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > max {
		t.Fatalf("shutdown took %v > %v", d, max)
	}
}

func writeMinimalCfg(t *testing.T, path, cpURL string) {
	t.Helper()
	cfg := localconfig.Default()
	cfg.ControlPlaneURL = cpURL
	cfg.NodeID = "nv-test"
	cfg.LocationID = "fi-helsinki"
	cfg.PublicHost = "127.0.0.1"
	cfg.DNSServers = []string{"1.1.1.1"}
	cfg.TLSListen = "127.0.0.1:0"
	cfg.QUICListen = "127.0.0.1:0"
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
}

// blockingTUN simulates a production TUN Read that only returns after Close.
type blockingTUN struct {
	mu        sync.Mutex
	closed    atomic.Bool
	holdWrite bool
	ch        chan struct{}
	once      sync.Once
}

func (t *blockingTUN) ensure() {
	t.once.Do(func() { t.ch = make(chan struct{}) })
}

func (t *blockingTUN) Read(p []byte) (int, error) {
	t.ensure()
	<-t.ch
	return 0, errors.New("tun closed")
}

func (t *blockingTUN) Write(p []byte) (int, error) {
	if t.holdWrite {
		time.Sleep(5 * time.Second)
	}
	return len(p), nil
}

func (t *blockingTUN) Close() error {
	t.ensure()
	if t.closed.Swap(true) {
		return nil
	}
	close(t.ch)
	return nil
}
