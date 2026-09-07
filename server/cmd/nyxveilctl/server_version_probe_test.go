package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nyxveil/server/internal/version"
)

// TestServerVersionSubcommandDoesNotStartDaemon reproduces the live
// failed_gate=server_version root cause: gate invoked `nyxveil-server version`
// which previously fell through into daemon start.
func TestServerVersionSubcommandDoesNotStartDaemon(t *testing.T) {
	bin := buildTestServer(t)
	out, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("version subcommand failed: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "nyxveil-server "+version.ServerVersion) {
		t.Fatalf("unexpected version output: %s", got)
	}
	if strings.Contains(strings.ToLower(got), "running") || strings.Contains(got, "listening") {
		t.Fatalf("version subcommand appears to start daemon: %s", got)
	}

	outJSON, err := exec.Command(bin, "version", "--json").CombinedOutput()
	if err != nil {
		t.Fatalf("version --json failed: %v\n%s", err, outJSON)
	}
	if !strings.Contains(string(outJSON), `"server_version":"`+version.ServerVersion+`"`) {
		t.Fatalf("json version: %s", outJSON)
	}

	outFlag, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("--version failed: %v\n%s", err, outFlag)
	}
	if !strings.Contains(string(outFlag), version.ServerVersion) {
		t.Fatalf("--version: %s", outFlag)
	}
}

func TestInstalledServerVersionProbesRealBinary(t *testing.T) {
	bin := buildTestServer(t)
	t.Setenv("NYXVEIL_SERVER_BINARY", bin)
	t.Setenv("NYXVEIL_CONTROL_HTTP", "http://127.0.0.1:1")
	got := installedServerVersion()
	if got != version.ServerVersion {
		t.Fatalf("installed=%q want %s", got, version.ServerVersion)
	}
}

func buildTestServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "nyxveil-server")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = filepath.Join(repoRootFromCtl(t), "cmd", "nyxveil-server")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build server: %v\n%s", err, b)
	}
	return out
}

func repoRootFromCtl(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
