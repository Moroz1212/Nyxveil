package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/version"
)

// Process-level: real compiled daemon answers version without binding listeners.
func TestRealDaemonVersionProbeExitsImmediately(t *testing.T) {
	bin := buildServer(t)
	start := time.Now()
	cmd := exec.Command(bin, "version")
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("version: %v\n%s", err, out)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("version took too long (%v) — may have started daemon", elapsed)
	}
	if !strings.Contains(string(out), version.ServerVersion) {
		t.Fatalf("output=%s", out)
	}

	// Positional version must not fall through to Start().
	cmd2 := exec.Command(bin, "version", "--json")
	out2, err := cmd2.CombinedOutput()
	if err != nil {
		t.Fatalf("version --json: %v\n%s", err, out2)
	}
	if !strings.Contains(string(out2), `"server_version":"`+version.ServerVersion+`"`) {
		t.Fatalf("json=%s", out2)
	}
}

func TestRealDaemonFlagVersion(t *testing.T) {
	bin := buildServer(t)
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("--version: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "nyxveil-server "+version.ServerVersion) {
		t.Fatalf("got %s", out)
	}
}

func buildServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "nyxveil-server")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	cmdDir := filepath.Dir(thisFile)
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = cmdDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, b)
	}
	return out
}
