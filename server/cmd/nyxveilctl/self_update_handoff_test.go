package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/filemeta"
	"github.com/nyxveil/server/internal/health"
	"github.com/nyxveil/server/internal/version"
)

// TestRealSelfUpdateOldProcessDoesNotFailCliVersion reproduces the live old→new
// ctl handoff: an OLD ctl process image must not treat its BuildVersion as installed CLI.
func TestRealSelfUpdateOldProcessDoesNotFailCliVersion(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash required")
	}
	root := serverRootFromCtl(t)
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	bin := filepath.Join(dir, "sbin")
	share := filepath.Join(dir, "share", "nyxveil")
	for _, d := range []string{state, bin, filepath.Join(share, "scripts")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	oldCtl := filepath.Join(bin, "nyxveilctl-old")
	newCtl := filepath.Join(bin, "nyxveilctl")
	serverBin := filepath.Join(bin, "nyxveil-server")
	if runtime.GOOS == "windows" {
		oldCtl += ".exe"
		newCtl += ".exe"
		serverBin += ".exe"
	}

	want := version.ServerVersion
	const oldProcess = "1.1.4"
	buildCtlWithVersion(t, root, oldCtl, oldProcess)
	buildCtlWithVersion(t, root, newCtl, want)
	buildServerWithVersion(t, root, serverBin, want)
	oldServer := filepath.Join(bin, "nyxveil-server-old")
	if runtime.GOOS == "windows" {
		oldServer += ".exe"
	}
	buildServerWithVersion(t, root, oldServer, oldProcess)

	if err := copyFileBytes(oldCtl, filepath.Join(state, "nyxveilctl.prev")); err != nil {
		t.Fatal(err)
	}
	if err := copyFileBytes(oldServer, filepath.Join(state, "nyxveil-server.prev")); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(share, "VERSION"), []byte(want+"\n"), 0o644)

	passGate := filepath.Join(share, "scripts", "production-gate.sh")
	writeExecFile(t, passGate, `#!/usr/bin/env bash
set -euo pipefail
echo "Version ................ PASS"
echo "Control Plane .......... PASS"
echo "Catalog signature ...... PASS"
echo "TLS .................... PASS"
echo "TUN .................... PASS"
echo "QUIC ................... PASS"
echo "Graceful SIGTERM ....... PASS"
echo "Restart recovery ....... PASS"
echo "RESULT=PASS"
`)

	t.Setenv("NYXVEIL_STATE_DIR", state)
	t.Setenv("NYXVEIL_CTL_BINARY", newCtl)
	t.Setenv("NYXVEIL_SERVER_BINARY", serverBin)
	t.Setenv("NYXVEIL_SHARE_DIR", share)
	t.Setenv("NYXVEIL_SHARE_VERSION", filepath.Join(share, "VERSION"))
	t.Setenv("NYXVEIL_PRODUCTION_GATE", passGate)
	t.Setenv("NYXVEIL_SKIP_GATE", "0")
	t.Setenv("GATE_MODE", "live")

	prevCLI, prevServer := version.CLIVersion, version.ServerVersion
	t.Cleanup(func() {
		version.CLIVersion = prevCLI
		version.ServerVersion = prevServer
	})
	version.CLIVersion = oldProcess
	version.ServerVersion = oldProcess

	if got := installedCLIVersion(); got != want {
		t.Fatalf("installed_cli_version=%q want %s", got, want)
	}

	srv := startStatusHTTP(t, want)
	t.Setenv("NYXVEIL_CONTROL_HTTP", srv)
	if err := assertVersionsMatchTarget(want); err != nil {
		t.Fatalf("old process must NOT fail on process cli_version: %v", err)
	}

	tx := &updateTransaction{
		ID:                "test-tx-1",
		TargetVersion:     want,
		PreviousVersion:   oldProcess,
		ServerPath:        serverBin,
		CtlPath:           newCtl,
		CtlPrev:           filepath.Join(state, "nyxveilctl.prev"),
		PreBaseline:       health.Baseline{DataplaneOK: true, CPConnected: true, Running: true, Accepting: true},
		PreTLS:            filemeta.TLSOwnershipSnapshot{},
		Phase:             txPhaseAssetsInstalled,
		ProcessCLIAtStart: oldProcess,
		CreatedAt:         time.Now().UTC(),
	}
	if err := writeUpdateTransaction(tx); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := runUpdateResume([]string{"--transaction", tx.ID})
	_ = w.Close()
	os.Stdout = oldStdout
	_, _ = buf.ReadFrom(r)
	out := buf.String()
	if err != nil {
		t.Fatalf("update-resume: %v\n%s", err, out)
	}
	if !strings.Contains(out, "updated to "+want) {
		t.Fatalf("missing updated to: %s", out)
	}
	if !strings.Contains(out, "RESULT=PASS") {
		t.Fatalf("gate must PASS: %s", out)
	}
}

func TestSelfUpdateExecsNewCtl(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(state, 0o700)
	spy := filepath.Join(dir, "spy-ctl")
	if runtime.GOOS == "windows" {
		spy += ".cmd"
		_ = os.WriteFile(spy, []byte("@echo spy-resume %*\r\n@exit /b 0\r\n"), 0o755)
	} else {
		writeExecFile(t, spy, "#!/bin/sh\necho spy-resume \"$@\"\nexit 0\n")
	}
	t.Setenv("NYXVEIL_STATE_DIR", state)
	tx := &updateTransaction{
		ID: "handoff-spy", TargetVersion: version.ServerVersion, CtlPath: spy,
		Phase: txPhaseAssetsInstalled, ProcessCLIAtStart: "1.1.4",
	}
	_ = writeUpdateTransaction(tx)
	code, err := spawnUpdateResume(spy, tx.ID)
	if err != nil || code != 0 {
		t.Fatalf("spawn resume: code=%d err=%v", code, err)
	}
}

func TestOldCliProcessVersionDoesNotRepresentInstalledCli(t *testing.T) {
	root := serverRootFromCtl(t)
	dir := t.TempDir()
	installed := filepath.Join(dir, "nyxveilctl")
	if runtime.GOOS == "windows" {
		installed += ".exe"
	}
	buildCtlWithVersion(t, root, installed, version.ServerVersion)
	t.Setenv("NYXVEIL_CTL_BINARY", installed)

	prev := version.CLIVersion
	version.CLIVersion = "1.1.4"
	t.Cleanup(func() { version.CLIVersion = prev })

	if got := installedCLIVersion(); got != version.ServerVersion {
		t.Fatalf("installed_cli_version=%q want %s", got, version.ServerVersion)
	}
}

func TestSelfUpdateResumeUsesTargetVersion(t *testing.T) {
	if err := assertVersionsMatchTarget(""); err == nil {
		t.Fatal("empty target must fail")
	}
}

func TestInstalledCliVersionFromActualBinary(t *testing.T) {
	TestOldCliProcessVersionDoesNotRepresentInstalledCli(t)
}

func TestRunningServerVersionFromControlSocket(t *testing.T) {
	srv := startStatusHTTP(t, version.ServerVersion)
	t.Setenv("NYXVEIL_CONTROL_HTTP", srv)
	if got := runningServerVersion(); got != version.ServerVersion {
		t.Fatalf("running=%q", got)
	}
}

func TestGateRunsOnlyAfterNewCtlHandoff(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(serverRootFromCtl(t), "cmd", "nyxveilctl", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "handoffPostCheckToNewCtl") {
		t.Fatal("update health must hand off to new ctl")
	}
	if !strings.Contains(text, "update-resume") {
		t.Fatal("update-resume command required")
	}
}

func TestSelfUpdateLockPreventsConcurrentResume(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NYXVEIL_STATE_DIR", dir)
	f1, err := acquireUpdateLock()
	if err != nil {
		t.Fatal(err)
	}
	_ = f1.Close()
	f2, err := acquireUpdateLock()
	if err != nil {
		t.Fatal(err)
	}
	_ = f2.Close()
}

func TestSelfUpdateRollbackAcrossHandoff(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(serverRootFromCtl(t), "cmd", "nyxveilctl", "update_handoff.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "rollbackAcrossHandoff") {
		t.Fatal("missing rollbackAcrossHandoff")
	}
	if !strings.Contains(text, "RollbackInstalled") {
		t.Fatal("handoff rollback must restore installed assets")
	}
	if !strings.Contains(text, "txPhaseRolledBackHealthy") {
		t.Fatal("success rollback must use rolled_back_healthy")
	}
	if !strings.Contains(text, "txPhaseRollbackFailed") {
		t.Fatal("failed rollback must use rollback_failed")
	}
}

func TestTxPhaseConstants(t *testing.T) {
	if txPhaseRollingBack != "rolling_back" {
		t.Fatalf("rolling_back=%q", txPhaseRollingBack)
	}
	if txPhaseRolledBackHealthy != "rolled_back_healthy" {
		t.Fatalf("rolled_back_healthy=%q", txPhaseRolledBackHealthy)
	}
	if txPhaseRollbackFailed != "rollback_failed" {
		t.Fatalf("rollback_failed=%q", txPhaseRollbackFailed)
	}
	if txPhaseRolledBack != "rolled_back" {
		t.Fatalf("legacy rolled_back alias=%q", txPhaseRolledBack)
	}
}

func TestWriteUpdateTransactionChecksErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NYXVEIL_STATE_DIR", dir)
	// Make transaction directory a file so MkdirAll/write fails.
	txnFile := filepath.Join(dir, "update-transactions")
	if err := os.WriteFile(txnFile, []byte("not-a-dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	tx := &updateTransaction{ID: "bad-write", Phase: txPhaseRollingBack, CreatedAt: time.Now().UTC()}
	if err := writeUpdateTransaction(tx); err == nil {
		t.Fatal("expected writeUpdateTransaction error when txn dir is a file")
	}
}

func TestRollbackAcrossHandoffHealthyPath(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	bin := filepath.Join(dir, "bin")
	_ = os.MkdirAll(state, 0o700)
	_ = os.MkdirAll(bin, 0o755)
	t.Setenv("NYXVEIL_STATE_DIR", state)
	t.Setenv("NYXVEIL_CONTROL_HTTP", "http://127.0.0.1:9") // skip systemd health path

	server := filepath.Join(bin, "nyxveil-server")
	ctl := filepath.Join(bin, "nyxveilctl")
	_ = os.WriteFile(server, []byte("NEW"), 0o755)
	_ = os.WriteFile(ctl, []byte("NEWCTL"), 0o755)
	_ = os.WriteFile(filepath.Join(state, "nyxveil-server.prev"), []byte("OLD"), 0o755)
	_ = os.WriteFile(filepath.Join(state, "nyxveilctl.prev"), []byte("OLDCTL"), 0o755)

	tx := &updateTransaction{
		ID:              "rb-healthy",
		TargetVersion:   "9.9.9",
		PreviousVersion: "1.1.10",
		ServerPath:      server,
		CtlPath:         ctl,
		CtlPrev:         filepath.Join(state, "nyxveilctl.prev"),
		PreBaseline:     health.Baseline{DataplaneOK: true, Running: true, Accepting: true},
		Phase:           txPhaseResuming,
		CreatedAt:       time.Now().UTC(),
	}
	oldTLS := rollbackEnforceTLS
	rollbackEnforceTLS = func(string) error { return nil }
	t.Cleanup(func() { rollbackEnforceTLS = oldTLS })
	oldConfirm := rollbackConfirmPrevious
	rollbackConfirmPrevious = func(string) error { return nil }
	t.Cleanup(func() { rollbackConfirmPrevious = oldConfirm })

	err := rollbackAcrossHandoff(tx)
	if err == nil {
		t.Fatal("expected error return after healthy rollback")
	}
	if !strings.Contains(err.Error(), "rolled_back_healthy") {
		t.Fatalf("error should mention rolled_back_healthy: %v", err)
	}
	loaded, loadErr := loadUpdateTransaction(tx.ID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if loaded.Phase != txPhaseRolledBackHealthy {
		t.Fatalf("phase=%q want %q", loaded.Phase, txPhaseRolledBackHealthy)
	}
	got, _ := os.ReadFile(server)
	if string(got) != "OLD" {
		t.Fatalf("server not rolled back: %q", got)
	}
	// Windows may lock recently rewritten binaries during TempDir cleanup.
	_ = os.RemoveAll(bin)
	_ = os.RemoveAll(state)
}

func TestRollbackAcrossHandoffBinaryFailure(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	bin := filepath.Join(dir, "bin")
	_ = os.MkdirAll(state, 0o700)
	_ = os.MkdirAll(bin, 0o755)
	t.Setenv("NYXVEIL_STATE_DIR", state)
	t.Setenv("NYXVEIL_CONTROL_HTTP", "http://127.0.0.1:9")

	server := filepath.Join(bin, "nyxveil-server")
	ctl := filepath.Join(bin, "nyxveilctl")
	_ = os.WriteFile(server, []byte("NEW"), 0o755)
	_ = os.WriteFile(ctl, []byte("NEWCTL"), 0o755)
	// Intentionally omit *.prev so RollbackInstalled fails.

	tx := &updateTransaction{
		ID:              "rb-fail",
		TargetVersion:   "9.9.9",
		PreviousVersion: "1.1.10",
		ServerPath:      server,
		CtlPath:         ctl,
		CtlPrev:         filepath.Join(state, "nyxveilctl.prev"),
		PreBaseline:     health.Baseline{Running: true},
		Phase:           txPhaseResuming,
		CreatedAt:       time.Now().UTC(),
	}
	err := rollbackAcrossHandoff(tx)
	if err == nil {
		t.Fatal("expected rollback_failed error")
	}
	if !strings.Contains(err.Error(), "rollback_failed") {
		t.Fatalf("error should mention rollback_failed: %v", err)
	}
	loaded, loadErr := loadUpdateTransaction(tx.ID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if loaded.Phase != txPhaseRollbackFailed {
		t.Fatalf("phase=%q want %q", loaded.Phase, txPhaseRollbackFailed)
	}
	_ = os.RemoveAll(bin)
	_ = os.RemoveAll(state)
}

func TestHandoffPostCheckTreatsPhases(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(serverRootFromCtl(t), "cmd", "nyxveilctl", "update_handoff.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "txPhaseRolledBackHealthy, txPhaseRolledBack, txPhaseRollingBack") {
		t.Fatal("handoff must treat rolled_back_healthy as child-owned rollback")
	}
	if !strings.Contains(text, "txPhaseRollbackFailed") {
		t.Fatal("handoff must special-case rollback_failed")
	}
}

func buildCtlWithVersion(t *testing.T, root, out, ver string) {
	t.Helper()
	ld := fmt.Sprintf("-X github.com/nyxveil/server/internal/version.ServerVersion=%s -X github.com/nyxveil/server/internal/version.CLIVersion=%s", ver, ver)
	cmd := exec.Command("go", "build", "-ldflags", ld, "-o", out, ".")
	cmd.Dir = filepath.Join(root, "cmd", "nyxveilctl")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build ctl %s: %v\n%s", ver, err, b)
	}
}

func buildServerWithVersion(t *testing.T, root, out, ver string) {
	t.Helper()
	ld := fmt.Sprintf("-X github.com/nyxveil/server/internal/version.ServerVersion=%s", ver)
	cmd := exec.Command("go", "build", "-ldflags", ld, "-o", out, ".")
	cmd.Dir = filepath.Join(root, "cmd", "nyxveil-server")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build server %s: %v\n%s", ver, err, b)
	}
}

func writeExecFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func copyFileBytes(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o755)
}

func startStatusHTTP(t *testing.T, ver string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"running":true,"server_version":"` + ver + `","cp_connected":true,"healthy":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}
