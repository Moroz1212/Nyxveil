package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/controlplane"
	"github.com/nyxveil/server/internal/filemeta"
)

func TestRolledBackHealthyIsNotSuccess(t *testing.T) {
	dir := t.TempDir()
	n := &Node{}
	n.opts.KeyPath = filepath.Join(dir, "node.key")
	m := updateMarker{
		CommandID:       "cmd-rb",
		PreviousVersion: "1.1.10",
		TargetVersion:   "1.1.11",
		Phase:           "ResultPending",
		ResultPending:   true,
		ResultSuccess:   false,
		ResultCode:      "rolled_back_healthy",
		ResultMessage:   "restored",
	}
	if err := n.writeUpdateMarker(m); err != nil {
		t.Fatal(err)
	}
	got, ok := n.readUpdateMarker()
	if !ok {
		t.Fatal("marker missing")
	}
	if got.ResultSuccess {
		t.Fatal("rolled_back_healthy must persist success=false")
	}
	if got.ResultCode != "rolled_back_healthy" {
		t.Fatalf("code=%q", got.ResultCode)
	}
}

func TestUpdateMarkerUsesDurableWriteContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(findRuntimeDir(t), "update_command.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "filemeta.DurableWrite") {
		t.Fatal("writeUpdateMarker must use DurableWrite")
	}
	if strings.Contains(text, "filemeta.AtomicWrite") {
		t.Fatal("writeUpdateMarker must not use AtomicWrite")
	}
	// Prove DurableWrite API is crash-safe (fsync).
	dir := t.TempDir()
	p := filepath.Join(dir, "marker.json")
	if err := filemeta.DurableWrite(p, []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPendingResultSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "commands-state.json")
	store := newCommandDedupeStore(path)
	store.addPendingResult(pendingResultRecord{
		CommandID:     "cmd-1",
		Success:       false,
		ResultCode:    "rolled_back_healthy",
		ResultMessage: "restored previous",
		BootID:        "boot-a",
	})
	reloaded := newCommandDedupeStore(path)
	pending := reloaded.pendingResults()
	if len(pending) != 1 || pending[0].Success || pending[0].ResultCode != "rolled_back_healthy" {
		t.Fatalf("%+v", pending)
	}
}

func TestRestartPendingSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "commands-state.json")
	store := newCommandDedupeStore(path)
	store.addRestartPending("cmd-rst", time.Now().UTC().Format(time.RFC3339))
	reloaded := newCommandDedupeStore(path)
	pending := reloaded.restartPending()
	if len(pending) != 1 || pending[0].CommandID != "cmd-rst" {
		t.Fatalf("%+v", pending)
	}
}

func TestExplicitRenewRateLimitWindow(t *testing.T) {
	if explicitRenewRateLimitWindow != time.Hour {
		t.Fatalf("rate limit window=%v want 1h", explicitRenewRateLimitWindow)
	}
	raw, err := os.ReadFile(filepath.Join(findRuntimeDir(t), "commands.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "issueACMEForced") {
		t.Fatal("explicit RenewCertificate must call issueACMEForced")
	}
	if strings.Contains(text, `"not_due"`) {
		t.Fatal("explicit renew must not report not_due")
	}
	if !strings.Contains(text, `"rate_limited"`) {
		t.Fatal("explicit renew must support rate_limited")
	}
}

func TestHeartbeatRenewalJSONMatchesCP132(t *testing.T) {
	raw, err := json.Marshal(controlplane.HeartbeatRequest{
		NodeID:                "n1",
		LastSuccessfulRenewal: "2026-01-01T00:00:00Z",
		NextPlannedRenewal:    "2026-02-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["last_renewal_success"]; !ok {
		t.Fatalf("missing last_renewal_success in %s", raw)
	}
	if _, ok := got["last_renewal_next"]; !ok {
		t.Fatalf("missing last_renewal_next in %s", raw)
	}
	if _, ok := got["last_successful_renewal"]; ok {
		t.Fatal("legacy last_successful_renewal must not be emitted")
	}
	if _, ok := got["next_planned_renewal"]; ok {
		t.Fatal("legacy next_planned_renewal must not be emitted")
	}
}

func findRuntimeDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}
