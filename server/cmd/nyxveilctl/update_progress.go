package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nyxveil/server/internal/controlplane"
	"github.com/nyxveil/server/internal/filemeta"
	"github.com/nyxveil/server/internal/identity"
	"github.com/nyxveil/server/internal/localconfig"
	"github.com/nyxveil/server/internal/paths"
)

func setUpdateProgress(tx *updateTransaction, transactionPhase, progressPhase, message string) {
	previousPhase := tx.Phase
	tx.Phase = transactionPhase
	if err := writeUpdateTransaction(tx); err != nil {
		tx.Phase = previousPhase
		log.Printf("update progress journal phase=%s: %v", progressPhase, err)
	}
	reportUpdateCommandProgress(tx.CommandID, progressPhase, message)
}

// reportUpdateCommandProgress is best-effort. The update marker is advanced
// durably first so the daemon can continue refreshing the lease after restart.
func reportUpdateCommandProgress(commandID, phase, message string) {
	m, ok := readUpdateCommandMarker()
	if !ok {
		return // Manual updates have no Control Plane command to refresh.
	}
	if commandID = strings.TrimSpace(commandID); commandID == "" {
		commandID = strings.TrimSpace(m.CommandID)
	}
	if commandID == "" || commandID != strings.TrimSpace(m.CommandID) {
		log.Printf("update progress phase=%s skipped: command correlation mismatch", phase)
		return
	}
	m.Phase = phase
	m.LastUpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeUpdateCommandMarker(m); err != nil {
		log.Printf("update progress marker phase=%s: %v", phase, err)
	}

	cfg, err := localconfig.Load(paths.ServerConfig())
	if err != nil {
		log.Printf("update progress phase=%s load config: %v", phase, err)
		return
	}
	keyBytes, err := os.ReadFile(filepath.Join(runtimeStateDir(), "node.key"))
	if err != nil {
		log.Printf("update progress phase=%s load node key: %v", phase, err)
		return
	}
	key, err := identity.ParsePEM(keyBytes)
	if err != nil {
		log.Printf("update progress phase=%s parse node key: %v", phase, err)
		return
	}
	client, _, err := controlplane.NewClientWithTLS(controlplane.TLSOptions{
		BaseURL:      cfg.ControlPlaneURL,
		SPKIPinHex:   cfg.ControlPlaneSPKIPin,
		PinnedCAFile: cfg.PinnedCAFile,
	})
	if err != nil {
		log.Printf("update progress phase=%s create client: %v", phase, err)
		return
	}
	client.NodeID = cfg.NodeID
	client.PrivateKey = key.Private
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.ReportCommandProgress(ctx, commandID, controlplane.NodeCommandProgressRequest{
		Phase: phase, Message: message,
	}); err != nil {
		log.Printf("update progress command=%s phase=%s: %v", commandID, phase, err)
	}
}

func writeUpdateCommandMarker(m updateCommandMarker) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	path := updateCommandMarkerPath()
	if err := filemeta.DurableWrite(path, raw, 0o600); err != nil {
		return err
	}
	uid, gid, _ := filemeta.LookupServiceIDs()
	_ = filemeta.ApplyOwnerMode(path, uid, gid, 0o600)
	return nil
}
