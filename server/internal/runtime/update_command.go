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

func (n *Node) executeUpdateNodeLatest(ctx context.Context, cmd *controlplane.NodeCommand) {
	if cmd == nil {
		return
	}
	commandID := cmd.ID
	prev := version.ServerVersion
	log.Printf("runtime: update %s phase=CheckingLatest", commandID)

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
		Phase:           "Downloading",
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
	log.Printf("runtime: update %s started %s target=%s", commandID, nyxveilUpdateUnit, target)
}

func (n *Node) completePendingUpdate(ctx context.Context) {
	m, ok := n.readUpdateMarker()
	if !ok || strings.TrimSpace(m.CommandID) == "" {
		return
	}

	if m.ResultPending {
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

	cur := version.ServerVersion
	target := strings.TrimPrefix(strings.TrimSpace(m.TargetVersion), "v")
	prev := strings.TrimPrefix(strings.TrimSpace(m.PreviousVersion), "v")
	got := strings.TrimPrefix(strings.TrimSpace(cur), "v")

	st := n.Status()
	st.Healthy = st.ComputeHealthy()

	switch {
	case got == prev && st.Healthy:
		// Update failed; previous release restored and proven healthy.
		n.finishUpdateLocal(ctx, m, false, "rolled_back_healthy",
			"Runtime rolled back and healthy at previous version: "+cur)
	case got == target && st.Healthy:
		n.finishUpdateLocal(ctx, m, true, "updated_healthy",
			"Runtime version and health confirmed: "+cur)
	default:
		n.finishUpdateLocal(ctx, m, false, "outcome_unknown",
			fmt.Sprintf("update outcome ambiguous: current=%s target=%s previous=%s healthy=%v",
				cur, m.TargetVersion, m.PreviousVersion, st.Healthy))
	}
}

func (n *Node) finishUpdateLocal(ctx context.Context, m updateMarker, success bool, code, message string) {
	m.ResultPending = true
	m.ResultSuccess = success
	m.ResultCode = code
	m.ResultMessage = message
	m.Phase = "ResultPending"
	m.LastUpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := n.writeUpdateMarker(m); err != nil {
		log.Printf("runtime: update marker result write: %v", err)
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
