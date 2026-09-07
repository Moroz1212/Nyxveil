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
	root := findServerRoot(t)
	script := filepath.Join(root, "scripts", "test-curl-installer.sh")

	if runtime.GOOS == "windows" {
		if wslOK() {
			wslRoot := windowsToWSLPath(root)
			cmd := exec.Command("wsl", "-e", "bash", "-lc", "cd '"+wslRoot+"' && bash scripts/test-curl-installer.sh")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("wsl test-curl-installer: %v\n%s", err, out)
			}
			t.Log(string(out))
			return
		}
		// Git Bash fallback when WSL optional component is not installed.
		bash, err := exec.LookPath("bash")
		if err != nil {
			t.Skip("windows without working WSL/bash: run scripts/test-curl-installer.sh on Linux")
		}
		cmd := exec.Command(bash, script)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git-bash test-curl-installer: %v\n%s", err, out)
		}
		t.Log(string(out))
		return
	}

	cmd := exec.Command("bash", script)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test-curl-installer: %v\n%s", err, out)
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
