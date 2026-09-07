package ipc_test

import (
	"strings"
	"testing"
	"time"

	"github.com/nyxveil/client-windows/internal/diag"
	"github.com/nyxveil/client-windows/internal/ipc"
)

func TestLogsSnapshotFromRingNoSecrets(t *testing.T) {
	r := diag.NewRing(32)
	r.Append(diag.Event{
		Time: time.Now(), Level: diag.LevelInfo, Component: "AUTH", Event: "token",
		Message: "Authorization: Bearer supersecrettokenvalue",
	})
	snap := ipc.LogsSnapshotFromRing("id1", r.Snapshot())
	if snap.Type != ipc.TypeLogsSnapshot {
		t.Fatal(snap.Type)
	}
	if len(snap.Entries) != 1 {
		t.Fatal(len(snap.Entries))
	}
	if strings.Contains(snap.Entries[0].Line, "supersecrettokenvalue") {
		t.Fatalf("secret leaked: %s", snap.Entries[0].Line)
	}
	if !strings.Contains(snap.Entries[0].Line, "REDACTED") {
		t.Fatalf("expected redaction: %s", snap.Entries[0].Line)
	}
}
