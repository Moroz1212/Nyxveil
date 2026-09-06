//go:build windows

package engine_test

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/nyxveil/client-windows/internal/engine"
	"github.com/nyxveil/client-windows/internal/recoverylog"
)

func TestRollbackPreservesJournalOnUndoFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "route-journal.json")
	j := recoverylog.Journal{
		Phase: "bypass",
		Applied: []recoverylog.Mutation{{
			ID: "bypass-x", Kind: "bypass_route",
			DestPrefix: "198.51.100.1/32", NextHop: "", // undoMutation must fail (missing next hop)
		}},
	}
	if err := recoverylog.Write(path, j); err != nil {
		t.Fatal(err)
	}
	ra := engine.NewWindowsApplier()
	ra.JournalPath = path
	err := ra.RecoverOnStartup()
	if err == nil {
		t.Fatal("expected restore failure for mutation missing next hop")
	}
	got, err := recoverylog.Read(path)
	if err != nil {
		t.Fatalf("journal must survive failed restore: %v", err)
	}
	if len(got.Applied) == 0 {
		t.Fatal("expected unreverted mutations retained")
	}
	if got.Phase != "restore_failed" {
		t.Fatalf("phase=%s want restore_failed", got.Phase)
	}
}

func TestSuccessfulRestoreClearsJournal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "route-journal.json")
	// Empty applied → rollback succeeds and clears.
	if err := recoverylog.Write(path, recoverylog.Journal{Phase: "capture", Applied: nil}); err != nil {
		t.Fatal(err)
	}
	ra := engine.NewWindowsApplier()
	ra.JournalPath = path
	if err := ra.RecoverOnStartup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("journal should be cleared after clean restore, err=%v", err)
	}
}

func TestPendingPersistFailureAbortsBypass(t *testing.T) {
	w := engine.NewWindowsApplier()
	// Path with invalid volume causes persist failure before OS mutation.
	w.JournalPath = `\\?\INVALID\nyxveil-journal-test\route-journal.json`
	plan := engine.NewPlan()
	plan.CapturedDefault.Present = true
	plan.CapturedDefault.NextHop = netip.MustParseAddr("192.0.2.1")
	plan.SetBypassHosts([]engine.HostRoute{{
		Destination: netip.MustParsePrefix("198.51.100.1/32"),
	}})
	if err := w.ApplyBypass(plan); err == nil {
		t.Fatal("expected persist failure before OS bypass mutation")
	}
}
