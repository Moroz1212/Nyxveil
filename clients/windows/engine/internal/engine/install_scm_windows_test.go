//go:build windows

package engine_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/nyxveil/client-windows/internal/engine"
	"golang.org/x/sys/windows"
)

func isElevated() bool {
	var token windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
	if err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

func serviceExists(name string) bool {
	out, _ := exec.Command("sc.exe", "query", name).CombinedOutput()
	return !strings.Contains(string(out), "1060")
}

func TestFinalizeSCMFailAfterLeavesNoOrphan(t *testing.T) {
	if !isElevated() {
		t.Skip("requires elevation")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Use the built service binary if present; otherwise skip (unit test host is engine.test).
	svc := os.Getenv("NYXVEIL_SERVICE_EXE")
	if svc == "" {
		t.Skip("set NYXVEIL_SERVICE_EXE to Nyxveil.Service.exe for SCM fail-injection")
	}
	_ = exe
	steps := []string{"create", "description", "failure", "start", "ready"}
	for _, step := range steps {
		t.Run(step, func(t *testing.T) {
			err := engine.FinalizeSCMInstall(svc, step)
			if err == nil {
				t.Fatal("expected injected failure")
			}
			if serviceExists("NyxveilClientService") {
				t.Fatalf("orphan service remains after fail-after=%s", step)
			}
		})
	}
}

func TestUninstallNetworkCleanupIdempotent(t *testing.T) {
	if err := engine.UninstallNetworkCleanup(); err != nil {
		if !isElevated() && (os.IsPermission(err) || strings.Contains(strings.ToLower(err.Error()), "access is denied")) {
			t.Skipf("requires elevation to touch ProgramData journal: %v", err)
		}
		t.Fatal(err)
	}
}
