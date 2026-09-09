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

	if err := startUpdateUnit(); err != nil {
		_ = n.clearUpdateMarker()
		n.reportCommandFailure(ctx, commandID, "update_unit_failed",
			"nyxveil-update.service required for remote update: "+err.Error())
		return
	}
	log.Printf("runtime: update %s started %s target=%s phase=%s", commandID, nyxveilUpdateUnit, target, updatePhaseDownloading)
}

func (n *Node) completePendingUpdate(ctx context.Context) {
	m, ok := n.readUpdateMarker()
	if !ok || strings.TrimSpace(m.CommandID) == "" {
		return
	}

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
				"Runtime rolled back and healthy at previous version: "+cur)
			return
		}
		return
	case updatePhaseRollbackFailed:
		n.finishUpdateLocal(ctx, m, false, "rollback_failed",
			"Update executor reported rollback_failed")
		return
	default:
		// Unknown terminal-ish phase: do not guess rollback from version equality.
		return
	}
}

// bridgeUpdatePhaseFromCtl maps nyxveilctl update-transaction phases onto the
// runtime marker so there is one authoritative lifecycle for CP results.
func (n *Node) bridgeUpdatePhaseFromCtl(m updateMarker) (updateMarker, bool) {
	stateDir := filepath.Dir(paths.CommandsState())
	if n.opts.KeyPath != "" {
		stateDir = filepath.Dir(n.opts.KeyPath)
	}
	txnDir := filepath.Join(stateDir, "update-transactions")
	entries, err := os.ReadDir(txnDir)
	if err != nil {
		return m, false
	}
	var latestPhase string
	var latestMod time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(txnDir, e.Name()))
		if err != nil {
			continue
		}
		var tx struct {
			Phase         string `json:"phase"`
			TargetVersion string `json:"target_version"`
		}
		if json.Unmarshal(raw, &tx) != nil {
			continue
		}
		if strings.TrimPrefix(strings.TrimSpace(tx.TargetVersion), "v") !=
			strings.TrimPrefix(strings.TrimSpace(m.TargetVersion), "v") {
			continue
		}
		if info.ModTime().After(latestMod) {
			latestMod = info.ModTime()
			latestPhase = tx.Phase
		}
	}
	if latestPhase == "" {
		return m, false
	}
	mapped := normalizeUpdatePhase(latestPhase)
	if mapped == "" || mapped == normalizeUpdatePhase(m.Phase) {
		return m, false
	}
	// Only advance toward terminal / progress phases from ctl.
	switch mapped {
	case updatePhaseInstalling, updatePhaseRestarting, updatePhasePostCheck,
		updatePhaseUpdatedHealthy, updatePhaseRollingBack,
		updatePhaseRolledBackHealthy, updatePhaseRollbackFailed:
		m.Phase = mapped
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
	n.completePendingUpdate(ctx)
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
