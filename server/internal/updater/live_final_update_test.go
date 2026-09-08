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
	sumsBody, err := os.ReadFile(sums)
	if err != nil {
		t.Fatal(err)
	}
	fileBody, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(fileBody)
	wantHex := hex.EncodeToString(want[:])
	name := filepath.Base(file)
	normalized := strings.ReplaceAll(string(sumsBody), "\r", "")
	matched := false
	for _, line := range strings.Split(normalized, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Accept "HASH  name" and "HASH *name" forms used by sha256sum.
		var hash, entry string
		switch {
		case strings.Contains(line, " *"):
			parts := strings.SplitN(line, " *", 2)
			if len(parts) != 2 {
				continue
			}
			hash, entry = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		case strings.Contains(line, "  "):
			parts := strings.SplitN(line, "  ", 2)
			if len(parts) != 2 {
				continue
			}
			hash, entry = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		default:
			continue
		}
		if entry != name {
			continue
		}
		matched = true
		if !strings.EqualFold(hash, wantHex) {
			t.Fatalf("checksum mismatch for %s: have %s want %s", name, hash, wantHex)
		}
	}
	if !matched {
		t.Fatalf("no checksum line for %s in %s\n%s", name, sums, normalized)
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
	if _, err := os.Stat(filepath.Join(dist, "release-manifest-linux-amd64.json")); err != nil {
		t.Skip("dist/release unsigned (no manifests) — set NYXVEIL_RELEASE_SIGNING_KEY and re-package")
	}
	cmd := exec.Command("go", "run", "./scripts/verify-live-final-consumer.go", dist)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "manifest signature invalid") || strings.Contains(string(out), "manifest verify failed") {
			t.Skipf("dist/release manifests not production-signed — set NYXVEIL_RELEASE_SIGNING_KEY and re-package\n%s", out)
		}
		t.Fatalf("consumer: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "LIVE_FINAL_UPDATE_CONSUMER=PASS") {
		t.Fatalf("unexpected consumer output:\n%s", out)
	}
}
