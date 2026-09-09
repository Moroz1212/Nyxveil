package controlplane_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/nyxveil/server/internal/controlplane"
)

// TestHeartbeatDTOMatchesCP132Contract pins Server HeartbeatRequest JSON names to
// Control Plane 1.3.2 ApiContracts.NodeHeartbeatRequest without modifying CP source.
func TestHeartbeatDTOMatchesCP132Contract(t *testing.T) {
	cpFile := filepath.Join(repoRoot(t), "licensing", "src", "Nyxveil.ControlPlane.Application", "Contracts", "V1", "ApiContracts.cs")
	raw, err := os.ReadFile(cpFile)
	if err != nil {
		t.Skipf("CP contracts not in workspace: %v", err)
	}
	text := string(raw)

	// Extract JsonPropertyName values from NodeHeartbeatRequest region approximately.
	re := regexp.MustCompile(`\[JsonPropertyName\("([^"]+)"\)\]`)
	want := map[string]bool{}
	inHB := false
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "class NodeHeartbeatRequest") {
			inHB = true
			continue
		}
		if inHB && strings.HasPrefix(strings.TrimSpace(line), "public sealed class") && !strings.Contains(line, "NodeHeartbeatRequest") {
			break
		}
		if inHB && strings.HasPrefix(strings.TrimSpace(line), "}") && strings.Count(line, "{") == 0 {
			// fragile end; keep collecting until VersionResponse nearby
		}
		if inHB {
			if m := re.FindStringSubmatch(line); len(m) == 2 {
				want[m[1]] = true
			}
		}
		if inHB && strings.Contains(line, "class NodeHeartbeatResponse") {
			break
		}
	}
	if len(want) < 10 {
		t.Fatalf("failed to parse CP heartbeat JSON names (%d)", len(want))
	}

	must := []string{
		"management_capabilities",
		"supports_commands",
		"boot_id",
		"cert_thumbprint",
		"last_renewal_attempt",
		"last_renewal_success",
		"last_renewal_next",
		"last_renewal_error",
		"acme_auto_renew",
		"tun_ready",
		"tls_ok",
		"quic_ok",
		"bridge_ok",
		"ticket_keys_loaded",
		"cp_connected",
	}
	for _, k := range must {
		if !want[k] {
			t.Fatalf("CP contract missing expected key %q", k)
		}
	}

	trueVal := true
	acme := true
	tun := true
	tlsOK := true
	quicOK := true
	bridgeOK := true
	tickets := true
	cpOK := true
	payload, err := json.Marshal(controlplane.HeartbeatRequest{
		NodeID:                 "n1",
		Version:                "1.1.11",
		ProtocolVersion:        1,
		ManagementCapabilities: "certificate_renew,service_restart,host_reboot,node_update",
		SupportsCommands:       &trueVal,
		BootID:                 "boot",
		CertThumbprint:         "abc",
		ACMEAutoRenew:          &acme,
		LastRenewalAttempt:     "2026-01-01T00:00:00Z",
		LastSuccessfulRenewal:  "2026-01-02T00:00:00Z",
		NextPlannedRenewal:     "2026-02-01T00:00:00Z",
		LastRenewalError:       "none",
		TUNReady:               &tun,
		TLSOK:                  &tlsOK,
		QUICOK:                 &quicOK,
		BridgeOK:               &bridgeOK,
		TicketKeysLoaded:       &tickets,
		CPConnected:            &cpOK,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range must {
		if _, ok := got[k]; !ok {
			t.Fatalf("server heartbeat missing CP key %q in %s", k, payload)
		}
	}
	if _, ok := got["last_successful_renewal"]; ok {
		t.Fatal("server must not emit legacy last_successful_renewal")
	}
	if _, ok := got["next_planned_renewal"]; ok {
		t.Fatal("server must not emit legacy next_planned_renewal")
	}
}

func TestCommandResultDTOMatchesCP132(t *testing.T) {
	payload, err := json.Marshal(controlplane.NodeCommandResultRequest{
		Success:       false,
		ResultCode:    "rolled_back_healthy",
		ResultMessage: "restored",
		BootID:        "boot-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"success", "result_code", "result_message", "boot_id"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("missing %q in %s", k, payload)
		}
	}
	if got["success"] != false {
		t.Fatal("rolled_back_healthy fixture must encode success=false")
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	// server/internal/controlplane -> repo root
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
