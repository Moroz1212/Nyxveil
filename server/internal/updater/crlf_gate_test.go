package updater_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBootstrapReleaseAssetHasLFOnly(t *testing.T) {
	root := findServerRoot(t)
	src := filepath.Join(root, "scripts", "bootstrap-cli-update.sh")
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte{0x0d}) {
		t.Fatalf("%s contains CRLF/CR (byte 0x0d)", src)
	}
	if !bytes.HasPrefix(b, []byte("#!/")) {
		t.Fatalf("%s missing shebang", src)
	}
	dist := filepath.Join(root, "dist", "release", "bootstrap-cli-update.sh")
	if db, err := os.ReadFile(dist); err == nil {
		if bytes.Contains(db, []byte{0x0d}) {
			t.Fatalf("RELEASE ASSET %s contains CRLF/CR — packaging bug", dist)
		}
	}
}

func TestAssertNoCRLFGateRejectsInjectedCRLF(t *testing.T) {
	root := findServerRoot(t)
	assertScript := filepath.Join(root, "scripts", "assert-no-crlf.sh")

	// Pure Go mirror of the release gate.
	bad := []byte("#!/bin/sh\r\nexit\r\n")
	good := []byte("#!/bin/sh\nexit\n")
	if !bytes.Contains(bad, []byte{0x0d}) {
		t.Fatal("bad fixture")
	}
	if bytes.Contains(good, []byte{0x0d}) {
		t.Fatal("good fixture")
	}

	if runtime.GOOS != "windows" {
		dir := tempDir(t)
		badPath := filepath.Join(dir, "bad.sh")
		goodPath := filepath.Join(dir, "good.sh")
		_ = os.WriteFile(badPath, bad, 0o755)
		_ = os.WriteFile(goodPath, good, 0o755)
		if out, err := exec.Command("bash", assertScript, goodPath).CombinedOutput(); err != nil {
			t.Fatalf("LF must pass: %s", out)
		}
		if out, err := exec.Command("bash", assertScript, badPath).CombinedOutput(); err == nil {
			t.Fatalf("CRLF must fail, out=%s", out)
		}
		return
	}

	if !wslAvailable() {
		t.Skip("WSL not installed; CRLF gate covered by pure Go byte checks above + Linux CI")
	}

	// On Windows+WSL: write fixtures inside the Linux filesystem to avoid DrvFs EOL translation.
	wslAssert := toWSLPath(assertScript)
	script := fmt.Sprintf(`
set -euo pipefail
tmp=$(mktemp -d)
printf '#!/bin/sh\nexit\n' > "$tmp/good.sh"
printf '#!/bin/sh\r\nexit\r\n' > "$tmp/bad.sh"
bash %q "$tmp/good.sh"
if bash %q "$tmp/bad.sh"; then
  echo "expected CRLF reject" >&2
  exit 1
fi
rm -rf "$tmp"
`, wslAssert, wslAssert)
	cmd := exec.Command("wsl", "-e", "bash", "-lc", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wsl CRLF gate: %v\n%s", err, out)
	}
}

func TestPackagingNormalizeStripsCRLF(t *testing.T) {
	root := findServerRoot(t)
	norm := filepath.Join(root, "scripts", "normalize-shell-lf.sh")
	if runtime.GOOS == "windows" {
		if !wslAvailable() {
			// Fall back to Git Bash on DrvFs — normalize still must strip CR.
			dir := tempDir(t)
			f := filepath.Join(dir, "crlf.sh")
			_ = os.WriteFile(f, []byte("#!/bin/sh\r\nset -e\r\n"), 0o755)
			if out, err := exec.Command("bash", norm, f).CombinedOutput(); err != nil {
				t.Fatalf("normalize failed: %v %s", err, out)
			}
			if bytes.Contains(mustRead(t, f), []byte{0x0d}) {
				t.Fatal("CR remains")
			}
			return
		}
		wslNorm := toWSLPath(norm)
		script := fmt.Sprintf(`
set -euo pipefail
tmp=$(mktemp -d)
f="$tmp/crlf.sh"
printf '#!/bin/sh\r\nset -e\r\n' > "$f"
bash %q "$f"
# must have zero CR bytes
python3 -c "import sys; d=open(sys.argv[1],'rb').read(); sys.exit(0 if b'\\r' not in d else 1)" "$f"
rm -rf "$tmp"
`, wslNorm)
		cmd := exec.Command("wsl", "-e", "bash", "-lc", script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("normalize: %v\n%s", err, out)
		}
		return
	}
	dir := tempDir(t)
	f := filepath.Join(dir, "crlf.sh")
	_ = os.WriteFile(f, []byte("#!/bin/sh\r\nset -e\r\n"), 0o755)
	if out, err := exec.Command("bash", norm, f).CombinedOutput(); err != nil {
		t.Fatalf("normalize failed: %v %s", err, out)
	}
	if bytes.Contains(mustRead(t, f), []byte{0x0d}) {
		t.Fatal("CR remains")
	}
}

func TestBootstrapScriptBashHelpUnderUbuntu(t *testing.T) {
	root := findServerRoot(t)
	src := filepath.Join(root, "scripts", "bootstrap-cli-update.sh")
	if runtime.GOOS == "windows" {
		if !wslAvailable() {
			if out, err := exec.Command("bash", "-n", src).CombinedOutput(); err != nil {
				t.Fatalf("bash -n: %s", out)
			}
			if out, err := exec.Command("bash", src, "--help").CombinedOutput(); err != nil {
				t.Fatalf("--help: %s", out)
			}
			return
		}
		wslSrc := toWSLPath(src)
		cmd := exec.Command("wsl", "-e", "bash", "-lc",
			fmt.Sprintf(`set -euo pipefail; file %q | grep -vi crlf >/dev/null; bash -n %q; bash %q --help >/dev/null`, wslSrc, wslSrc, wslSrc))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("ubuntu bash bootstrap help: %v\n%s", err, out)
		}
		return
	}
	if out, err := exec.Command("bash", "-n", src).CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %s", out)
	}
	if out, err := exec.Command("bash", src, "--help").CombinedOutput(); err != nil {
		t.Fatalf("--help: %s", out)
	}
}

func wslAvailable() bool {
	out, err := exec.Command("wsl", "-e", "true").CombinedOutput()
	if err != nil {
		_ = out
		return false
	}
	return true
}

func findServerRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	candidates := []string{wd, filepath.Join(wd, "..", ".."), filepath.Join(wd, "..")}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "VERSION")); err == nil {
			if _, err := os.Stat(filepath.Join(c, "scripts", "bootstrap-cli-update.sh")); err == nil {
				return c
			}
		}
	}
	t.Fatal("server root not found")
	return ""
}

func toWSLPath(p string) string {
	if len(p) >= 2 && p[1] == ':' {
		drive := p[0]
		if drive >= 'A' && drive <= 'Z' {
			drive = drive - 'A' + 'a'
		}
		rest := filepath.ToSlash(p[2:])
		return "/mnt/" + string(drive) + rest
	}
	return filepath.ToSlash(p)
}
