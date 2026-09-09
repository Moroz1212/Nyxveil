package runtime

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/nyxveil/server/internal/controlplane"
	"github.com/nyxveil/server/internal/paths"
)

const (
	commandPollInterval          = 7 * time.Second
	managementCapabilitiesList   = "certificate_renew,service_restart,host_reboot,node_update"
	explicitRenewRateLimitWindow = time.Hour
)

var (
	systemdRestartService = defaultSystemdRestart
	hostReboot            = defaultHostReboot
)

func defaultSystemdRestart(unit string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("service restart unsupported on %s", runtime.GOOS)
	}
	cmd := exec.Command("systemctl", "restart", unit)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func defaultHostReboot() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("host reboot unsupported on %s", runtime.GOOS)
	}
	cmd := exec.Command("systemctl", "reboot")
	if err := cmd.Run(); err == nil {
		return nil
	}
	cmd = exec.Command("/sbin/reboot")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (n *Node) ensureCommandStore() {
	if n.commandStore != nil {
		return
	}
	statePath := paths.CommandsState()
	if n.opts.KeyPath != "" {
		statePath = filepath.Join(filepath.Dir(n.opts.KeyPath), "commands-state.json")
	}
	n.commandStore = newCommandDedupeStore(statePath)
}

func (n *Node) commandPollLoop(ctx context.Context) {
	defer n.wg.Done()
	n.ensureCommandStore()
	n.flushPendingCommandResults(ctx)
	n.reportPendingRebootResults(ctx)
	n.reportPendingRestartResults(ctx)
	n.completePendingUpdate(ctx)

	ticker := time.NewTicker(commandPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.flushPendingCommandResults(ctx)
			n.reportPendingRestartResults(ctx)
			if !n.cpOK.Load() {
				n.completePendingUpdate(ctx)
				continue
			}
			n.completePendingUpdate(ctx)
			n.pollAndExecuteCommand(ctx)
		}
	}
}

func (n *Node) reportPendingRebootResults(ctx context.Context) {
	if n.commandStore == nil {
		return
	}
	currentBoot := readBootID()
	for _, pending := range n.commandStore.rebootPending() {
		if pending.PreBootID == "" || pending.PreBootID == currentBoot {
			continue
		}
		st := n.Status()
		st.Healthy = st.ComputeHealthy()
		if !st.Healthy {
			n.persistOrReportResult(ctx, pendingResultRecord{
				CommandID:     pending.CommandID,
				Success:       false,
				ResultCode:    "reboot_unhealthy",
				ResultMessage: "Host returned after reboot but node is not healthy",
				BootID:        currentBoot,
			})
			n.commandStore.removeRebootPending(pending.CommandID)
			continue
		}
		n.persistOrReportResult(ctx, pendingResultRecord{
			CommandID:     pending.CommandID,
			Success:       true,
			ResultCode:    "rebooted_healthy",
			ResultMessage: "Host returned after reboot and node is healthy",
			BootID:        currentBoot,
		})
		n.commandStore.removeRebootPending(pending.CommandID)
	}
}

func (n *Node) reportPendingRestartResults(ctx context.Context) {
	if n.commandStore == nil {
		return
	}
	for _, pending := range n.commandStore.restartPending() {
		st := n.Status()
		st.Healthy = st.ComputeHealthy()
		if !st.Healthy {
			// Keep pending; retry until healthy or operator intervenes.
			continue
		}
		n.persistOrReportResult(ctx, pendingResultRecord{
			CommandID:     pending.CommandID,
			Success:       true,
			ResultCode:    "restarted_healthy",
			ResultMessage: "Service restarted and node is healthy",
			BootID:        readBootID(),
		})
		n.commandStore.removeRestartPending(pending.CommandID)
	}
}

func (n *Node) flushPendingCommandResults(ctx context.Context) {
	if n.commandStore == nil {
		return
	}
	for _, pending := range n.commandStore.pendingResults() {
		err := n.cp.ReportCommandResult(ctx, pending.CommandID, controlplane.NodeCommandResultRequest{
			Success:       pending.Success,
			ResultCode:    pending.ResultCode,
			ResultMessage: pending.ResultMessage,
			BootID:        pending.BootID,
		})
		if err != nil {
			log.Printf("runtime: pending command result retry %s: %v", pending.CommandID, err)
			continue
		}
		n.commandStore.removePendingResult(pending.CommandID)
	}
}

func (n *Node) persistOrReportResult(ctx context.Context, rec pendingResultRecord) {
	n.ensureCommandStore()
	if err := n.commandStore.addPendingResult(rec); err != nil {
		log.Printf("runtime: durable pending result write failed %s: %v", rec.CommandID, err)
	}
	err := n.cp.ReportCommandResult(ctx, rec.CommandID, controlplane.NodeCommandResultRequest{
		Success:       rec.Success,
		ResultCode:    rec.ResultCode,
		ResultMessage: rec.ResultMessage,
		BootID:        rec.BootID,
	})
	if err != nil {
		log.Printf("runtime: command result %s queued for retry: %v", rec.CommandID, err)
		return
	}
	n.commandStore.removePendingResult(rec.CommandID)
}

func (n *Node) pollAndExecuteCommand(ctx context.Context) {
	cmd, err := n.cp.ClaimNextCommand(ctx)
	if errors.Is(err, controlplane.ErrNoCommand) {
		return
	}
	if err != nil {
		log.Printf("runtime: claim command: %v", err)
		return
	}
	if cmd == nil || strings.TrimSpace(cmd.ID) == "" {
		return
	}
	n.ensureCommandStore()
	if n.commandStore.WasExecuted(cmd.ID) {
		log.Printf("runtime: skip duplicate command execution id=%s type=%s", cmd.ID, cmd.Type)
		return
	}
	if err := n.cp.MarkCommandStarted(ctx, cmd.ID); err != nil {
		log.Printf("runtime: command started %s: %v", cmd.ID, err)
		n.reportCommandFailure(ctx, cmd.ID, "started_failed", err.Error())
		return
	}
	switch strings.TrimSpace(cmd.Type) {
	case "RenewCertificate":
		n.executeRenewCertificate(ctx, cmd.ID)
	case "RestartNyxveilService":
		n.executeRestartService(ctx, cmd.ID)
	case "RebootHost":
		n.executeRebootHost(ctx, cmd.ID)
	case "UpdateNodeLatest":
		n.executeUpdateNodeLatest(ctx, cmd)
	default:
		if !n.commandStore.TryMarkExecuted(cmd.ID) {
			return
		}
		n.reportCommandFailure(ctx, cmd.ID, "unsupported", fmt.Sprintf("unsupported command type %q", cmd.Type))
	}
}

func (n *Node) executeRenewCertificate(ctx context.Context, commandID string) {
	if !n.commandStore.TryMarkExecuted(commandID) {
		return
	}
	n.mu.RLock()
	cfg := *n.local
	lastOK := n.lastSuccessfulRenewal
	n.mu.RUnlock()
	if strings.TrimSpace(cfg.ACMEDomain) == "" {
		n.reportCommandFailure(ctx, commandID, "no_acme", "ACME domain is not configured on this node")
		return
	}
	// Explicit operator renew is allowed even when automatic renewal is not due.
	// Anti-abuse: refuse hammering Let's Encrypt within the cooldown window.
	if !lastOK.IsZero() && time.Since(lastOK) < explicitRenewRateLimitWindow {
		n.reportCommandFailure(ctx, commandID, "rate_limited",
			fmt.Sprintf("Certificate was renewed recently; retry after %s",
				explicitRenewRateLimitWindow.String()))
		return
	}
	_, _, _, _, err := n.issueACMEForced(ctx, cfg)
	if err != nil {
		n.reportCommandFailure(ctx, commandID, "renew_failed", safeRenewalError(err))
		return
	}
	n.reportCommandSuccess(ctx, commandID, "renewed", "Certificate renewed successfully")
}

func (n *Node) executeRestartService(ctx context.Context, commandID string) {
	n.ensureCommandStore()
	// Durable pending BEFORE MarkExecuted / restart — process may not survive systemctl restart.
	if err := n.commandStore.addRestartPending(commandID, time.Now().UTC().Format(time.RFC3339)); err != nil {
		n.reportCommandFailure(ctx, commandID, "journal_failed", err.Error())
		return
	}
	if !n.commandStore.TryMarkExecuted(commandID) {
		return
	}
	go func() {
		time.Sleep(1 * time.Second)
		if err := systemdRestartService(nyxveilServiceUnit); err != nil {
			log.Printf("runtime: service restart: %v", err)
			failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer cancel()
			n.commandStore.removeRestartPending(commandID)
			n.reportCommandFailure(failCtx, commandID, "restart_failed", err.Error())
		}
	}()
}

func (n *Node) executeRebootHost(ctx context.Context, commandID string) {
	n.ensureCommandStore()
	preBootID := readBootID()
	// Persist reboot pending before MarkExecuted / reboot — terminal success only after BootID changes + health.
	if err := n.commandStore.addRebootPending(commandID, preBootID); err != nil {
		n.reportCommandFailure(ctx, commandID, "journal_failed", err.Error())
		return
	}
	if !n.commandStore.TryMarkExecuted(commandID) {
		return
	}
	go func() {
		time.Sleep(2 * time.Second)
		if err := hostReboot(); err != nil {
			log.Printf("runtime: host reboot: %v", err)
			failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer cancel()
			n.commandStore.removeRebootPending(commandID)
			n.reportCommandFailure(failCtx, commandID, "reboot_failed", err.Error())
		}
	}()
}

func (n *Node) reportCommandSuccess(ctx context.Context, commandID, code, message string) {
	n.persistOrReportResult(ctx, pendingResultRecord{
		CommandID:     commandID,
		Success:       true,
		ResultCode:    code,
		ResultMessage: message,
		BootID:        readBootID(),
	})
}

func (n *Node) reportCommandFailure(ctx context.Context, commandID, code, message string) {
	n.persistOrReportResult(ctx, pendingResultRecord{
		CommandID:     commandID,
		Success:       false,
		ResultCode:    code,
		ResultMessage: message,
		BootID:        readBootID(),
	})
}
