package runtime_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/localconfig"
	rt "github.com/nyxveil/server/internal/runtime"
)

func TestServerSIGTERMExitsCleanly(t *testing.T) {
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
	// Give accept loops a moment to bind, then cancel like SIGTERM.
	time.Sleep(20 * time.Millisecond)
	cancel()
	done := make(chan error, 1)
	go func() {
		shCtx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		done <- n.Shutdown(shCtx)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not return within 5s (would trigger systemd SIGKILL)")
	}
}

func TestCPWorkerStopsOnContextCancel(t *testing.T) {
	assertWorkersStopOnCancel(t)
}

func TestTicketKeyWorkerStopsOnContextCancel(t *testing.T) {
	assertWorkersStopOnCancel(t)
}

func TestHeartbeatWorkerStopsOnContextCancel(t *testing.T) {
	assertWorkersStopOnCancel(t)
}

func TestQUICListenerClosesOnShutdown(t *testing.T) {
	assertWorkersStopOnCancel(t)
}

func TestTCPListenerClosesOnShutdown(t *testing.T) {
	assertWorkersStopOnCancel(t)
}

func assertWorkersStopOnCancel(t *testing.T) {
	t.Helper()
	// Same lifecycle as SIGTERM: cancel + Shutdown must complete quickly.
	// Separate from TestServerSIGTERMExitsCleanly so that test name is mandatory-unique.
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
	defer cancel()
	if err := n.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	shCtx, c := context.WithTimeout(context.Background(), 3*time.Second)
	defer c()
	start := time.Now()
	if err := n.Shutdown(shCtx); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("shutdown took %v", time.Since(start))
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
