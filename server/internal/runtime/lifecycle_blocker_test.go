package runtime

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/controlplane"
	"github.com/nyxveil/server/internal/identity"
	"github.com/nyxveil/server/internal/localconfig"
	"github.com/nyxveil/server/internal/metrics"
	"github.com/nyxveil/server/internal/revocation"
	"github.com/nyxveil/server/internal/sessions"
	"github.com/nyxveil/server/internal/version"
)

type resultCapture struct {
	mu      sync.Mutex
	results []controlplane.NodeCommandResultRequest
	ids     []string
}

func (c *resultCapture) record(id string, req controlplane.NodeCommandResultRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ids = append(c.ids, id)
	c.results = append(c.results, req)
}

func (c *resultCapture) codes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.results))
	for i, r := range c.results {
		out[i] = r.ResultCode
	}
	return out
}

func newLifecycleNode(t *testing.T) (*Node, *resultCapture) {
	t.Helper()
	dir := t.TempDir()
	mgr, err := sessions.New(4, "10.66.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cap := &resultCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/result") && r.Method == http.MethodPost {
			var req controlplane.NodeCommandResultRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			id := parts[len(parts)-2]
			cap.record(id, req)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))

	cp, err := controlplane.NewClient(srv.URL, nil)
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}
	cp.NodeID = "n-test"
	cp.PrivateKey = priv
	t.Cleanup(func() {
		if cp.HTTP != nil {
			cp.HTTP.CloseIdleConnections()
		}
		srv.Close()
	})

	n := &Node{
		opts: Options{
			KeyPath:  filepath.Join(dir, "node.key"),
			SkipTUN:  true,
			TestMode: true,
		},
		local: &localconfig.File{
			NodeID:     "n-test",
			LocationID: "loc-1",
		},
		key:      &identity.NodeKey{Private: priv, Public: pub},
		cp:       cp,
		sessions: mgr,
		rev:      revocation.New(),
		sampler:  metrics.NewSampler(),
	}
	n.rev.Apply(controlplane.RevocationSnapshot{UpdatedAt: time.Now().Unix()})
	n.running.Store(true)
	n.ticketKeysLoaded.Store(true)
	n.ensureCommandStore()
	return n, cap
}

func TestUpdatePreviousHealthyWhileDownloadingNotRollback(t *testing.T) {
	phases := []string{
		"Downloading",
		updatePhaseDownloading,
		updatePhaseVerifying,
		updatePhaseInstalling,
		updatePhaseRestarting,
		updatePhasePostCheck,
	}
	prev := version.ServerVersion
	for _, phase := range phases {
		t.Run(phase, func(t *testing.T) {
			n, cap := newLifecycleNode(t)
			n.cpOK.Store(true)
			if err := n.writeUpdateMarker(updateMarker{
				CommandID:       "cmd-upd-1",
				PreviousVersion: prev,
				TargetVersion:   "9.9.9",
				Phase:           phase,
				StartedAt:       time.Now().UTC().Add(-10 * time.Second).Format(time.RFC3339),
			}); err != nil {
				t.Fatal(err)
			}
			n.completePendingUpdate(context.Background())
			got, ok := n.readUpdateMarker()
			if !ok {
				t.Fatal("marker must remain while non-terminal")
			}
			if got.ResultPending || got.ResultCode == "rolled_back_healthy" {
				t.Fatalf("must not terminalize during %s: %+v", phase, got)
			}
			if codes := cap.codes(); len(codes) != 0 {
				t.Fatalf("unexpected CP results during %s: %v", phase, codes)
			}
			for _, c := range cap.codes() {
				if c == "rolled_back_healthy" {
					t.Fatal("rolled_back_healthy reported while downloading")
				}
			}
		})
	}
}

func TestUpdatePhaseTerminalOnly(t *testing.T) {
	if !updatePhaseIsNonTerminal(updatePhaseDownloading) {
		t.Fatal("downloading must be non-terminal")
	}
	if updatePhaseIsNonTerminal(updatePhaseUpdatedHealthy) {
		t.Fatal("updated_healthy must be terminal")
	}
	if updatePhaseIsNonTerminal(updatePhaseRolledBackHealthy) {
		t.Fatal("rolled_back_healthy must be terminal")
	}
	n, cap := newLifecycleNode(t)
	n.cpOK.Store(true)
	prev := version.ServerVersion
	// Executor claimed success but runtime still on previous → wait, no CP result yet.
	if err := n.writeUpdateMarker(updateMarker{
		CommandID:       "cmd-term",
		PreviousVersion: prev,
		TargetVersion:   "9.9.9",
		Phase:           updatePhaseUpdatedHealthy,
		StartedAt:       time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	n.completePendingUpdate(context.Background())
	if codes := cap.codes(); len(codes) != 0 {
		t.Fatalf("must wait for target version match: %v", codes)
	}
	got, ok := n.readUpdateMarker()
	if !ok || got.ResultPending {
		t.Fatalf("marker must remain pending confirmation: ok=%v %+v", ok, got)
	}

	// Real rollback only when executor marked rolled_back_healthy AND previous confirmed.
	if err := n.writeUpdateMarker(updateMarker{
		CommandID:       "cmd-rb",
		PreviousVersion: prev,
		TargetVersion:   "9.9.9",
		Phase:           updatePhaseRolledBackHealthy,
		StartedAt:       time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	n.completePendingUpdate(context.Background())
	codes := cap.codes()
	if len(codes) != 1 || codes[0] != "rolled_back_healthy" {
		t.Fatalf("want rolled_back_healthy, got %v", codes)
	}
	if cap.results[0].Success {
		t.Fatal("rolled_back_healthy must be success=false")
	}
}

func TestRebootCPNotReadyStaysPending(t *testing.T) {
	n, cap := newLifecycleNode(t)
	n.cpOK.Store(false)
	orig := bootIDReader
	bootIDReader = func() string { return "boot-after" }
	t.Cleanup(func() { bootIDReader = orig })

	if err := n.commandStore.addRebootPending("cmd-rb", "boot-before"); err != nil {
		t.Fatal(err)
	}
	n.reportPendingRebootResults(context.Background())
	pending := n.commandStore.rebootPending()
	if len(pending) != 1 {
		t.Fatalf("must remain pending: %+v", pending)
	}
	if pending[0].ReturnedAt == "" {
		t.Fatal("ReturnedAt must be stamped on BootID change")
	}
	if codes := cap.codes(); len(codes) != 0 {
		t.Fatalf("must not fail immediately: %v", codes)
	}
}

func TestRebootLaterHealthySuccess(t *testing.T) {
	n, cap := newLifecycleNode(t)
	n.cpOK.Store(true)
	orig := bootIDReader
	bootIDReader = func() string { return "boot-after" }
	t.Cleanup(func() { bootIDReader = orig })

	if err := n.commandStore.addRebootPending("cmd-rb-ok", "boot-before"); err != nil {
		t.Fatal(err)
	}
	n.reportPendingRebootResults(context.Background())
	codes := cap.codes()
	if len(codes) != 1 || codes[0] != "rebooted_healthy" {
		t.Fatalf("want rebooted_healthy got %v", codes)
	}
	if len(n.commandStore.rebootPending()) != 0 {
		t.Fatal("pending should clear after terminal success")
	}
}

func TestRebootTimeoutFailure(t *testing.T) {
	n, cap := newLifecycleNode(t)
	n.cpOK.Store(false)
	origBoot := bootIDReader
	bootIDReader = func() string { return "boot-after" }
	t.Cleanup(func() { bootIDReader = origBoot })

	fixed := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	origNow := nowFunc
	nowFunc = func() time.Time { return fixed }
	t.Cleanup(func() { nowFunc = origNow })

	if err := n.commandStore.addRebootPending("cmd-rb-to", "boot-before"); err != nil {
		t.Fatal(err)
	}
	// First pass: mark returned, still within grace.
	n.reportPendingRebootResults(context.Background())
	if codes := cap.codes(); len(codes) != 0 {
		t.Fatalf("within grace: %v", codes)
	}
	// Advance past grace.
	nowFunc = func() time.Time { return fixed.Add(rebootHealthGrace + time.Second) }
	n.reportPendingRebootResults(context.Background())
	codes := cap.codes()
	if len(codes) != 1 || codes[0] != "reboot_unhealthy" {
		t.Fatalf("want reboot_unhealthy got %v", codes)
	}
	if cap.results[0].Success {
		t.Fatal("reboot_unhealthy must be success=false")
	}
}

func TestRebootProcessRestartDuringGraceContinues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "commands-state.json")
	store := newCommandDedupeStore(path)
	if err := store.addRebootPending("cmd-rb-dur", "boot-before"); err != nil {
		t.Fatal(err)
	}
	if err := store.markRebootReturned("cmd-rb-dur", time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	reloaded := newCommandDedupeStore(path)
	pending := reloaded.rebootPending()
	if len(pending) != 1 || pending[0].ReturnedAt == "" || pending[0].PreBootID != "boot-before" {
		t.Fatalf("durable reboot recovery lost: %+v", pending)
	}
}

func TestRestartTimeoutFailure(t *testing.T) {
	n, cap := newLifecycleNode(t)
	n.cpOK.Store(false)
	fixed := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	origNow := nowFunc
	nowFunc = func() time.Time { return fixed }
	t.Cleanup(func() { nowFunc = origNow })

	if err := n.commandStore.addRestartPending("cmd-rst", fixed.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	n.reportPendingRestartResults(context.Background())
	if codes := cap.codes(); len(codes) != 0 {
		t.Fatalf("within grace: %v", codes)
	}
	nowFunc = func() time.Time { return fixed.Add(restartHealthGrace + time.Second) }
	n.reportPendingRestartResults(context.Background())
	codes := cap.codes()
	if len(codes) != 1 || codes[0] != "restart_timeout" {
		t.Fatalf("want restart_timeout got %v", codes)
	}
}

func TestRestartHealthySuccess(t *testing.T) {
	n, cap := newLifecycleNode(t)
	n.cpOK.Store(true)
	if err := n.commandStore.addRestartPending("cmd-rst-ok", time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	n.commandStore.data.RestartPending[0].OriginRuntimeID = "previous-process"
	n.reportPendingRestartResults(context.Background())
	codes := cap.codes()
	if len(codes) != 1 || codes[0] != "restarted_healthy" {
		t.Fatalf("want restarted_healthy got %v", codes)
	}
}

func TestRestartOldHealthyRuntimeCannotReportSuccess(t *testing.T) {
	n, cap := newLifecycleNode(t)
	if got := n.Status().ManagementCapabilities; got != managementCapabilitiesList {
		t.Fatalf("status capabilities: %q", got)
	}
	n.cpOK.Store(true)
	if err := n.commandStore.addRestartPending("same-runtime", time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	n.commandStore = newCommandDedupeStore(n.commandStore.path)
	n.reportPendingRestartResults(context.Background())
	if codes := cap.codes(); len(codes) != 0 {
		t.Fatalf("old process reported restart: %v", codes)
	}
	if got := n.commandStore.restartPending(); len(got) != 1 || got[0].OriginRuntimeID != runtimeInstanceID {
		t.Fatal("durable process identity lost")
	}
}

func TestTerminalResultJournalFailure(t *testing.T) {
	n, cap := newLifecycleNode(t)
	n.cpOK.Store(true)
	// Force durable journal path into a non-writable location.
	bad := filepath.Join(t.TempDir(), "missing", "nested", "commands-state.json")
	if err := os.MkdirAll(filepath.Dir(bad), 0o500); err == nil {
		// Make parent unwritable where possible (POSIX).
		_ = os.Chmod(filepath.Dir(filepath.Dir(bad)), 0o500)
	}
	n.commandStore = newCommandDedupeStore(filepath.Join(t.TempDir(), "ro", "commands-state.json"))
	// Replace save by pointing at a file under a file-as-directory trick.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	n.commandStore = newCommandDedupeStore(filepath.Join(blocker, "commands-state.json"))

	err := n.persistOrReportResult(context.Background(), pendingResultRecord{
		CommandID:     "cmd-dur",
		Success:       true,
		ResultCode:    "ok",
		ResultMessage: "should not reach CP without durable journal",
	})
	if err == nil {
		t.Fatal("expected durable journal failure")
	}
	if codes := cap.codes(); len(codes) != 0 {
		t.Fatalf("must not POST CP when durable persist fails: %v", codes)
	}

	// Happy path: durable first, then CP unavailable → result survives for retry.
	n2, _ := newLifecycleNode(t)
	srvDown := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srvDown.Close)
	cp, err := controlplane.NewClient(srvDown.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	cp.NodeID = "n-test"
	cp.PrivateKey = priv
	n2.cp = cp
	if err := n2.persistOrReportResult(context.Background(), pendingResultRecord{
		CommandID:     "cmd-retry",
		Success:       false,
		ResultCode:    "restart_timeout",
		ResultMessage: "queued",
	}); err != nil {
		t.Fatal(err)
	}
	pending := n2.commandStore.pendingResults()
	if len(pending) != 1 || pending[0].ResultCode != "restart_timeout" {
		t.Fatalf("result must survive CP outage: %+v", pending)
	}
	// Duplicate command must not re-execute once marked.
	if !n2.commandStore.TryMarkExecuted("cmd-retry") {
		t.Fatal("first mark should succeed")
	}
	if n2.commandStore.TryMarkExecuted("cmd-retry") {
		t.Fatal("duplicate must not execute again")
	}
}

func TestUpdateBridgeFromCtlTransaction(t *testing.T) {
	n, cap := newLifecycleNode(t)
	n.cpOK.Store(true)
	prev := version.ServerVersion
	if err := n.writeUpdateMarker(updateMarker{
		CommandID:       "cmd-bridge",
		PreviousVersion: prev,
		TargetVersion:   "9.9.9",
		Phase:           updatePhaseDownloading,
		StartedAt:       time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	txnDir := filepath.Join(filepath.Dir(n.opts.KeyPath), "update-transactions")
	if err := os.MkdirAll(txnDir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{
		"phase":          "committed",
		"target_version": "9.9.9",
	})
	if err := os.WriteFile(filepath.Join(txnDir, "tx1.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	n.completePendingUpdate(context.Background())
	got, ok := n.readUpdateMarker()
	if !ok {
		t.Fatal("marker missing")
	}
	if normalizeUpdatePhase(got.Phase) != updatePhaseUpdatedHealthy {
		t.Fatalf("expected bridge to updated_healthy, got %q", got.Phase)
	}
	if codes := cap.codes(); len(codes) != 0 {
		t.Fatalf("should wait for version match after bridge: %v", codes)
	}
}
