package updater_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	// server-v1.0.0 was signed with the pre-1.1.4 UpdatePublicKey trust root.
	legacyPub := ed25519.PublicKey{
		0xf6, 0x3d, 0x2c, 0x80, 0x01, 0xdf, 0x3d, 0x7b,
		0x2e, 0xfd, 0xd1, 0x71, 0xa1, 0x64, 0x63, 0x26,
		0x0c, 0xb7, 0x19, 0x0d, 0x61, 0xef, 0x56, 0x44,
		0x19, 0xcc, 0x08, 0x36, 0x77, 0x7d, 0x17, 0x6f,
	}
	if _, err := updater.ParseManifest(raw, legacyPub); err != nil {
		t.Fatalf("Go ParseManifest amd64 (legacy trust root): %v", err)
	}

	arm64 := releaseManifestPath(t, "arm64")
	raw64, err := os.ReadFile(arm64)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := updater.ParseManifest(raw64, legacyPub); err != nil {
		t.Fatalf("Go ParseManifest arm64 (legacy trust root): %v", err)
	}
}

func TestCurrentDistReleaseManifestsVerifyWithUpdatePublicKey(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	for _, arch := range []string{"amd64", "arm64"} {
		p := filepath.Join(root, "dist", "release", "release-manifest-linux-"+arch+".json")
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Skipf("dist/release missing (%v) — run package-release first", err)
		}
		m, err := updater.ParseManifest(raw, updater.UpdatePublicKey)
		if err != nil {
			t.Fatalf("current %s manifest: %v", arch, err)
		}
		verBytes, err := os.ReadFile(filepath.Join(root, "VERSION"))
		if err != nil {
			t.Fatal(err)
		}
		wantVer := strings.TrimSpace(string(verBytes))
		if m.Version != wantVer {
			t.Fatalf("version=%s want %s", m.Version, wantVer)
		}
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
		legacyPub := ed25519.PublicKey{
			0xf6, 0x3d, 0x2c, 0x80, 0x01, 0xdf, 0x3d, 0x7b,
			0x2e, 0xfd, 0xd1, 0x71, 0xa1, 0x64, 0x63, 0x26,
			0x0c, 0xb7, 0x19, 0x0d, 0x61, 0xef, 0x56, 0x44,
			0x19, 0xcc, 0x08, 0x36, 0x77, 0x7d, 0x17, 0x6f,
		}
		m, err := updater.ParseManifest(raw, legacyPub)
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

func TestBootstrapShellCanonicalBytesMatchGo112Assets(t *testing.T) {
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
	bootstrap := filepath.Join(root, "scripts", "bootstrap-cli-update.sh")
	manifest := &updater.Manifest{
		Version: "1.1.2", Arch: "linux/amd64", MinCore: "1.0.0", MinProtocol: 1,
		Assets: []updater.Asset{{
			Name: "nyxveilctl", SHA256: strings.Repeat("a", 64), URL: "https://example.invalid/nyxveilctl",
			Destination: "/usr/local/sbin/nyxveilctl", Mode: "0755", Required: true,
		}},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tempDir(t), "manifest.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("bash", bootstrap, "--dump-canonical", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := updater.CanonicalManifestBytes(manifest)
	if !bytes.Equal(out, want) {
		t.Fatalf("bootstrap canonical mismatch\ngo=%q\nsh=%q", want, out)
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

	// Historical 1.0.0 manifests require the legacy PUB_HEX. After 1.1.4 trust-root
	// rotation the installer embeds the new key, so shell verify of 1.0.0 is skipped
	// here; Go legacy-key coverage lives in TestProductionManifestsParseAndMatchKnownAMD64SHA.
	t.Skip("shell verify of server-v1.0.0 requires legacy PUB_HEX; covered by Go legacy ParseManifest")

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
