package productiongate_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProductionGateDoesNotFailOnCurlWhenRuntimeConnected(t *testing.T) {
	root := findServerRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "scripts", "production-gate.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "wait_cp_authenticated") {
		t.Fatal("gate must poll authenticated runtime CP state")
	}
	if !strings.Contains(text, "diagnostic only") {
		t.Fatal("curl probe must be marked diagnostic-only")
	}
	// Authoritative fail must mention runtime cp_connected, not curl.
	if !strings.Contains(text, "authenticated Control Plane not ready") {
		t.Fatal("authoritative failure message missing")
	}
	// Old brittle pattern: fail live solely on curl /health.
	if strings.Contains(text, `fail "control_plane_reachable" "live mode Control Plane probe failed"`) {
		t.Fatal("gate still fails live mode on curl /health probe")
	}
}

func TestProductionGateHasCPDiagnosticsBundle(t *testing.T) {
	root := findServerRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "scripts", "production-gate.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, needle := range []string{
		"capture_cp_diagnostics",
		"cp-diagnostics.txt",
		"last_cp_success",
		"cp_last_error",
		"GATE_CP_WAIT_SEC",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("missing %q", needle)
		}
	}
}

func findServerRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
