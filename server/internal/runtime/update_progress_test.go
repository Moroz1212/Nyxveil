package runtime

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/controlplane"
)

func TestCompletePendingUpdateReportsProgressOnPhaseChange(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var gotPath string
	var got controlplane.NodeCommandProgressRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodPost || r.Header.Get("X-Node-Signature") == "" {
			t.Errorf("unexpected progress request method=%s signed=%v", r.Method, r.Header.Get("X-Node-Signature") != "")
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("decode progress body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cp, err := controlplane.NewClient(srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	cp.NodeID = "node-1"
	cp.PrivateKey = privateKey

	dir := t.TempDir()
	n := &Node{cp: cp}
	n.opts.KeyPath = filepath.Join(dir, "node.key")
	started := time.Now().UTC().Add(-time.Minute)
	marker := updateMarker{
		CommandID:       "command-1",
		PreviousVersion: "1.1.16",
		TargetVersion:   "1.1.17",
		Phase:           updatePhaseDownloading,
		StartedAt:       started.Format(time.RFC3339),
	}
	tx := ctlTxnWire{
		ID:               "transaction-1",
		CommandID:        marker.CommandID,
		TargetVersion:    marker.TargetVersion,
		PreviousVersion:  marker.PreviousVersion,
		Phase:            "resuming",
		CreatedAt:        started.Add(time.Second),
		CommandStartedAt: marker.StartedAt,
	}
	txDir := n.updateTransactionDir()
	if err := os.MkdirAll(txDir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(txDir, tx.ID+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	n.completePendingUpdateFromMarker(context.Background(), marker)

	if gotPath != "/api/v1/node/commands/command-1/progress" {
		t.Fatalf("progress path=%q", gotPath)
	}
	if got.Phase != updatePhaseRestarting || !strings.Contains(got.Message, "Restarting") {
		t.Fatalf("progress=%+v", got)
	}
	updated, ok := n.readUpdateMarker()
	if !ok || updated.Phase != updatePhaseRestarting {
		t.Fatalf("updated marker=%+v ok=%v", updated, ok)
	}
}
