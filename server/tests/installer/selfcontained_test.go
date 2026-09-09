package installer_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestCurlInstallerSelfContained runs scripts/test-curl-installer.sh via bash (Linux/WSL/Git Bash).
func TestCurlInstallerSelfContained(t *testing.T) {
	runInstallerBashScript(t, "scripts/test-curl-installer.sh")
}

// TestInstallerManagementAssetsContract covers the Ubuntu 24.04 regression where
// download_or_copy_binaries rejected nyxveil-update-service / nyxveil-management-polkit.
func TestInstallerManagementAssetsContract(t *testing.T) {
	runInstallerBashScript(t, "scripts/test-installer-management-assets.sh")
}

// TestInstallerVersionResolution covers env override, local VERSION, and
// resolve_stable_server_version (mocked curl; no network).
func TestInstallerVersionResolution(t *testing.T) {
	runInstallerBashScript(t, "scripts/test-installer-version-resolution.sh")
}

func runInstallerBashScript(t *testing.T, rel string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// Authoritative installer bash gates run on Linux Server CI (and via scripts/test-*.sh).
		t.Skip("installer bash contract tests require Linux")
	}
	root := findServerRoot(t)
	script := filepath.Join(root, filepath.FromSlash(rel))
	cmd := exec.Command("bash", script)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", rel, err, out)
	}
	t.Log(string(out))
}

func findServerRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// tests/installer → server root
	cand := filepath.Clean(filepath.Join(wd, "..", ".."))
	if _, err := os.Stat(filepath.Join(cand, "installer", "install.sh")); err == nil {
		return cand
	}
	cand = wd
	if _, err := os.Stat(filepath.Join(cand, "installer", "install.sh")); err == nil {
		return cand
	}
	t.Fatalf("cannot locate server root from %s", wd)
	return ""
}
