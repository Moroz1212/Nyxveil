package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/nyxveil/server/internal/filemeta"
	"github.com/nyxveil/server/internal/health"
	"github.com/nyxveil/server/internal/localconfig"
	"github.com/nyxveil/server/internal/paths"
	"github.com/nyxveil/server/internal/updater"
	"github.com/nyxveil/server/internal/version"
)

// updateTransaction journals a self-update across the old→new ctl process boundary.
// Only one process owns rollback at a time (see OwnerPID / Phase).
type updateTransaction struct {
	LegacyParent      bool                          `json:"legacy_parent,omitempty"`
	TerminalOutcome   string                        `json:"terminal_outcome,omitempty"`
	FailureReason     string                        `json:"failure_reason,omitempty"`
	ID                string                        `json:"id"`
	TargetVersion     string                        `json:"target_version"`
	ManifestURL       string                        `json:"manifest_url,omitempty"`
	LocalDir          string                        `json:"local_dir,omitempty"`
	ServerPath        string                        `json:"server_path"`
	CtlPath           string                        `json:"ctl_path"`
	CtlPrev           string                        `json:"ctl_prev"`
	PreBaseline       health.Baseline               `json:"pre_baseline"`
	PreTLS            filemeta.TLSOwnershipSnapshot `json:"pre_tls"`
	Phase             string                        `json:"phase"` // assets_installed|resuming|committed|rolling_back|rolled_back_healthy|rollback_failed
	OwnerPID          int                           `json:"owner_pid"`
	CreatedAt         time.Time                     `json:"created_at"`
	ProcessCLIAtStart string                        `json:"process_cli_at_start"`
	PreviousVersion   string                        `json:"previous_version,omitempty"`
}

const (
	txPhaseAssetsInstalled   = "assets_installed"
	txPhaseResuming          = "resuming"
	txPhaseCommitted         = "committed"
	txPhaseRollingBack       = "rolling_back"
	txPhaseRolledBackHealthy = "rolled_back_healthy"
	txPhaseRollbackFailed    = "rollback_failed"
	// txPhaseRolledBack is a legacy alias kept for older journals/tests.
	txPhaseRolledBack = "rolled_back"
)

// Test hooks (overridable).
var (
	rollbackEnforceTLS      = filemeta.EnforceRuntimeTLS
	rollbackConfirmPrevious = assertVersionsMatchTarget
	rollbackVerifyHealth    = func(pre health.Baseline, seconds int) (health.RollbackResult, bool) {
		return verifyRollbackHealth(pre, seconds)
	}
)

func txPath(id string) string {
	return filepath.Join(updateTxnDir(), id+".json")
}

func updateTxnDir() string {
	if v := strings.TrimSpace(os.Getenv("NYXVEIL_STATE_DIR")); v != "" {
		return filepath.Join(v, "update-transactions")
	}
	return paths.UpdateTransactionDir()
}

func updateTxnLockPath() string {
	if v := strings.TrimSpace(os.Getenv("NYXVEIL_STATE_DIR")); v != "" {
		return filepath.Join(v, "update-transaction.lock")
	}
	return paths.UpdateTransactionLock()
}

func writeUpdateTransaction(tx *updateTransaction) error {
	if err := os.MkdirAll(updateTxnDir(), 0o700); err != nil {
		return err
	}
	wire := *tx
	if tx.LegacyParent && (tx.Phase == txPhaseRolledBackHealthy || tx.Phase == txPhaseRollbackFailed) {
		// 1.1.9 parent recognizes only these ownership phases. Preserve the exact
		// outcome separately; rolling_back must never imply successful rollback.
		wire.TerminalOutcome = tx.Phase
		wire.Phase = txPhaseRollingBack
		if tx.Phase == txPhaseRolledBackHealthy {
			wire.Phase = txPhaseRolledBack
		}
	}
	b, err := json.MarshalIndent(&wire, "", "  ")
	if err != nil {
		return err
	}
	return filemeta.DurableWrite(txPath(tx.ID), b, 0o600)
}

func loadUpdateTransaction(id string) (*updateTransaction, error) {
	b, err := os.ReadFile(txPath(id))
	if err != nil {
		return nil, err
	}
	var tx updateTransaction
	if err := json.Unmarshal(b, &tx); err != nil {
		return nil, err
	}
	if tx.TerminalOutcome == txPhaseRollbackFailed || tx.TerminalOutcome == txPhaseRolledBackHealthy {
		tx.Phase = tx.TerminalOutcome
	}
	return &tx, nil
}

func removeUpdateTransaction(id string) {
	_ = os.Remove(txPath(id))
}

func acquireUpdateLock() (*os.File, error) {
	dir := filepath.Dir(updateTxnLockPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(updateTxnLockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFileExclusive(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func spawnUpdateResume(newCtl, txID string) (exitCode int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, newCtl, "update-resume", "--transaction", txID)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = append(os.Environ(), "NYXVEIL_UPDATE_RESUME=1")
	err = cmd.Run()
	if err == nil {
		return 0, nil
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), err
	}
	return 1, err
}

// handoffPostCheckToNewCtl runs the newly installed ctl to complete post-check + gate.
// On child success the parent exits 0 (does not return to Apply).
// On child failure without child-owned rollback, returns false so Apply rolls back.
func handoffPostCheckToNewCtl(tx *updateTransaction) bool {
	fmt.Printf("self-update handoff: process_cli=%s → exec %s update-resume --transaction %s\n",
		version.CLIVersion, tx.CtlPath, tx.ID)
	code, err := spawnUpdateResume(tx.CtlPath, tx.ID)
	if err == nil && code == 0 {
		os.Exit(0)
	}
	loaded, loadErr := loadUpdateTransaction(tx.ID)
	if loadErr == nil {
		switch loaded.Phase {
		case txPhaseRolledBackHealthy, txPhaseRolledBack, txPhaseRollingBack, txPhaseRollbackFailed, txPhaseResuming, txPhaseCommitted:
			fmt.Printf("self-update handoff: new ctl owns rollback (phase=%s); parent will not double-rollback\n", loaded.Phase)
			os.Exit(1)
		}
	}
	fmt.Printf("self-update handoff failed: %v (exit=%d); parent will rollback\n", err, code)
	removeUpdateTransaction(tx.ID)
	return false
}

func runUpdateResume(args []string) error {
	fs := flag.NewFlagSet("update-resume", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	txID := fs.String("transaction", "", "update transaction id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*txID) == "" {
		return fmt.Errorf("update-resume: --transaction is required")
	}

	lock, err := acquireUpdateLock()
	if err != nil {
		return fmt.Errorf("update-resume: lock: %w", err)
	}
	defer func() { _ = lock.Close() }()

	tx, err := loadUpdateTransaction(*txID)
	if err != nil {
		return fmt.Errorf("update-resume: load transaction: %w", err)
	}
	if tx.Phase != txPhaseAssetsInstalled && tx.Phase != txPhaseResuming {
		return fmt.Errorf("update-resume: unexpected phase %q", tx.Phase)
	}
	tx.Phase = txPhaseResuming
	tx.LegacyParent = tx.LegacyParent || !tx.PreBaseline.LifecycleKnown
	if tx.PreviousVersion == "" {
		tx.PreviousVersion = tx.ProcessCLIAtStart
	}
	tx.OwnerPID = os.Getpid()
	if err := writeUpdateTransaction(tx); err != nil {
		return err
	}
	if err := restoreLegacyLifecycle(tx); err != nil {
		tx.FailureReason = err.Error()
		return rollbackAcrossHandoff(tx)
	}

	fmt.Printf("update-resume: target=%s process_cli=%s (was %s at start)\n",
		tx.TargetVersion, version.CLIVersion, tx.ProcessCLIAtStart)

	if !performPostUpdateVerification(tx) {
		return rollbackAcrossHandoff(tx)
	}

	fmt.Printf("updated to %s\n", tx.TargetVersion)
	if err := afterSuccessfulUpdate(tx.TargetVersion); err != nil {
		tx.FailureReason = err.Error()
		return rollbackAcrossHandoff(tx)
	}

	_ = os.Remove(paths.RollbackMarker())
	tx.Phase = txPhaseCommitted
	if err := writeUpdateTransaction(tx); err != nil {
		return fmt.Errorf("update-resume: commit journal: %w", err)
	}
	// Retain the terminal journal until the daemon has durably reported the result.
	return nil
}

func performPostUpdateVerification(tx *updateTransaction) bool {
	// Windows and HTTP control-socket harnesses (CI/unit tests) have no systemd unit.
	if runtime.GOOS == "windows" || strings.TrimSpace(os.Getenv("NYXVEIL_CONTROL_HTTP")) != "" {
		if err := assertVersionsMatchTarget(tx.TargetVersion); err != nil {
			fmt.Printf("update_success=false reason=version_mismatch detail=%v\n", err)
			return false
		}
		fmt.Printf("update_success=true dataplane_healthy=%v management_plane_connected=%v preexisting_management_degradation=%v\n",
			tx.PreBaseline.DataplaneOK, true, !tx.PreBaseline.CPConnected)
		return true
	}
	prePID := unitMainPID("nyxveil-server")
	if err := verifyUpdateTLS(); err != nil {
		tx.FailureReason = "TLS validation: " + err.Error()
		return false
	}
	if err := restartUnit("nyxveil-server"); err != nil {
		tx.FailureReason = "restart failure: " + err.Error()
		fmt.Printf("update restart failed: %v\n", err)
		return false
	}
	res, ok := verifyPostUpdateHealth(tx.PreBaseline, 45)
	if !ok {
		tx.FailureReason = "health timeout: " + res.Reason
		return false
	}
	postPID := unitMainPID("nyxveil-server")
	if prePID > 0 && postPID > 0 && prePID == postPID {
		tx.FailureReason = "same PID after restart"
		fmt.Printf("update_success=false reason=same_pid_after_restart pre_pid=%d post_pid=%d\n", prePID, postPID)
		return false
	}
	if err := assertVersionsMatchTarget(tx.TargetVersion); err != nil {
		tx.FailureReason = "version mismatch: " + err.Error()
		fmt.Printf("update_success=false reason=version_mismatch detail=%v\n", err)
		return false
	}
	fmt.Printf("update_success=%v dataplane_healthy=%v management_plane_connected=%v preexisting_management_degradation=%v pre_pid=%d post_pid=%d\n",
		res.UpdateSuccess, res.DataplaneHealthy, res.ManagementPlaneConnected, res.PreexistingManagementDegradation, prePID, postPID)
	if res.Reason != "" {
		fmt.Printf("update note: %s\n", res.Reason)
	}
	return true
}

func markRollbackFailed(tx *updateTransaction, reason error) error {
	tx.FailureReason += "; rollback failure: " + reason.Error()
	tx.Phase = txPhaseRollbackFailed
	tx.OwnerPID = os.Getpid()
	if werr := writeUpdateTransaction(tx); werr != nil {
		return fmt.Errorf("rollback failed (%v); also journal write failed: %w", reason, werr)
	}
	return fmt.Errorf("update-resume failed; rollback_failed: %w", reason)
}

func rollbackAcrossHandoff(tx *updateTransaction) error {
	tx.Phase = txPhaseRollingBack
	tx.OwnerPID = os.Getpid()
	if err := writeUpdateTransaction(tx); err != nil {
		return fmt.Errorf("update-resume failed; could not journal rolling_back: %w", err)
	}

	fmt.Println("update-resume failed; rolling back previous binaries/TLS ownership…")
	stateDir := runtimeStateDir()
	prevServer := paths.PreviousBinary()
	marker := paths.RollbackMarker()
	if stateDir != paths.StateDir {
		prevServer = filepath.Join(stateDir, "nyxveil-server.prev")
		marker = filepath.Join(stateDir, "rollback.marker")
	}
	u := updater.New(tx.ServerPath, prevServer, marker)
	extraDest, extraPrev := paths.DefaultExtraInstallMaps()
	if stateDir != paths.StateDir {
		// Test / isolated layout: never touch host systemd or production install paths.
		u.DaemonReload = func() error { return nil }
		for name := range extraDest {
			if name == "nyxveilctl" {
				continue
			}
			extraDest[name] = filepath.Join(stateDir, name)
			extraPrev[name] = filepath.Join(stateDir, name+".prev")
		}
	}
	extraDest["nyxveilctl"] = tx.CtlPath
	extraPrev["nyxveilctl"] = tx.CtlPrev
	u.ExtraBinaries = extraDest
	u.ExtraPrev = extraPrev
	u.StateDir = stateDir
	u.EnforceOwnership = rollbackEnforceTLS

	if err := u.RollbackInstalled(); err != nil {
		fmt.Printf("rollback binary restore error: %v\n", err)
		return markRollbackFailed(tx, err)
	}
	if err := rollbackEnforceTLS(stateDir); err != nil {
		fmt.Printf("rollback TLS enforce error: %v\n", err)
		return markRollbackFailed(tx, fmt.Errorf("TLS ownership failure: %w", err))
	}

	skipSystemd := runtime.GOOS == "windows" || strings.TrimSpace(os.Getenv("NYXVEIL_CONTROL_HTTP")) != ""
	if !skipSystemd {
		if err := verifyUpdateTLS(); err != nil {
			return markRollbackFailed(tx, err)
		}
		if err := restartUnit("nyxveil-server"); err != nil {
			fmt.Printf("rollback restart failed: %v\n", err)
			return markRollbackFailed(tx, err)
		}
		rb, ok := rollbackVerifyHealth(tx.PreBaseline, 45)
		if !ok {
			fmt.Printf("rollback_complete=false baseline_restored=false reason=%s\n", rb.Reason)
			return markRollbackFailed(tx, fmt.Errorf("rollback health failed: %s", rb.Reason))
		}
		fmt.Printf("rollback_complete=%v baseline_restored=%v\n", rb.Complete, rb.BaselineRestored)
	} else {
		fmt.Printf("rollback_complete=true baseline_restored=true\n")
	}

	prev := strings.TrimSpace(tx.PreviousVersion)
	if prev == "" {
		fmt.Printf("rollback_complete=false reason=previous_version_unknown\n")
		return markRollbackFailed(tx, fmt.Errorf("previous version confirmation failed: previous version unknown"))
	}
	if err := rollbackConfirmPrevious(prev); err != nil {
		fmt.Printf("rollback_complete=false reason=previous_version_mismatch detail=%v\n", err)
		return markRollbackFailed(tx, fmt.Errorf("previous version confirmation failed: %w", err))
	}
	fmt.Printf("rollback_previous_version_confirmed=%s\n", prev)

	tx.Phase = txPhaseRolledBackHealthy
	if err := writeUpdateTransaction(tx); err != nil {
		return fmt.Errorf("update-resume failed; binaries rolled back but journal write failed: %w", err)
	}
	return fmt.Errorf("update-resume failed; rolled_back_healthy to previous release")
}

// Older ctl snapshots omit lifecycle fields. Recover only from the authoritative
// persisted CP configuration and matching status while the update owns the node.
func restoreLegacyLifecycle(tx *updateTransaction) error {
	if tx.PreBaseline.LifecycleKnown || tx.PreBaseline.Accepting {
		return nil
	}
	snap, err := localconfig.LoadApplied(filepath.Join(runtimeStateDir(), "applied-config.json"))
	if err != nil {
		return fmt.Errorf("legacy lifecycle unavailable: %w", err)
	}
	raw, err := ctlStatusJSON()
	if err != nil {
		return err
	}
	st, err := health.ParseStatusJSON(raw)
	if err != nil {
		return err
	}
	cfg := snap.Config
	if cfg.ConfigVersion <= 0 || cfg.NodeID == "" || cfg.NodeID != st.NodeID ||
		cfg.ConfigVersion != st.ConfigVersion || st.Accepting ||
		cfg.Draining != st.Draining || cfg.MaintenanceMode != st.MaintenanceMode ||
		(!cfg.Draining && !cfg.MaintenanceMode) {
		return fmt.Errorf("legacy lifecycle cannot prove intentional drain/maintenance")
	}
	tx.PreBaseline.LifecycleKnown = true
	tx.PreBaseline.Draining = cfg.Draining
	tx.PreBaseline.MaintenanceMode = cfg.MaintenanceMode
	tx.PreBaseline.NodeID = cfg.NodeID
	tx.PreBaseline.ConfigVersion = cfg.ConfigVersion
	return writeUpdateTransaction(tx)
}

func runtimeStateDir() string {
	if v := strings.TrimSpace(os.Getenv("NYXVEIL_STATE_DIR")); v != "" {
		return v
	}
	return paths.StateDir
}
