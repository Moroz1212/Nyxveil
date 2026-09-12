package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCaptureCommandCorrelationBeforeLegacyMarkerDeletion(t *testing.T) {
	state := t.TempDir()
	t.Setenv("NYXVEIL_STATE_DIR", state)
	mgmt := filepath.Join(state, "management")
	if err := os.MkdirAll(mgmt, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := map[string]any{
		"command_id":       "ac14e15b-6cc9-4d45-9ff7-6a05c992af5d",
		"previous_version": "1.1.9",
		"target_version":   "1.1.14",
		"started_at":       time.Now().UTC().Format(time.RFC3339),
		"phase":            "downloading",
	}
	raw, _ := json.MarshalIndent(marker, "", "  ")
	if err := os.WriteFile(filepath.Join(mgmt, "update-command.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	// Simulate a legacy 1.1.9-created journal that has no command_id yet.
	tx := &updateTransaction{
		ID:                "1789143920896154056-64966",
		TargetVersion:     "1.1.14",
		PreviousVersion:   "1.1.9",
		ProcessCLIAtStart: "1.1.9",
		Phase:             txPhaseAssetsInstalled,
		OwnerPID:          os.Getpid(),
		CreatedAt:         time.Now().UTC(),
		LegacyParent:      true,
	}
	if err := writeUpdateTransaction(tx); err != nil {
		t.Fatal(err)
	}
	if err := captureCommandCorrelationFromMarker(tx); err != nil {
		t.Fatal(err)
	}
	if tx.CommandID != "ac14e15b-6cc9-4d45-9ff7-6a05c992af5d" {
		t.Fatalf("command_id not captured: %q", tx.CommandID)
	}
	loaded, err := loadUpdateTransaction(tx.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CommandID != tx.CommandID {
		t.Fatalf("durable journal missing command_id: %+v", loaded)
	}

	// Legacy parent deletes the marker during shutdown/fallback.
	if err := os.Remove(filepath.Join(mgmt, "update-command.json")); err != nil {
		t.Fatal(err)
	}
	// Correlation must still be present in the transaction after marker loss.
	reloaded, err := loadUpdateTransaction(tx.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.CommandID == "" {
		t.Fatal("command correlation lost after marker deletion")
	}

	// Target mismatch must fail closed.
	tx2 := &updateTransaction{
		ID:            "tx-bad-target",
		TargetVersion: "9.9.9",
		Phase:         txPhaseAssetsInstalled,
		CreatedAt:     time.Now().UTC(),
	}
	_ = os.WriteFile(filepath.Join(mgmt, "update-command.json"), raw, 0o600)
	if err := captureCommandCorrelationFromMarker(tx2); err == nil {
		t.Fatal("expected target mismatch failure")
	}
}

func TestCaptureCommandCorrelationAbsentMarkerOK(t *testing.T) {
	t.Setenv("NYXVEIL_STATE_DIR", t.TempDir())
	tx := &updateTransaction{
		ID:            "manual",
		TargetVersion: "1.1.14",
		Phase:         txPhaseAssetsInstalled,
		CreatedAt:     time.Now().UTC(),
	}
	if err := captureCommandCorrelationFromMarker(tx); err != nil {
		t.Fatal(err)
	}
	if tx.CommandID != "" {
		t.Fatal("manual update must not invent command_id")
	}
}
