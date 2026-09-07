package productiongate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionGateFullAuditContract(t *testing.T) {
	root := findServerRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "scripts", "production-gate.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)

	mustContain := []string{
		`nyxveilctl version --json`,
		`"${SERVER}" --version`,
		`wait_cp_authenticated`,
		`json_bool "${WORK}/status.json" cp_connected`,
		`json_bool "${WORK}/status.json" healthy`,
		`json_bool "${WORK}/status.json" tun_ready`,
		`json_bool "${WORK}/status.json" bridge_ok`,
		`json_bool "${WORK}/status.json" tls_ok`,
		`json_bool "${WORK}/status.json" quic_ok`,
		`fail "identity"`,
		`fail "tls_files"`,
		`catalog_signature`,
		`TimeoutStopUSec`,
		`GATE_CP_WAIT_SEC`,
		`GATE_STOP_AFTER_CP`,
		`capture_cp_diagnostics`,
		`capture_version_diagnostics`,
		`diagnostic only`,
		`authenticated Control Plane not ready`,
	}
	for _, n := range mustContain {
		if !strings.Contains(text, n) {
			t.Fatalf("gate audit missing %q", n)
		}
	}

	forbidden := []string{
		`fail "control_plane_reachable" "live mode Control Plane probe failed"`,
		`nyxveil-server version`,
		`grep -E.*server_version`,
	}
	for _, n := range forbidden {
		if strings.Contains(text, n) {
			// Allow --version probe line and comments.
			if n == `nyxveil-server version` && strings.Contains(text, `"${SERVER}" --version`) {
				// ensure we don't invoke positional version as daemon start
				if strings.Contains(text, `${SERVER}" version`) || strings.Contains(text, `${SERVER} version`) {
					t.Fatal("gate must not invoke positional server version")
				}
				continue
			}
			if n == `nyxveil-server version` {
				continue
			}
			t.Fatalf("gate audit found forbidden pattern %q", n)
		}
	}

	// Live health must not fail-closed before CP wait.
	if strings.Contains(text, `fail "health" "live mode requires healthy node"`) {
		t.Fatal("live mode must not hard-fail health before authenticated CP wait")
	}
}
