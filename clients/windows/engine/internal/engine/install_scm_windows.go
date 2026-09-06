//go:build windows

package engine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/nyxveil/client-windows/internal/ipc"
	"github.com/nyxveil/client-windows/internal/recoverylog"
)

const scmServiceName = "NyxveilClientService"

// FinalizeSCMInstall creates/configures/starts NyxveilClientService and waits for
// named-pipe Admin readiness. On any failure after sc create, the service is
// deleted so install never leaves an orphan SCM entry.
// failAfter (tests): create|description|failure|start|ready — injects failure after that step.
func FinalizeSCMInstall(binPath, failAfter string) error {
	binPath = strings.TrimSpace(binPath)
	if binPath == "" {
		return fmt.Errorf("scm: binPath required")
	}
	abs, err := filepath.Abs(binPath)
	if err != nil {
		return err
	}
	created := false
	rollback := func(reason error) error {
		_ = stopAndDeleteService(scmServiceName)
		if reason == nil {
			return fmt.Errorf("scm: rolled back after failure")
		}
		return fmt.Errorf("scm: %w (service rolled back)", reason)
	}

	_ = exec.Command("sc.exe", "stop", scmServiceName).Run()
	_ = waitServiceState(scmServiceName, "STOPPED", 20*time.Second)
	_ = exec.Command("sc.exe", "delete", scmServiceName).Run()
	_ = waitServiceAbsent(scmServiceName, 15*time.Second)

	createArgs := []string{
		"create", scmServiceName,
		"binPath=", `"` + abs + `"`,
		"start=", "auto",
		"DisplayName=", "Nyxveil Client Service",
	}
	if out, err := exec.Command("sc.exe", createArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("scm: sc create: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	created = true
	if failAfter == "create" {
		return rollback(fmt.Errorf("injected failure after create"))
	}

	if out, err := exec.Command("sc.exe", "description", scmServiceName,
		"Nyxveil VPN client engine (Frozen Connector / Wintun)").CombinedOutput(); err != nil {
		return rollback(fmt.Errorf("sc description: %w (%s)", err, strings.TrimSpace(string(out))))
	}
	if failAfter == "description" {
		return rollback(fmt.Errorf("injected failure after description"))
	}

	if out, err := exec.Command("sc.exe", "failure", scmServiceName,
		"reset=", "86400",
		"actions=", "restart/5000/restart/10000/restart/30000").CombinedOutput(); err != nil {
		return rollback(fmt.Errorf("sc failure: %w (%s)", err, strings.TrimSpace(string(out))))
	}
	if failAfter == "failure" {
		return rollback(fmt.Errorf("injected failure after failure-config"))
	}

	if out, err := exec.Command("sc.exe", "start", scmServiceName).CombinedOutput(); err != nil {
		return rollback(fmt.Errorf("sc start: %w (%s)", err, strings.TrimSpace(string(out))))
	}
	if failAfter == "start" {
		return rollback(fmt.Errorf("injected failure after start"))
	}

	if !waitServiceState(scmServiceName, "RUNNING", 30*time.Second) {
		return rollback(fmt.Errorf("service not RUNNING after start"))
	}
	if !serviceIsLocalSystem(scmServiceName) {
		return rollback(fmt.Errorf("service not LocalSystem"))
	}
	if err := waitPipeReady(ipc.PipeName, 30*time.Second); err != nil {
		return rollback(fmt.Errorf("pipe readiness: %w", err))
	}
	if failAfter == "ready" {
		return rollback(fmt.Errorf("injected failure after ready"))
	}
	_ = created
	return nil
}

// UninstallNetworkCleanup runs journal recovery and refuses to report clean if
// mutations remain. Call before deleting ProgramData during uninstall.
func UninstallNetworkCleanup() error {
	applier := NewWindowsApplier()
	if err := applier.RecoverOnStartup(); err != nil {
		return fmt.Errorf("uninstall recover: %w", err)
	}
	if _, err := os.Stat(recoverylog.DefaultPath()); err == nil {
		// Recover should have cleared; if file remains treat as dirty.
		_ = recoverylog.Clear(recoverylog.DefaultPath())
		if _, err2 := os.Stat(recoverylog.DefaultPath()); err2 == nil {
			return fmt.Errorf("uninstall: route journal still present after recovery")
		}
	}
	return nil
}

func stopAndDeleteService(name string) error {
	_ = exec.Command("sc.exe", "stop", name).Run()
	_ = waitServiceState(name, "STOPPED", 20*time.Second)
	_ = exec.Command("sc.exe", "delete", name).Run()
	if !waitServiceAbsent(name, 20*time.Second) {
		return fmt.Errorf("scm: service %s still present after delete", name)
	}
	return nil
}

func waitServiceAbsent(name string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, _ := exec.Command("sc.exe", "query", name).CombinedOutput()
		s := string(out)
		if strings.Contains(s, "1060") || (!strings.Contains(strings.ToUpper(s), "SERVICE_NAME") && !strings.Contains(strings.ToUpper(s), "STATE")) {
			return true
		}
		time.Sleep(400 * time.Millisecond)
	}
	out, _ := exec.Command("sc.exe", "query", name).CombinedOutput()
	return strings.Contains(string(out), "1060")
}

func waitServiceState(name, want string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	wantU := strings.ToUpper(want)
	for time.Now().Before(deadline) {
		out, _ := exec.Command("sc.exe", "query", name).CombinedOutput()
		s := strings.ToUpper(string(out))
		if wantU == "STOPPED" && strings.Contains(s, "1060") {
			return true
		}
		if strings.Contains(s, wantU) {
			return true
		}
		time.Sleep(400 * time.Millisecond)
	}
	return false
}

func serviceIsLocalSystem(name string) bool {
	out, err := exec.Command("sc.exe", "qc", name).CombinedOutput()
	if err != nil {
		return false
	}
	s := strings.ToUpper(string(out))
	return strings.Contains(s, "LOCALSYSTEM") || strings.Contains(s, "LOCAL SYSTEM")
}

func waitPipeReady(pipePath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		last = probePipeHelloWin(pipePath)
		if last == nil {
			return nil
		}
		time.Sleep(400 * time.Millisecond)
	}
	return last
}

func probePipeHelloWin(pipePath string) error {
	timeout := 2 * time.Second
	conn, err := winio.DialPipe(pipePath, &timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	hello := `{"v":1,"type":"hello"}` + "\n"
	if _, err := conn.Write([]byte(hello)); err != nil {
		return err
	}
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return err
	}
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &env); err != nil {
		return fmt.Errorf("pipe hello parse: %w (%s)", err, line)
	}
	if env.Type == "" {
		return fmt.Errorf("pipe hello empty type")
	}
	return nil
}
