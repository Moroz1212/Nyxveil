//go:build windows

package engine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/nyxveil/client-windows/internal/diag"
	"github.com/nyxveil/client-windows/internal/ipc"
	"github.com/nyxveil/client-windows/internal/recoverylog"
)

const scmServiceName = "NyxveilClientService"

// FinalizeSCMInstall configures/starts NyxveilClientService and waits for named-pipe
// Admin readiness. Prefers stop→config→start over DeleteService→CreateService to
// avoid ERROR_SERVICE_MARKED_FOR_DELETE (1072). On failure after create, rolls back
// orphan SCM entries. failAfter (tests): create|description|failure|start|ready.
func FinalizeSCMInstall(binPath, failAfter string) error {
	binPath = strings.TrimSpace(binPath)
	if binPath == "" {
		return fmt.Errorf("scm: binPath required")
	}
	abs, err := filepath.Abs(binPath)
	if err != nil {
		return err
	}
	ilog := openInstallerLog()
	defer ilog.close()
	ilog.logf("finalize-scm begin bin=%s failAfter=%q", abs, failAfter)

	created := false
	rollback := func(reason error) error {
		ilog.logf("ROLLBACK reason=%v", reason)
		_ = stopAndDeleteService(scmServiceName)
		if reason == nil {
			return fmt.Errorf("scm: rolled back after failure")
		}
		return fmt.Errorf("scm: %w (service rolled back)", reason)
	}

	// Preflight: clear dirty recovery journal so LocalSystem start cannot fail-closed
	// on stale tun_dns for an already-removed Wintun adapter (1.0.6 live root cause).
	if err := NewWindowsApplier().RecoverOnStartup(); err != nil {
		ilog.logf("preflight RecoverOnStartup: %v", err)
		return fmt.Errorf("scm: preflight journal recovery: %w", err)
	}
	ilog.logf("preflight RecoverOnStartup OK")

	exists := serviceExists(scmServiceName)
	ilog.logf("service_exists=%v", exists)

	if exists {
		ilog.logf("upgrade path: stop then config")
		if err := stopServiceFully(scmServiceName, ilog); err != nil {
			return fmt.Errorf("scm: stop existing: %w", err)
		}
		if out, err := exec.Command("sc.exe", "config", scmServiceName,
			"binPath=", `"`+abs+`"`,
			"start=", "auto",
			"obj=", "LocalSystem",
			"DisplayName=", "Nyxveil Client Service",
		).CombinedOutput(); err != nil {
			raw := strings.TrimSpace(string(out))
			ilog.logf("sc config failed: %v (%s)", err, raw)
			if err2 := stopAndDeleteService(scmServiceName); err2 != nil {
				return fmt.Errorf("scm: sc config: %w (%s); delete: %v", err, raw, err2)
			}
			exists = false
		} else {
			ilog.logf("sc config OK")
		}
	}

	if !exists {
		ilog.logf("create path")
		_ = stopServiceFully(scmServiceName, ilog)
		_ = deleteServiceWaitGone(scmServiceName, ilog)
		createArgs := []string{
			"create", scmServiceName,
			"binPath=", `"` + abs + `"`,
			"start=", "auto",
			"obj=", "LocalSystem",
			"DisplayName=", "Nyxveil Client Service",
		}
		if out, err := exec.Command("sc.exe", createArgs...).CombinedOutput(); err != nil {
			raw := strings.TrimSpace(string(out))
			ilog.logf("sc create failed: %v (%s)", err, raw)
			return fmt.Errorf("scm: sc create: %w (%s)", err, raw)
		}
		created = true
		ilog.logf("sc create OK")
	}
	if failAfter == "create" {
		return rollback(fmt.Errorf("injected failure after create"))
	}

	if out, err := exec.Command("sc.exe", "description", scmServiceName,
		"Nyxveil VPN client engine (Frozen Connector / Wintun)").CombinedOutput(); err != nil {
		raw := strings.TrimSpace(string(out))
		if created {
			return rollback(fmt.Errorf("sc description: %w (%s)", err, raw))
		}
		return fmt.Errorf("scm: sc description: %w (%s)", err, raw)
	}
	if failAfter == "description" {
		return rollback(fmt.Errorf("injected failure after description"))
	}

	if out, err := exec.Command("sc.exe", "failure", scmServiceName,
		"reset=", "86400",
		"actions=", "restart/5000/restart/10000/restart/30000").CombinedOutput(); err != nil {
		raw := strings.TrimSpace(string(out))
		if created {
			return rollback(fmt.Errorf("sc failure: %w (%s)", err, raw))
		}
		return fmt.Errorf("scm: sc failure: %w (%s)", err, raw)
	}
	if failAfter == "failure" {
		return rollback(fmt.Errorf("injected failure after failure-config"))
	}

	if out, err := exec.Command("sc.exe", "start", scmServiceName).CombinedOutput(); err != nil {
		raw := strings.TrimSpace(string(out))
		detail := captureServiceFailureDetail(scmServiceName)
		ilog.logf("sc start failed: %v (%s); %s", err, raw, detail)
		if created {
			return rollback(fmt.Errorf("sc start: %w (%s); %s", err, raw, detail))
		}
		return fmt.Errorf("scm: sc start: %w (%s); %s", err, raw, detail)
	}
	ilog.logf("sc start issued")
	if failAfter == "start" {
		return rollback(fmt.Errorf("injected failure after start"))
	}

	if !waitServiceState(scmServiceName, "RUNNING", 30*time.Second) {
		detail := captureServiceFailureDetail(scmServiceName)
		ilog.logf("not RUNNING: %s", detail)
		if created {
			return rollback(fmt.Errorf("service not RUNNING after start; %s", detail))
		}
		return fmt.Errorf("scm: service not RUNNING after start; %s", detail)
	}
	if !serviceIsLocalSystem(scmServiceName) {
		if created {
			return rollback(fmt.Errorf("service not LocalSystem"))
		}
		return fmt.Errorf("scm: service not LocalSystem")
	}
	ilog.logf("qc: %s", queryServiceQC(scmServiceName))
	if err := waitPipeReady(ipc.PipeName, 30*time.Second); err != nil {
		detail := captureServiceFailureDetail(scmServiceName)
		ilog.logf("pipe ready failed: %v; %s", err, detail)
		if created {
			return rollback(fmt.Errorf("pipe readiness: %w; %s", err, detail))
		}
		return fmt.Errorf("scm: pipe readiness: %w; %s", err, detail)
	}
	if failAfter == "ready" {
		return rollback(fmt.Errorf("injected failure after ready"))
	}
	ilog.logf("finalize-scm OK")
	_ = created
	return nil
}

type installerLog struct{ f *os.File }

func openInstallerLog() *installerLog {
	dir := filepath.Join(ipc.ClientDataDir(), "logs")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "nyxveil-installer.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return &installerLog{}
	}
	return &installerLog{f: f}
}

func (l *installerLog) logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	line := time.Now().Format("2006-01-02 15:04:05.000") + " " + msg
	diag.Info("INSTALLER", "scm", msg)
	if l != nil && l.f != nil {
		_, _ = l.f.WriteString(line + "\n")
	}
}

func (l *installerLog) close() {
	if l != nil && l.f != nil {
		_ = l.f.Close()
	}
}

func serviceExists(name string) bool {
	out, _ := exec.Command("sc.exe", "query", name).CombinedOutput()
	s := string(out)
	return !strings.Contains(s, "1060") && (strings.Contains(strings.ToUpper(s), "SERVICE_NAME") || strings.Contains(strings.ToUpper(s), "STATE"))
}

func stopServiceFully(name string, ilog *installerLog) error {
	if !serviceExists(name) {
		return nil
	}
	out, _ := exec.Command("sc.exe", "stop", name).CombinedOutput()
	if ilog != nil {
		ilog.logf("sc stop: %s", strings.TrimSpace(string(out)))
	}
	if !waitServiceState(name, "STOPPED", 45*time.Second) {
		return fmt.Errorf("service %s did not reach STOPPED", name)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		pid := servicePID(name)
		if pid == 0 {
			if ilog != nil {
				ilog.logf("service process exited")
			}
			return nil
		}
		if ilog != nil {
			ilog.logf("waiting service PID=%d exit", pid)
		}
		time.Sleep(400 * time.Millisecond)
	}
	return fmt.Errorf("service %s process still running after STOPPED", name)
}

func servicePID(name string) int {
	out, err := exec.Command("sc.exe", "queryex", name).CombinedOutput()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToUpper(line), "PID") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				n, _ := strconv.Atoi(parts[len(parts)-1])
				return n
			}
		}
	}
	return 0
}

func deleteServiceWaitGone(name string, ilog *installerLog) error {
	if !serviceExists(name) {
		return nil
	}
	_ = stopServiceFully(name, ilog)
	out, err := exec.Command("sc.exe", "delete", name).CombinedOutput()
	raw := strings.TrimSpace(string(out))
	if ilog != nil {
		ilog.logf("sc delete: err=%v out=%s", err, raw)
	}
	if waitServiceAbsent(name, 45*time.Second) {
		return nil
	}
	return fmt.Errorf("service %s still present after delete (err=%v out=%s)", name, err, raw)
}

func captureServiceFailureDetail(name string) string {
	var b strings.Builder
	qc, _ := exec.Command("sc.exe", "qc", name).CombinedOutput()
	qx, _ := exec.Command("sc.exe", "queryex", name).CombinedOutput()
	fmt.Fprintf(&b, "queryex=%s; qc=%s", strings.TrimSpace(string(qx)), strings.TrimSpace(string(qc)))
	logPath := filepath.Join(ipc.ClientDataDir(), "logs", "nyxveil-service.log")
	if raw, err := os.ReadFile(logPath); err == nil {
		lines := strings.Split(string(raw), "\n")
		n := len(lines)
		start := n - 30
		if start < 0 {
			start = 0
		}
		fmt.Fprintf(&b, "; service_log_tail=%s", strings.Join(lines[start:], " | "))
	}
	return b.String()
}

func queryServiceQC(name string) string {
	out, _ := exec.Command("sc.exe", "qc", name).CombinedOutput()
	return strings.TrimSpace(string(out))
}

// UninstallNetworkCleanup runs journal recovery and refuses to report clean if
// mutations remain. Call before deleting ProgramData during uninstall.
func UninstallNetworkCleanup() error {
	applier := NewWindowsApplier()
	if err := applier.RecoverOnStartup(); err != nil {
		return fmt.Errorf("uninstall recover: %w", err)
	}
	if _, err := os.Stat(recoverylog.DefaultPath()); err == nil {
		_ = recoverylog.Clear(recoverylog.DefaultPath())
		if _, err2 := os.Stat(recoverylog.DefaultPath()); err2 == nil {
			return fmt.Errorf("uninstall: route journal still present after recovery")
		}
	}
	return nil
}

func stopAndDeleteService(name string) error {
	_ = stopServiceFully(name, nil)
	return deleteServiceWaitGone(name, nil)
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
