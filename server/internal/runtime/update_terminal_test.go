package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/version"
)

func TestDrainedUpdateTerminalJournalReported(t *testing.T) {
	n, capture := newLifecycleNode(t)
	n.cpOK.Store(true)
	n.draining.Store(true)
	started := time.Now().UTC().Add(-time.Second)
	m := updateMarker{CommandID: "drained-upgrade", PreviousVersion: "1.1.9", TargetVersion: version.ServerVersion, Phase: updatePhaseRestarting, StartedAt: started.Format(time.RFC3339)}
	if err := n.writeUpdateMarker(m); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(filepath.Dir(n.opts.KeyPath), "update-transactions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(created time.Time) {
		raw, _ := json.Marshal(map[string]any{"phase": "committed", "target_version": version.ServerVersion, "created_at": created})
		if err := os.WriteFile(filepath.Join(dir, "tx.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(started.Add(-time.Hour))
	if _, changed := n.bridgeUpdatePhaseFromCtl(m); changed {
		t.Fatal("stale transaction accepted")
	}
	write(time.Now().UTC())
	n.completePendingUpdate(context.Background())
	codes := capture.codes()
	if len(codes) != 1 || codes[0] != "updated_healthy" {
		t.Fatalf("terminal success missing: %v", codes)
	}
	if !n.draining.Load() {
		t.Fatal("updater must not undrain itself")
	}
}
