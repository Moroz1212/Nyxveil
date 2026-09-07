package updater_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nyxveil/server/internal/updater"
)

func repoRootFromUpdaterTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestBootstrapChecksumLF(t *testing.T) {
	dir := t.TempDir()
	body := []byte("bootstrap-body\n")
	path := filepath.Join(dir, "bootstrap-cli-update.sh")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	sums := filepath.Join(dir, "SHA256SUMS")
	line := hex.EncodeToString(sum[:]) + "  bootstrap-cli-update.sh\n"
	if err := os.WriteFile(sums, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	assertChecksumParse(t, sums, path)
}

func TestBootstrapChecksumCRLF(t *testing.T) {
	dir := t.TempDir()
	body := []byte("bootstrap-body\n")
	path := filepath.Join(dir, "bootstrap-cli-update.sh")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	sums := filepath.Join(dir, "SHA256SUMS")
	line := hex.EncodeToString(sum[:]) + "  bootstrap-cli-update.sh\r\n"
	if err := os.WriteFile(sums, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	assertChecksumParse(t, sums, path)
}

func assertChecksumParse(t *testing.T, sums, file string) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash required")
	}
	script := `
set -euo pipefail
sums="$1"
file="$2"
name="$(basename "$file")"
(
  cd "$(dirname "$file")"
  tr -d '\r' < "$sums" | grep -E " [*]?${name}$" | sha256sum -c - >/dev/null
)
`
	cmd := exec.Command(bash, "-c", script, "bash", sums, file)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checksum parse: %v\n%s", err, out)
	}
}

func TestBootstrapTrustUsesReleaseSigningRoot(t *testing.T) {
	root := repoRootFromUpdaterTest(t)
	const pubHex = "caf921521e213cb1bcdc2f9df4816c2ecd43222b23a47d6f869672e6ab0e79af"
	for _, rel := range []string{
		filepath.Join("scripts", "live-final-update.sh"),
		filepath.Join("scripts", "bootstrap-cli-update.sh"),
		filepath.Join("installer", "install.sh"),
	} {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), pubHex) {
			t.Fatalf("%s missing UpdatePublicKey hex", rel)
		}
	}
	got := hex.EncodeToString(updater.UpdatePublicKey)
	if got != pubHex {
		t.Fatalf("UpdatePublicKey=%s want %s", got, pubHex)
	}
	live := string(mustRead(t, filepath.Join(root, "scripts", "live-final-update.sh")))
	if strings.Contains(live, `${SCRIPT_DIR}/VERSION`) || strings.Contains(live, "${SCRIPT_DIR}/VERSION") {
		t.Fatal("live-final-update.sh must not read VERSION from SCRIPT_DIR/cwd")
	}
	if !strings.Contains(live, "mktemp -d") {
		t.Fatal("live-final-update.sh must use private mktemp workdir")
	}
	if !strings.Contains(live, `tr -d '\r'`) {
		t.Fatal("live-final-update.sh must CRLF-normalize checksum parsing")
	}
}

func TestLiveFinalUpdateConsumerDistRelease(t *testing.T) {
	root := repoRootFromUpdaterTest(t)
	dist := filepath.Join(root, "dist", "release")
	if _, err := os.Stat(filepath.Join(dist, "live-final-update.sh")); err != nil {
		t.Skip("dist/release not packaged yet")
	}
	cmd := exec.Command("go", "run", "./scripts/verify-live-final-consumer.go", dist)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("consumer: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "LIVE_FINAL_UPDATE_CONSUMER=PASS") {
		t.Fatalf("unexpected consumer output:\n%s", out)
	}
}
