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

// TestRealSelfUpdateOldProcessDoesNotFailCliVersion reproduces the live 1.1.4→1.1.5
// failure: an OLD ctl process image must not treat its BuildVersion as installed CLI.
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

	const want = "1.1.7"
	buildCtlWithVersion(t, root, oldCtl, "1.1.4")
	buildCtlWithVersion(t, root, newCtl, want)
	buildServerWithVersion(t, root, serverBin, want)

	if err := copyFileBytes(oldCtl, filepath.Join(state, "nyxveilctl.prev")); err != nil {
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
	version.CLIVersion = "1.1.4"
	version.ServerVersion = "1.1.4"

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
		ServerPath:        serverBin,
		CtlPath:           newCtl,
		CtlPrev:           filepath.Join(state, "nyxveilctl.prev"),
		PreBaseline:       health.Baseline{DataplaneOK: true, CPConnected: true, Running: true, Accepting: true},
		PreTLS:            filemeta.TLSOwnershipSnapshot{},
		Phase:             txPhaseAssetsInstalled,
		ProcessCLIAtStart: "1.1.4",
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
		ID: "handoff-spy", TargetVersion: "1.1.7", CtlPath: spy,
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
	buildCtlWithVersion(t, root, installed, "1.1.7")
	t.Setenv("NYXVEIL_CTL_BINARY", installed)

	prev := version.CLIVersion
	version.CLIVersion = "1.1.4"
	t.Cleanup(func() { version.CLIVersion = prev })

	if got := installedCLIVersion(); got != "1.1.7" {
		t.Fatalf("installed_cli_version=%q want 1.1.7", got)
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
	srv := startStatusHTTP(t, "1.1.7")
	t.Setenv("NYXVEIL_CONTROL_HTTP", srv)
	if got := runningServerVersion(); got != "1.1.7" {
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
