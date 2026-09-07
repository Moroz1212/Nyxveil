package productiongate_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	rt "github.com/nyxveil/server/internal/runtime"
)

func startRuntimeNode(t *testing.T, cfgPath, state, cpURL, caFile string) (controlHTTP string, stop func()) {
	t.Helper()
	_ = cpURL
	_ = caFile
	keyPath := filepath.Join(state, "node.key")
	applied := filepath.Join(state, "applied-config.json")
	node, err := rt.New(rt.Options{
		ConfigPath:  cfgPath,
		KeyPath:     keyPath,
		AppliedPath: applied,
		ControlHTTP: "127.0.0.1:0",
		SkipTUN:     true,
		TestMode:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := node.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	// Discover bound control HTTP from status poller — Node stores listener addr internally.
	// ControlHTTP "127.0.0.1:0" is expanded on start; read via env file written by test helper.
	addr := node.ControlHTTPAddr()
	if addr == "" {
		cancel()
		_ = node.Shutdown(context.Background())
		t.Fatal("control HTTP addr empty")
	}
	controlHTTP = "http://" + addr
	var stopped bool
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		shCtx, shCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shCancel()
		_ = node.Shutdown(shCtx)
	}
	// Ensure identity file exists for gate identity check.
	if st, err := os.Stat(keyPath); err != nil || st.Size() == 0 {
		stop()
		t.Fatal("node.key missing after start")
	}
	return controlHTTP, stop
}
