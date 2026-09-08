package runtime

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/nyxveil/server/internal/configure"
	"github.com/nyxveil/server/internal/controlplane"
	"github.com/nyxveil/server/internal/localconfig"
	"github.com/nyxveil/server/internal/nodetls"
	"github.com/nyxveil/server/internal/paths"
)

const (
	commandPollInterval        = 7 * time.Second
	managementCapabilitiesList = "certificate_renew,service_restart,host_reboot,node_update"
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
	n.reportPendingRebootResults(ctx)
	n.completePendingUpdate(ctx)

	ticker := time.NewTicker(commandPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !n.cpOK.Load() {
				continue
			}
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
		err := n.cp.ReportCommandResult(ctx, pending.CommandID, controlplane.NodeCommandResultRequest{
			Success:       true,
			ResultCode:    "rebooted",
			ResultMessage: "Host returned after reboot",
			BootID:        currentBoot,
		})
		if err != nil {
			log.Printf("runtime: reboot result for %s: %v", pending.CommandID, err)
			continue
		}
		n.commandStore.removeRebootPending(pending.CommandID)
	}
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
	if !n.commandStore.TryMarkExecuted(cmd.ID) {
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
		n.executeUpdateNodeLatest(ctx, cmd.ID)
	default:
		n.reportCommandFailure(ctx, cmd.ID, "unsupported", fmt.Sprintf("unsupported command type %q", cmd.Type))
	}
}

func (n *Node) executeRenewCertificate(ctx context.Context, commandID string) {
	n.mu.RLock()
	cfg := *n.local
	n.mu.RUnlock()
	if strings.TrimSpace(cfg.ACMEDomain) == "" {
		n.reportCommandFailure(ctx, commandID, "no_acme", "ACME domain is not configured on this node")
		return
	}
	notDue, err := certNotDueForRenewal(cfg)
	if err != nil {
		n.reportCommandFailure(ctx, commandID, "precheck_failed", err.Error())
		return
	}
	if notDue {
		n.reportCommandFailure(ctx, commandID, "not_due", "Сертификат пока не требует обновления.")
		return
	}
	_, _, _, _, err = n.issueACME(ctx, cfg)
	if err != nil {
		n.reportCommandFailure(ctx, commandID, "renew_failed", safeRenewalError(err))
		return
	}
	n.reportCommandSuccess(ctx, commandID, "renewed", "Certificate renewed successfully")
}

func certNotDueForRenewal(cfg localconfig.File) (bool, error) {
	certFile := cfg.TLSCertFile
	keyFile := cfg.TLSKeyFile
	if certFile == "" {
		certFile = paths.TLSCert()
	}
	if keyFile == "" {
		keyFile = paths.TLSKey()
	}
	domain := strings.TrimSpace(cfg.ACMEDomain)
	if !nodetls.Exists(nodetls.Paths{CertFile: certFile, KeyFile: keyFile}) {
		return false, nil
	}
	if configure.ValidateLeafForDomain(certFile, keyFile, domain, time.Now()) != nil {
		return false, nil
	}
	existing, err := nodetls.Load(nodetls.Paths{CertFile: certFile, KeyFile: keyFile})
	if err != nil {
		return false, err
	}
	if len(existing.Certificate) == 0 {
		return false, nil
	}
	leaf, err := x509.ParseCertificate(existing.Certificate[0])
	if err != nil {
		return false, err
	}
	return time.Until(leaf.NotAfter) > 30*24*time.Hour, nil
}

func (n *Node) executeRestartService(ctx context.Context, commandID string) {
	if err := systemdRestartService(nyxveilServiceUnit); err != nil {
		n.reportCommandFailure(ctx, commandID, "restart_failed", err.Error())
		return
	}
	// Best-effort success before this process is stopped by systemd.
	_ = n.cp.ReportCommandResult(ctx, commandID, controlplane.NodeCommandResultRequest{
		Success:       true,
		ResultCode:    "restarted",
		ResultMessage: "Service restart initiated",
		BootID:        readBootID(),
	})
}

func (n *Node) executeRebootHost(ctx context.Context, commandID string) {
	preBootID := readBootID()
	if err := n.cp.ReportCommandResult(ctx, commandID, controlplane.NodeCommandResultRequest{
		Success:       true,
		ResultCode:    "reboot_accepted",
		ResultMessage: "Host reboot accepted",
		BootID:        preBootID,
	}); err != nil {
		log.Printf("runtime: reboot accepted report %s: %v", commandID, err)
		return
	}
	n.commandStore.addRebootPending(commandID, preBootID)
	go func() {
		time.Sleep(2 * time.Second)
		if err := hostReboot(); err != nil {
			log.Printf("runtime: host reboot: %v", err)
			failCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			n.reportCommandFailure(failCtx, commandID, "reboot_failed", err.Error())
			n.commandStore.removeRebootPending(commandID)
		}
	}()
}

func (n *Node) reportCommandSuccess(ctx context.Context, commandID, code, message string) {
	err := n.cp.ReportCommandResult(ctx, commandID, controlplane.NodeCommandResultRequest{
		Success:       true,
		ResultCode:    code,
		ResultMessage: message,
		BootID:        readBootID(),
	})
	if err != nil {
		log.Printf("runtime: command result %s: %v", commandID, err)
	}
}

func (n *Node) reportCommandFailure(ctx context.Context, commandID, code, message string) {
	err := n.cp.ReportCommandResult(ctx, commandID, controlplane.NodeCommandResultRequest{
		Success:       false,
		ResultCode:    code,
		ResultMessage: message,
		BootID:        readBootID(),
	})
	if err != nil {
		log.Printf("runtime: command failure %s: %v", commandID, err)
	}
}
