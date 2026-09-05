package updater_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nyxveil/server/internal/updater"
)

func releaseManifestPath(t *testing.T, arch string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	p := filepath.Join(root, "dist", "release", "release-manifest-linux-"+arch+".json")
	if _, err := os.Stat(p); err != nil {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		url := "https://github.com/Moroz1212/Nyxveil/releases/download/server-v1.0.0/release-manifest-linux-" + arch + ".json"
		cmd := exec.Command("curl", "-fsSL", "-o", p, url)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("production manifest missing and download failed: %v\n%s", err, out)
		}
	}
	return p
}

func TestProductionManifestsParseAndMatchKnownAMD64SHA(t *testing.T) {
	amd64 := releaseManifestPath(t, "amd64")
	raw, err := os.ReadFile(amd64)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	got := hex.EncodeToString(sum[:])
	const want = "e4a4fcb21b4bcffbf6c08b28b757dc8f7a5b0f30c66d8a961c3a7960f5128261"
	if got != want {
		t.Fatalf("amd64 manifest SHA256=%s want %s (do not resign/reupload for installer-only fix)", got, want)
	}
	if _, err := updater.ParseManifest(raw, updater.UpdatePublicKey); err != nil {
		t.Fatalf("Go ParseManifest amd64: %v", err)
	}

	arm64 := releaseManifestPath(t, "arm64")
	raw64, err := os.ReadFile(arm64)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := updater.ParseManifest(raw64, updater.UpdatePublicKey); err != nil {
		t.Fatalf("Go ParseManifest arm64: %v", err)
	}
}

func TestShellCanonicalBytesMatchGo(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash required")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq required for shell canonicalization")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	installer := filepath.Join(root, "installer", "install.sh")

	for _, arch := range []string{"amd64", "arm64"} {
		man := releaseManifestPath(t, arch)
		raw, err := os.ReadFile(man)
		if err != nil {
			t.Fatal(err)
		}
		m, err := updater.ParseManifest(raw, updater.UpdatePublicKey)
		if err != nil {
			t.Fatal(err)
		}
		goCanon := updater.CanonicalManifestBytes(m)
		if len(goCanon) == 0 {
			t.Fatal("empty go canonical")
		}
		if goCanon[len(goCanon)-1] == '\n' {
			t.Fatal("Go CanonicalManifestBytes must not end with LF")
		}

		cmd := exec.Command("bash", installer, "--dump-canonical", man)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("shell --dump-canonical %s: %v\n%s", arch, err, out)
		}
		if len(out) == 0 {
			t.Fatal("empty shell canonical")
		}
		if out[len(out)-1] == '\n' {
			t.Fatalf("shell canonical has trailing LF (arch=%s len=%d)", arch, len(out))
		}
		if !bytes.Equal(goCanon, out) {
			t.Fatalf("canonical byte mismatch arch=%s\ngo  len=%d sha=%s\nsh  len=%d sha=%s\ngo=%q\nsh=%q",
				arch, len(goCanon), sha256Hex(goCanon), len(out), sha256Hex(out),
				truncate(goCanon, 120), truncate(out, 120))
		}
	}
}

func TestShellVerifyProductionManifests(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash required")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq required")
	}
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl required")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	installer := filepath.Join(root, "installer", "install.sh")

	for _, arch := range []string{"amd64", "arm64"} {
		man := releaseManifestPath(t, arch)
		cmd := exec.Command("bash", installer, "--verify-manifest", man)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("shell verify %s: %v\n%s", arch, err, out)
		}
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
