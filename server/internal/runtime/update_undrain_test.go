package runtime_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/controlplane"
	rt "github.com/nyxveil/server/internal/runtime"
)

// Real TCP/QUIC listeners, with a test TUN exemption; this is not a Linux E2E.
func TestUpdateRestartDrainedThenCPUndrainStartsListeners(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "server.json")
	writeMinimalCfg(t, cfgPath, "http://127.0.0.1:1")
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	n, err := rt.New(rt.Options{ConfigPath: cfgPath, KeyPath: filepath.Join(dir, "node.key"), AppliedPath: filepath.Join(dir, "applied-config.json"), SkipTUN: true, TestMode: true, ControlHTTP: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := controlplane.NodeConfig{NodeID: "nv-test", LocationID: "fi-helsinki", Enabled: true, Draining: true, ConfigVersion: 7}
	if err := n.ApplyConfig(cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := n.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = n.Shutdown(c)
	}()
	st := n.Status()
	if st.Accepting || st.TLSOK || st.QUICOK {
		t.Fatalf("drained restart unexpectedly listening: %+v", st)
	}
	keyBefore, err := os.ReadFile(filepath.Join(dir, "node.key"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Draining = false
	cfg.ConfigVersion++
	if err := n.ApplyConfig(cfg); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st = n.Status()
		if st.Accepting && st.TLSOK && st.QUICOK {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !st.Accepting || !st.TLSOK || !st.QUICOK {
		t.Fatalf("undrain did not restore listeners: %+v", st)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	keyAfter, err := os.ReadFile(filepath.Join(dir, "node.key"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || string(keyBefore) != string(keyAfter) {
		t.Fatal("undrain changed config/identity")
	}
}
