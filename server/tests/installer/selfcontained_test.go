package installer_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
	root := findServerRoot(t)
	script := filepath.Join(root, filepath.FromSlash(rel))

	if runtime.GOOS == "windows" {
		if wslOK() {
			wslRoot := windowsToWSLPath(root)
			cmd := exec.Command("wsl", "-e", "bash", "-lc", "cd '"+wslRoot+"' && bash "+rel)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("wsl %s: %v\n%s", rel, err, out)
			}
			t.Log(string(out))
			return
		}
		bash, err := exec.LookPath("bash")
		if err != nil {
			t.Skip("windows without working WSL/bash: run " + rel + " on Linux")
		}
		cmd := exec.Command(bash, script)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git-bash %s: %v\n%s", rel, err, out)
		}
		t.Log(string(out))
		return
	}

	cmd := exec.Command("bash", script)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", rel, err, out)
	}
	t.Log(string(out))
}

func wslOK() bool {
	if _, err := exec.LookPath("wsl"); err != nil {
		return false
	}
	return exec.Command("wsl", "-e", "true").Run() == nil
}

func windowsToWSLPath(p string) string {
	p = filepath.ToSlash(p)
	if len(p) >= 2 && p[1] == ':' {
		drive := strings.ToLower(string(p[0]))
		return "/mnt/" + drive + p[2:]
	}
	return p
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
