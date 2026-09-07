//go:build windows

package engine_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nyxveil/client-windows/internal/engine"
	"github.com/nyxveil/client-windows/internal/recoverylog"
)

// TestRecoverOnStartupStaleTunDNSWithoutAdapter reproduces the 1.0.6 installer
// failure: dirty journal with tun_dns for already-removed Wintun adapter must
// clear idempotently so SCM start can succeed.
func TestRecoverOnStartupStaleTunDNSWithoutAdapter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "route-journal.json")
	j := recoverylog.Journal{
		Phase: "restore_failed",
		Applied: []recoverylog.Mutation{
			{ID: "tun-dns", Kind: "tun_dns", TunName: "Nyxveil-Missing-Adapter-XYZ", DNS: []string{"10.66.0.1"}},
		},
	}
	raw, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	applier := &engine.WindowsApplier{JournalPath: path}
	if err := applier.RecoverOnStartup(); err != nil {
		t.Fatalf("RecoverOnStartup must succeed for missing adapter DNS: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("journal should be cleared, err=%v", err)
	}
}
