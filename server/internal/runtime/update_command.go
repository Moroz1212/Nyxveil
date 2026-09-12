package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/nyxveil/server/internal/controlplane"
	"github.com/nyxveil/server/internal/filemeta"
	"github.com/nyxveil/server/internal/paths"
	"github.com/nyxveil/server/internal/updater"
	"github.com/nyxveil/server/internal/version"
)

const (
	nyxveilUpdateUnit = "nyxveil-update.service"
	updateMarkerName  = "update-command.json"

	updatePhaseStarting          = "starting"
	updatePhaseDownloading       = "downloading"
	updatePhaseVerifying         = "verifying"
	updatePhaseInstalling        = "installing"
	updatePhaseRestarting        = "restarting"
	updatePhasePostCheck         = "post_check"
	updatePhaseUpdatedHealthy    = "updated_healthy"
	updatePhaseRollingBack       = "rolling_back"
	updatePhaseRolledBackHealthy = "rolled_back_healthy"
	updatePhaseRollbackFailed    = "rollback_failed"
	updatePhaseResultPending     = "result_pending"

	// Legacy phase written by 1.1.11 pre-fix runtimes (normalized via ToLower).
	updatePhaseLegacyDownloading = "Downloading"
)

type updateMarker struct {
	FailureReason   string `json:"failure_reason,omitempty"`
	CommandID       string `json:"command_id"`
	PreviousVersion string `json:"previous_version"`
	TargetVersion   string `json:"target_version"`
	Phase           string `json:"phase"`
	StartedAt       string `json:"started_at"`
	LastUpdatedAt   string `json:"last_updated_at,omitempty"`
	ResultPending   bool   `json:"result_pending,omitempty"`
	ResultSuccess   bool   `json:"result_success,omitempty"`
	ResultCode      string `json:"result_code,omitempty"`
	ResultMessage   string `json:"result_message,omitempty"`
}

func normalizeUpdatePhase(phase string) string {
	// Preserve recognition of the historical Title-Case marker value.
	if phase == updatePhaseLegacyDownloading {
		return updatePhaseDownloading
	}
	p := strings.TrimSpace(strings.ToLower(phase))
	switch p {
	case "downloading":
		return updatePhaseDownloading
	case "starting":
		return updatePhaseStarting
	case "verifying":
		return updatePhaseVerifying
	case "installing", "assets_installed":
		return updatePhaseInstalling
	case "restarting", "resuming":
		return updatePhaseRestarting
	case "post_check":
		return updatePhasePostCheck
	case "updated_healthy", "committed":
		return updatePhaseUpdatedHealthy
	case "rolling_back":
		return updatePhaseRollingBack
	case "rolled_back_healthy", "rolled_back":
		return updatePhaseRolledBackHealthy
	case "rollback_failed":
		return updatePhaseRollbackFailed
	case "result_pending":
		return updatePhaseResultPending
	default:
		return p
	}
}

func updatePhaseIsNonTerminal(phase string) bool {
	switch normalizeUpdatePhase(phase) {
	case updatePhaseStarting, updatePhaseDownloading, updatePhaseVerifying,
		updatePhaseInstalling, updatePhaseRestarting, updatePhasePostCheck,
		updatePhaseRollingBack:
		return true
	default:
		return false
	}
}

func (n *Node) executeUpdateNodeLatest(ctx context.Context, cmd *controlplane.NodeCommand) {
	if cmd == nil {
		return
	}
	commandID := cmd.ID
	prev := version.ServerVersion
	log.Printf("runtime: update %s phase=starting", commandID)

	target := strings.TrimSpace(strings.TrimPrefix(cmd.TargetVersion, "v"))
	if target == "" {
		if !n.commandStore.TryMarkExecuted(commandID) {
			return
		}
		n.reportCommandFailure(ctx, commandID, "target_missing",
			"UpdateNodeLatest requires pinned target_version from Control Plane")
		return
	}

	cmp, err := compareSemVer(prev, target)
	if err != nil {
		if !n.commandStore.TryMarkExecuted(commandID) {
			return
		}
		n.reportCommandFailure(ctx, commandID, "version_parse", err.Error())
		return
	}
	if cmp == 0 {
		if !n.commandStore.TryMarkExecuted(commandID) {
			return
		}
		n.reportCommandSuccess(ctx, commandID, "already_current", "Already up to date: "+prev)
		return
	}
	if cmp > 0 {
		if !n.commandStore.TryMarkExecuted(commandID) {
			return
		}
		n.reportCommandSuccess(ctx, commandID, "ahead",
			"Node version is newer than pinned target; downgrade blocked")
		return
	}

	// Durable update marker BEFORE MarkExecuted — crash recovery needs TargetVersion.
	if err := n.writeUpdateMarker(updateMarker{
		CommandID:       commandID,
		PreviousVersion: prev,
		TargetVersion:   target,
		Phase:           updatePhaseDownloading,
		StartedAt:       time.Now().UTC().Format(time.RFC3339),
		LastUpdatedAt:   time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		n.reportCommandFailure(ctx, commandID, "marker_write", err.Error())
		return
	}
	if !n.commandStore.TryMarkExecuted(commandID) {
		return
	}
	n.reportUpdateProgress(ctx, commandID, updatePhaseDownloading)

	if err := startUpdateUnit(); err != nil {
		_ = n.clearUpdateMarker()
		n.reportCommandFailure(ctx, commandID, "update_unit_failed",
			"nyxveil-update.service required for remote update: "+err.Error())
		return
	}
	log.Printf("runtime: update %s started %s target=%s phase=%s", commandID, nyxveilUpdateUnit, target, updatePhaseDownloading)
}

func (n *Node) completePendingUpdate(ctx context.Context) {
	// Consume already-queued journal results whose pending CP POST finished.
	n.finalizeReportedUpdateTransactions()

	m, ok := n.readUpdateMarker()
	if ok && strings.TrimSpace(m.CommandID) != "" {
		n.completePendingUpdateFromMarker(ctx, m)
	}
	// Recover terminal outcomes when the legacy parent deleted update-command.json
	// but a correlated transaction journal still carries the CommandID.
	n.recoverCorrelatedTerminalUpdates(ctx)
}

func (n *Node) completePendingUpdateFromMarker(ctx context.Context, m updateMarker) {
	phase := normalizeUpdatePhase(m.Phase)
	if m.ResultPending || phase == updatePhaseResultPending {
		req := controlplane.NodeCommandResultRequest{
			Success:       m.ResultSuccess,
			ResultCode:    m.ResultCode,
			ResultMessage: m.ResultMessage,
		}
		if err := n.cp.ReportCommandResult(ctx, m.CommandID, req); err != nil {
			log.Printf("runtime: update result retry %s: %v", m.CommandID, err)
			return
		}
		_ = n.clearUpdateMarker()
		n.consumeUpdateTransactionsForCommand(m.CommandID)
		return
	}

	// Bridge: adopt terminal phase from nyxveilctl update transaction journal.
	if bridged, changed := n.bridgeUpdatePhaseFromCtl(m); changed {
		m = bridged
		phase = normalizeUpdatePhase(m.Phase)
		if err := n.writeUpdateMarker(m); err != nil {
			log.Printf("runtime: update marker bridge write: %v", err)
			return
		}
	}

	// Non-terminal: never infer rolled_back_healthy from previous==current.
	if updatePhaseIsNonTerminal(phase) {
		n.reportUpdateProgress(ctx, m.CommandID, phase)
		return
	}

	cur := version.ServerVersion
	target := strings.TrimPrefix(strings.TrimSpace(m.TargetVersion), "v")
	prev := strings.TrimPrefix(strings.TrimSpace(m.PreviousVersion), "v")
	got := strings.TrimPrefix(strings.TrimSpace(cur), "v")

	st := n.Status()
	st.Healthy = st.ComputeHealthy()
	cpOK := n.cpOK.Load()

	switch phase {
	case updatePhaseUpdatedHealthy:
		if got == target && st.Healthy && cpOK {
			n.finishUpdateLocal(ctx, m, true, "updated_healthy",
				"Runtime version and health confirmed: "+cur)
			return
		}
		// Executor claimed success but runtime not ready yet — wait.
		return
	case updatePhaseRolledBackHealthy:
		if got == prev && st.Healthy && cpOK {
			n.finishUpdateLocal(ctx, m, false, "rolled_back_healthy",
				"Runtime rolled back and healthy at previous version: "+cur+"; "+m.FailureReason)
			return
		}
		return
	case updatePhaseRollbackFailed:
		n.finishUpdateLocal(ctx, m, false, "rollback_failed",
			"Update executor reported rollback_failed: "+m.FailureReason)
		return
	default:
		// Unknown terminal-ish phase: do not guess rollback from version equality.
		return
	}
}

func (n *Node) reportUpdateProgress(ctx context.Context, commandID, phase string) {
	var message string
	switch normalizeUpdatePhase(phase) {
	case updatePhaseDownloading:
		message = "Downloading release assets"
	case updatePhaseVerifying:
		message = "Verifying signed release assets"
	case updatePhaseInstalling:
		message = "Installing verified release assets"
	case updatePhaseRestarting:
		message = "Restarting Nyxveil service"
	case updatePhasePostCheck:
		message = "Running post-update health checks"
	default:
		return
	}
	if n.cp == nil {
		return
	}
	err := n.cp.ReportCommandProgress(ctx, commandID, controlplane.NodeCommandProgressRequest{
		Phase:   normalizeUpdatePhase(phase),
		Message: message,
	})
	if err != nil {
		log.Printf("runtime: update progress %s phase=%s: %v", commandID, phase, err)
	}
}

// ctlTxnWire is the subset of nyxveilctl updateTransaction JSON needed for recovery.
type ctlTxnWire struct {
	CommandID        string    `json:"command_id"`
	CommandStartedAt string    `json:"command_started_at"`
	ResultQueuedAt   string    `json:"result_queued_at"`
	ResultReportedAt string    `json:"result_reported_at"`
	TerminalOutcome  string    `json:"terminal_outcome"`
	FailureReason    string    `json:"failure_reason"`
	ID               string    `json:"id"`
	TargetVersion    string    `json:"target_version"`
	PreviousVersion  string    `json:"previous_version"`
	Phase            string    `json:"phase"`
	CreatedAt        time.Time `json:"created_at"`
	PreBaseline      struct {
		NodeID string `json:"node_id"`
	} `json:"pre_baseline"`
}

func (n *Node) updateTransactionDir() string {
	stateDir := filepath.Dir(paths.CommandsState())
	if n.opts.KeyPath != "" {
		stateDir = filepath.Dir(n.opts.KeyPath)
	}
	return filepath.Join(stateDir, "update-transactions")
}

func (tx *ctlTxnWire) effectivePhase() string {
	if tx.TerminalOutcome == updatePhaseRollbackFailed || tx.TerminalOutcome == updatePhaseRolledBackHealthy {
		return tx.TerminalOutcome
	}
	return normalizeUpdatePhase(tx.Phase)
}

func (tx *ctlTxnWire) isTerminal() bool {
	switch tx.effectivePhase() {
	case updatePhaseUpdatedHealthy, updatePhaseRolledBackHealthy, updatePhaseRollbackFailed:
		return true
	default:
		return false
	}
}

func (n *Node) listUpdateTransactions() []ctlTxnWire {
	dir := n.updateTransactionDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]ctlTxnWire, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var tx ctlTxnWire
		if json.Unmarshal(raw, &tx) != nil {
			continue
		}
		if strings.TrimSpace(tx.ID) == "" {
			tx.ID = strings.TrimSuffix(e.Name(), ".json")
		}
		out = append(out, tx)
	}
	return out
}

func (n *Node) writeUpdateTransactionWire(tx ctlTxnWire) error {
	if strings.TrimSpace(tx.ID) == "" {
		return fmt.Errorf("transaction id required")
	}
	dir := n.updateTransactionDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, tx.ID+".json")
	// Preserve unknown fields by merging onto the existing document when present.
	var existing map[string]json.RawMessage
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &existing)
	}
	if existing == nil {
		existing = map[string]json.RawMessage{}
	}
	patch, err := json.Marshal(tx)
	if err != nil {
		return err
	}
	var overlay map[string]json.RawMessage
	if err := json.Unmarshal(patch, &overlay); err != nil {
		return err
	}
	for k, v := range overlay {
		existing[k] = v
	}
	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	if err := filemeta.DurableWrite(path, out, 0o600); err != nil {
		return err
	}
	uid, gid, _ := filemeta.LookupServiceIDs()
	_ = filemeta.ApplyOwnerMode(dir, uid, gid, 0o700)
	_ = filemeta.ApplyOwnerMode(path, uid, gid, 0o600)
	return nil
}

func (n *Node) removeUpdateTransactionID(id string) {
	if strings.TrimSpace(id) == "" {
		return
	}
	_ = os.Remove(filepath.Join(n.updateTransactionDir(), id+".json"))
}

func (n *Node) consumeUpdateTransactionsForCommand(commandID string) {
	commandID = strings.TrimSpace(commandID)
	if commandID == "" {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, tx := range n.listUpdateTransactions() {
		if strings.TrimSpace(tx.CommandID) != commandID {
			continue
		}
		tx.ResultReportedAt = now
		if err := n.writeUpdateTransactionWire(tx); err != nil {
			log.Printf("runtime: mark transaction reported %s: %v", tx.ID, err)
			continue
		}
		n.removeUpdateTransactionID(tx.ID)
	}
}

func (n *Node) finalizeReportedUpdateTransactions() {
	n.ensureCommandStore()
	pending := map[string]struct{}{}
	for _, p := range n.commandStore.pendingResults() {
		pending[strings.TrimSpace(p.CommandID)] = struct{}{}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, tx := range n.listUpdateTransactions() {
		cmd := strings.TrimSpace(tx.CommandID)
		if cmd == "" {
			continue
		}
		if strings.TrimSpace(tx.ResultReportedAt) != "" {
			n.removeUpdateTransactionID(tx.ID)
			continue
		}
		if strings.TrimSpace(tx.ResultQueuedAt) == "" {
			continue
		}
		if _, stillPending := pending[cmd]; stillPending {
			continue
		}
		// Pending result gone ⇒ CP accepted (or identical replay). Consume journal.
		tx.ResultReportedAt = now
		_ = n.writeUpdateTransactionWire(tx)
		n.removeUpdateTransactionID(tx.ID)
	}
}

// recoverCorrelatedTerminalUpdates reports terminal ctl outcomes when the marker
// is gone but the transaction journal still carries an exact CommandID.
func (n *Node) recoverCorrelatedTerminalUpdates(ctx context.Context) {
	txs := n.listUpdateTransactions()
	byCmd := map[string][]ctlTxnWire{}
	for _, tx := range txs {
		cmd := strings.TrimSpace(tx.CommandID)
		if cmd == "" {
			// Historical journals without correlation cannot safely identify a CP command.
			continue
		}
		if strings.TrimSpace(tx.ResultReportedAt) != "" {
			continue
		}
		if !tx.isTerminal() {
			continue
		}
		byCmd[cmd] = append(byCmd[cmd], tx)
	}
	if len(byCmd) == 0 {
		return
	}

	st := n.Status()
	st.Healthy = st.ComputeHealthy()
	cpOK := n.cpOK.Load()
	got := strings.TrimPrefix(strings.TrimSpace(version.ServerVersion), "v")
	nodeID := ""
	if n.local != nil {
		nodeID = strings.TrimSpace(n.local.NodeID)
	}
	if nodeID == "" && n.cp != nil {
		nodeID = strings.TrimSpace(n.cp.NodeID)
	}

	for cmd, list := range byCmd {
		if len(list) != 1 {
			log.Printf("runtime: update recovery refuse ambiguous journals for command %s count=%d", cmd, len(list))
			continue
		}
		tx := list[0]
		if strings.TrimSpace(tx.ResultQueuedAt) != "" {
			// Already durably queued; flushPendingCommandResults owns delivery.
			continue
		}
		if txNode := strings.TrimSpace(tx.PreBaseline.NodeID); txNode != "" && nodeID != "" && txNode != nodeID {
			log.Printf("runtime: update recovery refuse node mismatch command=%s journal=%s runtime=%s",
				cmd, txNode, nodeID)
			continue
		}
		phase := tx.effectivePhase()
		target := strings.TrimPrefix(strings.TrimSpace(tx.TargetVersion), "v")
		prev := strings.TrimPrefix(strings.TrimSpace(tx.PreviousVersion), "v")
		var success bool
		var code, message string
		switch phase {
		case updatePhaseUpdatedHealthy:
			if got != target || !st.Healthy || !cpOK {
				continue
			}
			success = true
			code = "updated_healthy"
			message = "Recovered committed update from correlated transaction; runtime healthy at " + version.ServerVersion
		case updatePhaseRolledBackHealthy:
			if got != prev || !st.Healthy || !cpOK {
				continue
			}
			success = false
			code = "rolled_back_healthy"
			message = "Recovered healthy rollback from correlated transaction at " + version.ServerVersion
			if tx.FailureReason != "" {
				message += "; " + tx.FailureReason
			}
		case updatePhaseRollbackFailed:
			success = false
			code = "rollback_failed"
			message = "Recovered rollback_failed from correlated transaction"
			if tx.FailureReason != "" {
				message += ": " + tx.FailureReason
			}
		default:
			continue
		}
		n.finishUpdateFromCorrelatedTransaction(ctx, tx, success, code, message)
	}
}

func (n *Node) finishUpdateFromCorrelatedTransaction(ctx context.Context, tx ctlTxnWire, success bool, code, message string) {
	n.ensureCommandStore()
	rec := pendingResultRecord{
		CommandID:     strings.TrimSpace(tx.CommandID),
		Success:       success,
		ResultCode:    code,
		ResultMessage: message,
		BootID:        readBootID(),
	}
	if err := n.commandStore.addPendingResult(rec); err != nil {
		log.Printf("runtime: CRITICAL correlated update pending write failed %s: %v", rec.CommandID, err)
		return
	}
	tx.ResultQueuedAt = time.Now().UTC().Format(time.RFC3339)
	if err := n.writeUpdateTransactionWire(tx); err != nil {
		log.Printf("runtime: CRITICAL correlated update queue mark failed %s: %v", tx.ID, err)
		// Keep pending result; do not clear evidence.
		return
	}
	err := n.cp.ReportCommandResult(ctx, rec.CommandID, controlplane.NodeCommandResultRequest{
		Success:       rec.Success,
		ResultCode:    rec.ResultCode,
		ResultMessage: rec.ResultMessage,
		BootID:        rec.BootID,
	})
	if err != nil {
		log.Printf("runtime: correlated update result %s queued for retry: %v", rec.CommandID, err)
		return
	}
	if remErr := n.commandStore.removePendingResult(rec.CommandID); remErr != nil {
		log.Printf("runtime: pending result remove failed %s: %v", rec.CommandID, remErr)
	}
	tx.ResultReportedAt = time.Now().UTC().Format(time.RFC3339)
	_ = n.writeUpdateTransactionWire(tx)
	n.removeUpdateTransactionID(tx.ID)
	// Marker may already be gone (legacy race); clear if it still points at this command.
	if m, ok := n.readUpdateMarker(); ok && strings.TrimSpace(m.CommandID) == rec.CommandID {
		_ = n.clearUpdateMarker()
	}
}

// bridgeUpdatePhaseFromCtl maps nyxveilctl update-transaction phases onto the
// runtime marker so there is one authoritative lifecycle for CP results.
func (n *Node) bridgeUpdatePhaseFromCtl(m updateMarker) (updateMarker, bool) {
	started, parseErr := time.Parse(time.RFC3339, m.StartedAt)
	if parseErr != nil {
		return m, false
	}
	cmdID := strings.TrimSpace(m.CommandID)
	target := strings.TrimPrefix(strings.TrimSpace(m.TargetVersion), "v")

	var exactPhase, latestPhase string
	var exactReason, latestReason string
	var latestMod time.Time
	exactFound := false

	dir := n.updateTransactionDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return m, false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var tx ctlTxnWire
		if json.Unmarshal(raw, &tx) != nil {
			continue
		}
		phase := tx.effectivePhase()
		txCmd := strings.TrimSpace(tx.CommandID)
		if txCmd != "" && cmdID != "" {
			if txCmd != cmdID {
				continue
			}
			// Exact CommandID correlation wins over time/target heuristics.
			exactPhase = phase
			exactReason = tx.FailureReason
			exactFound = true
			continue
		}
		if !tx.CreatedAt.IsZero() && tx.CreatedAt.Before(started) {
			continue
		}
		if info.ModTime().Before(started) {
			continue
		}
		if strings.TrimPrefix(strings.TrimSpace(tx.TargetVersion), "v") != target {
			continue
		}
		if info.ModTime().After(latestMod) {
			latestMod = info.ModTime()
			latestPhase = phase
			latestReason = tx.FailureReason
		}
	}
	chosenPhase := latestPhase
	chosenReason := latestReason
	if exactFound {
		chosenPhase = exactPhase
		chosenReason = exactReason
	}
	if chosenPhase == "" {
		return m, false
	}
	mapped := normalizeUpdatePhase(chosenPhase)
	if mapped == "" || mapped == normalizeUpdatePhase(m.Phase) {
		return m, false
	}
	// Only advance toward terminal / progress phases from ctl.
	switch mapped {
	case updatePhaseInstalling, updatePhaseRestarting, updatePhasePostCheck,
		updatePhaseUpdatedHealthy, updatePhaseRollingBack,
		updatePhaseRolledBackHealthy, updatePhaseRollbackFailed:
		m.Phase = mapped
		m.FailureReason = chosenReason
		m.LastUpdatedAt = time.Now().UTC().Format(time.RFC3339)
		return m, true
	default:
		return m, false
	}
}

func (n *Node) finishUpdateLocal(ctx context.Context, m updateMarker, success bool, code, message string) {
	m.ResultPending = true
	m.ResultSuccess = success
	m.ResultCode = code
	m.ResultMessage = message
	m.Phase = updatePhaseResultPending
	m.LastUpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := n.writeUpdateMarker(m); err != nil {
		log.Printf("runtime: update marker result write FAILED (not reporting): %v", err)
		return
	}
	n.completePendingUpdateFromMarker(ctx, m)
}

func startUpdateUnit() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("update unit unsupported on %s", runtime.GOOS)
	}
	cmd := exec.Command("systemctl", "start", "--no-block", nyxveilUpdateUnit)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func compareSemVer(a, b string) (int, error) {
	pa, err := parseSemVerParts(a)
	if err != nil {
		return 0, err
	}
	pb, err := parseSemVerParts(b)
	if err != nil {
		return 0, err
	}
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1, nil
		}
		if pa[i] > pb[i] {
			return 1, nil
		}
	}
	return 0, nil
}

func parseSemVerParts(v string) ([3]int, error) {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V"))
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("invalid semver %q", v)
	}
	for i := 0; i < 3; i++ {
		var n int
		if _, err := fmt.Sscanf(parts[i], "%d", &n); err != nil {
			return out, err
		}
		out[i] = n
	}
	return out, nil
}

func (n *Node) updateMarkerPath() string {
	base := filepath.Dir(paths.CommandsState())
	if n.opts.KeyPath != "" {
		base = filepath.Dir(n.opts.KeyPath)
	}
	return filepath.Join(base, "management", updateMarkerName)
}

func (n *Node) writeUpdateMarker(m updateMarker) error {
	path := n.updateMarkerPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	// Crash-sensitive journal: DurableWrite (fsync temp + rename + dir fsync).
	if err := filemeta.DurableWrite(path, raw, 0o600); err != nil {
		return err
	}
	uid, gid, _ := filemeta.LookupServiceIDs()
	_ = filemeta.ApplyOwnerMode(path, uid, gid, 0o600)
	return nil
}

func (n *Node) readUpdateMarker() (updateMarker, bool) {
	raw, err := os.ReadFile(n.updateMarkerPath())
	if err != nil {
		return updateMarker{}, false
	}
	var m updateMarker
	if json.Unmarshal(raw, &m) != nil {
		return updateMarker{}, false
	}
	return m, true
}

func (n *Node) clearUpdateMarker() error {
	err := os.Remove(n.updateMarkerPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ReadPinnedUpdateTarget exposes the durable marker target for nyxveilctl update.
func ReadPinnedUpdateTarget(stateDir string) (string, bool) {
	path := filepath.Join(stateDir, "management", updateMarkerName)
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var m updateMarker
	if json.Unmarshal(raw, &m) != nil {
		return "", false
	}
	v := strings.TrimSpace(strings.TrimPrefix(m.TargetVersion, "v"))
	if v == "" {
		return "", false
	}
	return v, true
}

// ManifestURLForPinnedTarget builds the fixed-repo manifest URL for a pinned version.
func ManifestURLForPinnedTarget(ver string) string {
	return updater.ManifestURLForVersion(ver)
}
