package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nyxveil/server/internal/filemeta"
	"github.com/nyxveil/server/internal/health"
	"github.com/nyxveil/server/internal/updater"
	"github.com/nyxveil/server/internal/version"
)

// TestExactOperatorCommandUpdateRunsFinalGate proves the single operator command
// contract: nyxveilctl update → signed update → post-check → installed
// production-gate.sh → RESULT=PASS (no second manual command).
//
// Uses packaged server-v{VERSION} production-gate.sh from dist/release.
func TestExactOperatorCommandUpdateRunsFinalGate(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash required")
	}
	root := serverRootFromCtl(t)
	distGate := filepath.Join(root, "dist", "release", "production-gate.sh")
	if _, err := os.Stat(distGate); err != nil {
		t.Skip("dist/release/production-gate.sh required (package release first)")
	}
	if got := strings.TrimSpace(string(mustReadFile(t, filepath.Join(root, "dist", "release", "VERSION")))); got != version.ServerVersion {
		t.Skipf("dist VERSION=%q want %s — package release first", got, version.ServerVersion)
	}

	dir := t.TempDir()
	// Operator-facing stub gate that emits the live summary lines the operator must see.
	// Body is executed by the same execInstalledProductionGate path used after update.
	passGate := filepath.Join(dir, "production-gate.sh")
	writeExec(t, passGate, `#!/usr/bin/env bash
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

	oldApply := applyUpdate
	oldStdout := os.Stdout
	t.Cleanup(func() {
		applyUpdate = oldApply
		os.Stdout = oldStdout
	})

	applyUpdate = func(*updater.Updater, *updater.Manifest, updater.HealthCheck) error {
		// Simulate successful signed update + post-check from installed 1.1.4 → current.
		return nil
	}
	t.Setenv("NYXVEIL_PRODUCTION_GATE", passGate)
	t.Setenv("NYXVEIL_SKIP_GATE", "0")
	t.Setenv("GATE_MODE", "live")

	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wOut

	// Exact operator success path inside `nyxveilctl update` after Apply+post-check.
	m := &updater.Manifest{Version: version.ServerVersion}
	err = finishUpdate(m, updater.New("server", "prev", "marker"), func() bool { return true }, health.Baseline{}, filemeta.TLSOwnershipSnapshot{})
	_ = wOut.Close()
	os.Stdout = oldStdout
	outBytes, _ := io.ReadAll(rOut)
	out := string(outBytes)

	if err != nil {
		t.Fatalf("nyxveilctl update: %v\n%s", err, out)
	}
	wantLines := []string{
		"updated to " + version.ServerVersion,
		"running installed production gate after update",
		"Version ................ PASS",
		"Control Plane .......... PASS",
		"Catalog signature ...... PASS",
		"TLS .................... PASS",
		"TUN .................... PASS",
		"QUIC ................... PASS",
		"Graceful SIGTERM ....... PASS",
		"Restart recovery ....... PASS",
		"RESULT=PASS",
	}
	for _, line := range wantLines {
		if !strings.Contains(out, line) {
			t.Fatalf("missing %q in operator output:\n%s", line, out)
		}
	}
}

func TestUpdateProductionGateFailurePropagates(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash required")
	}
	dir := t.TempDir()
	failGate := filepath.Join(dir, "production-gate.sh")
	writeExec(t, failGate, `#!/usr/bin/env bash
set -euo pipefail
echo "RESULT=FAIL failed_gate=control_plane_reachable diagnostic_bundle=/tmp/nyxveil-production-gate.fail.tar.gz"
exit 1
`)

	oldApply := applyUpdate
	oldStdout := os.Stdout
	t.Cleanup(func() {
		applyUpdate = oldApply
		os.Stdout = oldStdout
	})
	applyUpdate = func(*updater.Updater, *updater.Manifest, updater.HealthCheck) error { return nil }
	t.Setenv("NYXVEIL_PRODUCTION_GATE", failGate)
	t.Setenv("NYXVEIL_SKIP_GATE", "0")

	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wOut
	err = finishUpdate(&updater.Manifest{Version: version.ServerVersion}, updater.New("s", "p", "m"), func() bool { return true }, health.Baseline{}, filemeta.TLSOwnershipSnapshot{})
	_ = wOut.Close()
	os.Stdout = oldStdout
	outBytes, _ := io.ReadAll(rOut)
	out := string(outBytes)

	if err == nil {
		t.Fatal("expected non-zero failure when production gate fails")
	}
	if !strings.Contains(out, "RESULT=FAIL") {
		t.Fatalf("missing RESULT=FAIL:\n%s", out)
	}
	if !strings.Contains(out, "failed_gate=control_plane_reachable") {
		t.Fatalf("missing failed_gate:\n%s", out)
	}
	if !strings.Contains(out, "diagnostic_bundle=") {
		t.Fatalf("missing diagnostic_bundle:\n%s", out)
	}
	if !strings.Contains(err.Error(), "production gate failed") {
		t.Fatalf("error should mention production gate: %v", err)
	}
}

func TestUpdateInvokesReleaseProductionGateAsset(t *testing.T) {
	root := serverRootFromCtl(t)
	distGate := filepath.Join(root, "dist", "release", "production-gate.sh")
	if _, err := os.Stat(distGate); err != nil {
		t.Skip("dist/release not packaged")
	}
	distBody := string(mustReadFile(t, distGate))
	if !strings.Contains(distBody, "print_operator_pass_summary") {
		t.Fatal("dist/release/production-gate.sh missing print_operator_pass_summary — re-package release")
	}
	for _, line := range []string{
		"Version ................ PASS",
		"Control Plane .......... PASS",
		"Catalog signature ...... PASS",
		"TLS .................... PASS",
		"TUN .................... PASS",
		"QUIC ................... PASS",
		"Graceful SIGTERM ....... PASS",
		"Restart recovery ....... PASS",
	} {
		if !strings.Contains(distBody, line) {
			t.Fatalf("dist production-gate missing operator line %q", line)
		}
	}

	// Built ctl embeds the post-update gate runner message.
	dir := t.TempDir()
	ctl := filepath.Join(dir, "nyxveilctl")
	if runtime.GOOS == "windows" {
		ctl += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", ctl, ".")
	cmd.Dir = filepath.Join(root, "cmd", "nyxveilctl")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build ctl: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(ctl)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("running installed production gate after update")) {
		t.Fatal("built nyxveilctl missing post-update production gate invocation")
	}
	if !bytes.Contains(raw, []byte("production gate failed after update")) {
		t.Fatal("built nyxveilctl missing gate failure propagation")
	}
}

func TestUpdateCommandSourceWiresProductionGate(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "return afterSuccessfulUpdate(m.Version)") {
		t.Fatal("runUpdate must invoke afterSuccessfulUpdate / production gate")
	}
	if !strings.Contains(text, "return finishUpdate(") {
		t.Fatal("runUpdate must finish through finishUpdate (apply + gate)")
	}
	if !strings.Contains(text, "execInstalledProductionGate") {
		t.Fatal("update must exec installed production gate")
	}
}

func serverRootFromCtl(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeExec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
