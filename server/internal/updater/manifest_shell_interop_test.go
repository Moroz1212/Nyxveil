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

func releaseManifestPath(t *testing.T, arch string) string {
	t.Helper()
	// Always fetch the published server-v1.0.0 manifest for the frozen SHA pin.
	// Local dist/release belongs to the current VERSION being packaged and must not
	// overwrite this regression anchor.
	dir := tempDir(t)
	p := filepath.Join(dir, "release-manifest-linux-"+arch+".json")
	url := "https://github.com/Moroz1212/Nyxveil/releases/download/server-v1.0.0/release-manifest-linux-" + arch + ".json"
	cmd := exec.Command("curl", "-fsSL", "-o", p, url)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("production server-v1.0.0 manifest download failed: %v\n%s", err, out)
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
	if _, err := updater.ParseManifest(raw); err != nil {
		t.Fatalf("Go ParseManifest amd64 (unsigned GitHub-trust model): %v", err)
	}

	arm64 := releaseManifestPath(t, "arm64")
	raw64, err := os.ReadFile(arm64)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := updater.ParseManifest(raw64); err != nil {
		t.Fatalf("Go ParseManifest arm64 (unsigned GitHub-trust model): %v", err)
	}
}

func TestCurrentDistReleaseManifestsParseUnsigned(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	verBytes, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	wantVer := strings.TrimSpace(string(verBytes))
	for _, arch := range []string{"amd64", "arm64"} {
		p := filepath.Join(root, "dist", "release", "release-manifest-linux-"+arch+".json")
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Skipf("dist/release missing (%v) — run package-release first", err)
		}
		m, err := updater.ParseManifest(raw)
		if err != nil {
			t.Fatalf("dist/release manifest must parse without signature: %v", err)
		}
		if m.Version != wantVer {
			t.Skipf("dist/release version=%s want %s (stale/foreign fixture; run package-release)", m.Version, wantVer)
		}
	}
}
