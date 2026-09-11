package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/controlplane"
	"github.com/nyxveil/server/internal/version"
)

func writeTxn(t *testing.T, dir, id string, body map[string]any) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLegacy119MarkerDeleted_CorrelatedCommittedReportsUpdatedHealthy(t *testing.T) {
	n, capture := newLifecycleNode(t)
	n.cpOK.Store(true)
	n.draining.Store(true)
	dir := n.updateTransactionDir()
	cmd := "ac14e15b-6cc9-4d45-9ff7-6a05c992af5d"
	writeTxn(t, dir, "1789143920896154056-64966", map[string]any{
		"id":                   "1789143920896154056-64966",
		"legacy_parent":        true,
		"phase":                "committed",
		"command_id":           cmd,
		"previous_version":     "1.1.9",
		"target_version":       version.ServerVersion,
		"process_cli_at_start": "1.1.9",
		"created_at":           time.Now().UTC(),
		"pre_baseline": map[string]any{
			"node_id":         "n-test",
			"draining":        true,
			"lifecycle_known": true,
		},
	})
	// Historical unrelated journals without correlation must be ignored.
	writeTxn(t, dir, "old-stale-1", map[string]any{
		"id":             "old-stale-1",
		"phase":          "committed",
		"target_version": version.ServerVersion,
		"created_at":     time.Now().UTC().Add(-24 * time.Hour),
	})
	writeTxn(t, dir, "old-stale-2", map[string]any{
		"id":             "old-stale-2",
		"phase":          "committed",
		"target_version": "1.1.12",
		"created_at":     time.Now().UTC().Add(-48 * time.Hour),
	})
	// Marker intentionally missing (legacy 1.1.9 deleted it).
	if _, ok := n.readUpdateMarker(); ok {
		t.Fatal("marker must be absent for this scenario")
	}

	n.completePendingUpdate(context.Background())
	codes := capture.codes()
	if len(codes) != 1 || codes[0] != "updated_healthy" {
		t.Fatalf("expected recovered updated_healthy, got %v", codes)
	}
	if _, err := os.Stat(filepath.Join(dir, "1789143920896154056-64966.json")); !os.IsNotExist(err) {
		t.Fatalf("correlated journal must be consumed after successful report: %v", err)
	}
	if !n.draining.Load() {
		t.Fatal("updater must not undrain")
	}

	// Restart / replay must not invent another result.
	n.completePendingUpdate(context.Background())
	if len(capture.codes()) != 1 {
		t.Fatalf("duplicate report after consume: %v", capture.codes())
	}
}

func TestCorrelatedUpdate_CPOutageThenRetry(t *testing.T) {
	n, _ := newLifecycleNode(t)
	n.cpOK.Store(true)
	var fail atomic.Bool
	fail.Store(true)
	var mu sync.Mutex
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/result") && r.Method == http.MethodPost {
			mu.Lock()
			posts++
			mu.Unlock()
			if fail.Load() {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	cp, err := controlplane.NewClient(srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	cp.NodeID = "n-test"
	cp.PrivateKey = n.cp.PrivateKey
	n.cp = cp

	cmd := "cmd-retry-correlated"
	writeTxn(t, n.updateTransactionDir(), "tx-retry", map[string]any{
		"id":               "tx-retry",
		"phase":            "committed",
		"command_id":       cmd,
		"previous_version": "1.1.9",
		"target_version":   version.ServerVersion,
		"created_at":       time.Now().UTC(),
		"pre_baseline":     map[string]any{"node_id": "n-test"},
	})

	n.completePendingUpdate(context.Background())
	pending := n.commandStore.pendingResults()
	if len(pending) != 1 || pending[0].CommandID != cmd {
		t.Fatalf("pending must survive CP outage: %+v", pending)
	}
	raw, err := os.ReadFile(filepath.Join(n.updateTransactionDir(), "tx-retry.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"result_queued_at"`) {
		t.Fatalf("journal must record result_queued_at: %s", raw)
	}

	fail.Store(false)
	n.flushPendingCommandResults(context.Background())
	n.completePendingUpdate(context.Background())
	if len(n.commandStore.pendingResults()) != 0 {
		t.Fatal("pending must clear after successful retry")
	}
	if _, err := os.Stat(filepath.Join(n.updateTransactionDir(), "tx-retry.json")); !os.IsNotExist(err) {
		t.Fatal("journal must be consumed after confirmed report")
	}
	mu.Lock()
	defer mu.Unlock()
	if posts < 2 {
		t.Fatalf("expected retry posts, got %d", posts)
	}
}

func TestJournalWithoutCommandID_FailClosed(t *testing.T) {
	n, capture := newLifecycleNode(t)
	n.cpOK.Store(true)
	writeTxn(t, n.updateTransactionDir(), "no-cmd", map[string]any{
		"id":             "no-cmd",
		"phase":          "committed",
		"target_version": version.ServerVersion,
		"created_at":     time.Now().UTC(),
	})
	n.completePendingUpdate(context.Background())
	if len(capture.codes()) != 0 {
		t.Fatalf("must not guess without command_id: %v", capture.codes())
	}
}

func TestTargetVersionMismatch_FailClosed(t *testing.T) {
	n, capture := newLifecycleNode(t)
	n.cpOK.Store(true)
	writeTxn(t, n.updateTransactionDir(), "mismatch", map[string]any{
		"id":               "mismatch",
		"phase":            "committed",
		"command_id":       "cmd-mm",
		"previous_version": "1.1.9",
		"target_version":   "9.9.9",
		"created_at":       time.Now().UTC(),
		"pre_baseline":     map[string]any{"node_id": "n-test"},
	})
	n.completePendingUpdate(context.Background())
	if len(capture.codes()) != 0 {
		t.Fatalf("must not report when installed != target: %v", capture.codes())
	}
}

func TestNodeIdentityMismatch_FailClosed(t *testing.T) {
	n, capture := newLifecycleNode(t)
	n.cpOK.Store(true)
	writeTxn(t, n.updateTransactionDir(), "node-mm", map[string]any{
		"id":               "node-mm",
		"phase":            "committed",
		"command_id":       "cmd-node",
		"previous_version": "1.1.9",
		"target_version":   version.ServerVersion,
		"created_at":       time.Now().UTC(),
		"pre_baseline":     map[string]any{"node_id": "other-node"},
	})
	n.completePendingUpdate(context.Background())
	if len(capture.codes()) != 0 {
		t.Fatalf("must not report node mismatch: %v", capture.codes())
	}
}

func TestAmbiguousCorrelatedJournals_FailClosed(t *testing.T) {
	n, capture := newLifecycleNode(t)
	n.cpOK.Store(true)
	dir := n.updateTransactionDir()
	cmd := "cmd-ambig"
	writeTxn(t, dir, "a", map[string]any{
		"id": "a", "phase": "committed", "command_id": cmd,
		"target_version": version.ServerVersion, "created_at": time.Now().UTC(),
		"pre_baseline": map[string]any{"node_id": "n-test"},
	})
	writeTxn(t, dir, "b", map[string]any{
		"id": "b", "phase": "rollback_failed", "command_id": cmd,
		"target_version": version.ServerVersion, "created_at": time.Now().UTC(),
		"pre_baseline": map[string]any{"node_id": "n-test"},
	})
	n.completePendingUpdate(context.Background())
	if len(capture.codes()) != 0 {
		t.Fatalf("ambiguous journals must fail closed: %v", capture.codes())
	}
}

func TestIntactMarkerPathStillWorks(t *testing.T) {
	n, capture := newLifecycleNode(t)
	n.cpOK.Store(true)
	started := time.Now().UTC().Add(-time.Second)
	if err := n.writeUpdateMarker(updateMarker{
		CommandID:       "cmd-marker",
		PreviousVersion: "1.1.9",
		TargetVersion:   version.ServerVersion,
		Phase:           updatePhaseRestarting,
		StartedAt:       started.Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	writeTxn(t, n.updateTransactionDir(), "tx-marker", map[string]any{
		"id":               "tx-marker",
		"phase":            "committed",
		"command_id":       "cmd-marker",
		"previous_version": "1.1.9",
		"target_version":   version.ServerVersion,
		"created_at":       time.Now().UTC(),
	})
	n.completePendingUpdate(context.Background())
	codes := capture.codes()
	if len(codes) != 1 || codes[0] != "updated_healthy" {
		t.Fatalf("marker path broken: %v", codes)
	}
}

func TestCorrelatedRollbackHealthy(t *testing.T) {
	n, capture := newLifecycleNode(t)
	n.cpOK.Store(true)
	prev := version.ServerVersion
	writeTxn(t, n.updateTransactionDir(), "tx-rb", map[string]any{
		"id":               "tx-rb",
		"phase":            "rolling_back",
		"terminal_outcome": "rolled_back_healthy",
		"command_id":       "cmd-rb",
		"previous_version": prev,
		"target_version":   "9.9.9",
		"created_at":       time.Now().UTC(),
		"pre_baseline":     map[string]any{"node_id": "n-test"},
	})
	n.completePendingUpdate(context.Background())
	codes := capture.codes()
	if len(codes) != 1 || codes[0] != "rolled_back_healthy" {
		t.Fatalf("expected rolled_back_healthy, got %v", codes)
	}
	if capture.successes()[0] {
		t.Fatal("rollback must not be success")
	}
}

func TestCorrelatedRollbackFailedNotSuccess(t *testing.T) {
	n, capture := newLifecycleNode(t)
	n.cpOK.Store(true)
	writeTxn(t, n.updateTransactionDir(), "tx-rbf", map[string]any{
		"id":               "tx-rbf",
		"phase":            "rollback_failed",
		"command_id":       "cmd-rbf",
		"previous_version": "1.1.9",
		"target_version":   version.ServerVersion,
		"failure_reason":   "tls enforce failed",
		"created_at":       time.Now().UTC(),
		"pre_baseline":     map[string]any{"node_id": "n-test"},
	})
	n.completePendingUpdate(context.Background())
	codes := capture.codes()
	if len(codes) != 1 || codes[0] != "rollback_failed" {
		t.Fatalf("expected rollback_failed, got %v", codes)
	}
	if capture.successes()[0] {
		t.Fatal("rollback_failed must not be success")
	}
}
