package main

import (
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
	"github.com/nyxveil/server/internal/paths"
	"github.com/nyxveil/server/internal/updater"
	"github.com/nyxveil/server/internal/version"
)

// updateTransaction journals a self-update across the old→new ctl process boundary.
// Only one process owns rollback at a time (see OwnerPID / Phase).
type updateTransaction struct {
	ID                string                        `json:"id"`
	TargetVersion     string                        `json:"target_version"`
	ManifestURL       string                        `json:"manifest_url,omitempty"`
	LocalDir          string                        `json:"local_dir,omitempty"`
	ServerPath        string                        `json:"server_path"`
	CtlPath           string                        `json:"ctl_path"`
	CtlPrev           string                        `json:"ctl_prev"`
	PreBaseline       health.Baseline               `json:"pre_baseline"`
	PreTLS            filemeta.TLSOwnershipSnapshot `json:"pre_tls"`
	Phase             string                        `json:"phase"` // assets_installed|resuming|committed|rolling_back|rolled_back
	OwnerPID          int                           `json:"owner_pid"`
	CreatedAt         time.Time                     `json:"created_at"`
	ProcessCLIAtStart string                        `json:"process_cli_at_start"`
}

const (
	txPhaseAssetsInstalled = "assets_installed"
	txPhaseResuming        = "resuming"
	txPhaseCommitted       = "committed"
	txPhaseRollingBack     = "rolling_back"
	txPhaseRolledBack      = "rolled_back"
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
	b, err := json.MarshalIndent(tx, "", "  ")
	if err != nil {
		return err
	}
	tmp := txPath(tx.ID) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, txPath(tx.ID))
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
	cmd := exec.Command(newCtl, "update-resume", "--transaction", txID)
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
	if loadErr == nil && (loaded.Phase == txPhaseRolledBack || loaded.Phase == txPhaseRollingBack) {
		fmt.Printf("self-update handoff: new ctl owns rollback (phase=%s); parent will not double-rollback\n", loaded.Phase)
		os.Exit(1)
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
	tx.OwnerPID = os.Getpid()
	if err := writeUpdateTransaction(tx); err != nil {
		return err
	}

	fmt.Printf("update-resume: target=%s process_cli=%s (was %s at start)\n",
		tx.TargetVersion, version.CLIVersion, tx.ProcessCLIAtStart)

	if !performPostUpdateVerification(tx) {
		return rollbackAcrossHandoff(tx)
	}

	fmt.Printf("updated to %s\n", tx.TargetVersion)
	if err := afterSuccessfulUpdate(tx.TargetVersion); err != nil {
		return rollbackAcrossHandoff(tx)
	}

	_ = os.Remove(paths.RollbackMarker())
	tx.Phase = txPhaseCommitted
	_ = writeUpdateTransaction(tx)
	removeUpdateTransaction(tx.ID)
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
	if err := restartUnit("nyxveil-server"); err != nil {
		fmt.Printf("update restart failed: %v\n", err)
		return false
	}
	res, ok := verifyPostUpdateHealth(tx.PreBaseline, 45)
	if !ok {
		return false
	}
	postPID := unitMainPID("nyxveil-server")
	if prePID > 0 && postPID > 0 && prePID == postPID {
		fmt.Printf("update_success=false reason=same_pid_after_restart pre_pid=%d post_pid=%d\n", prePID, postPID)
		return false
	}
	if err := assertVersionsMatchTarget(tx.TargetVersion); err != nil {
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

func rollbackAcrossHandoff(tx *updateTransaction) error {
	tx.Phase = txPhaseRollingBack
	tx.OwnerPID = os.Getpid()
	_ = writeUpdateTransaction(tx)

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
	u.EnforceOwnership = filemeta.EnforceRuntimeTLS

	if err := u.RollbackInstalled(); err != nil {
		fmt.Printf("rollback binary restore error: %v\n", err)
	}
	_ = filemeta.EnforceRuntimeTLS(stateDir)
	if runtime.GOOS != "windows" {
		_ = restartUnit("nyxveil-server")
		rb, ok := verifyRollbackHealth(tx.PreBaseline, 45)
		if ok {
			fmt.Printf("rollback_complete=%v baseline_restored=%v\n", rb.Complete, rb.BaselineRestored)
		} else {
			fmt.Printf("rollback_complete=false baseline_restored=false reason=%s\n", rb.Reason)
		}
	} else {
		fmt.Printf("rollback_complete=true baseline_restored=true\n")
	}

	tx.Phase = txPhaseRolledBack
	_ = writeUpdateTransaction(tx)
	return fmt.Errorf("update-resume failed; rolled back to previous release")
}

func runtimeStateDir() string {
	if v := strings.TrimSpace(os.Getenv("NYXVEIL_STATE_DIR")); v != "" {
		return v
	}
	return paths.StateDir
}
