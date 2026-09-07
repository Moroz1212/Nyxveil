package releasecontract_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nyxveil/server/internal/updater"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestInstallerAssetNamesMatchGitHubWorkflow(t *testing.T) {
	root := repoRoot(t)
	wf := readFile(t, filepath.Join(root, "..", ".github", "workflows", "server-release.yml"))
	inst := readFile(t, filepath.Join(root, "installer", "install.sh"))
	pkg := readFile(t, filepath.Join(root, "scripts", "package-release.sh"))
	sign := readFile(t, filepath.Join(root, "scripts", "sign-release.go"))

	requiredAssets := []string{
		"nyxveil-server-linux-amd64",
		"nyxveilctl-linux-amd64",
		"nyxveil-catalog-verify-linux-amd64",
		"nyxveil-server-linux-arm64",
		"nyxveilctl-linux-arm64",
		"nyxveil-catalog-verify-linux-arm64",
		"production-gate.sh",
		"VERSION",
		"THIRD_PARTY_CORE.md",
		"release-manifest-linux-amd64.json",
		"release-manifest-linux-arm64.json",
		"SHA256SUMS",
	}
	for _, name := range requiredAssets {
		if !strings.Contains(pkg, name) {
			t.Errorf("package upload list missing asset %s", name)
		}
	}
	if !strings.Contains(wf, "UPLOAD-LIST-server-v${VERSION}.txt") || !strings.Contains(wf, "mapfile -t BASENAMES") {
		t.Error("workflow must consume the generated upload list")
	}
	if !strings.Contains(pkg, "production-gate.sh") {
		t.Error("package-release must ship production-gate.sh")
	}
	if !strings.Contains(sign, "production-gate") || !strings.Contains(sign, "share-version") {
		t.Error("sign-release must include production-gate and share-version assets")
	}
	for _, field := range []string{"Destination:", "Mode:", "Required: true"} {
		if !strings.Contains(sign, field) {
			t.Errorf("sign-release missing authoritative asset field %s", field)
		}
	}
	if !strings.Contains(inst, "production-gate") || !strings.Contains(inst, "/usr/local/share/nyxveil") {
		t.Error("installer must install production-gate under /usr/local/share/nyxveil")
	}
	if !strings.Contains(inst, "release-manifest-linux-") {
		t.Error("installer must download release-manifest-linux-${arch}.json")
	}
	if !strings.Contains(sign, "nyxveil-server-linux-") {
		t.Error("sign-release must reference arch-qualified binary asset names in URLs")
	}
	if !strings.Contains(inst, ".assets[") && !strings.Contains(inst, ".assets|") {
		t.Error("installer must install from manifest asset URLs")
	}
	for _, name := range updater.RequiredAssetNames {
		if name == "nyxveil-server" {
			continue
		}
		if !strings.Contains(sign, name) {
			t.Errorf("sign-release missing required asset %s", name)
		}
	}
}

func TestProductVersionIs115(t *testing.T) {
	root := repoRoot(t)
	if got := strings.TrimSpace(readFile(t, filepath.Join(root, "VERSION"))); got != "1.1.5" {
		t.Fatalf("VERSION=%q want 1.1.5", got)
	}
	for _, file := range []string{
		filepath.Join(root, "internal", "version", "version.go"),
		filepath.Join(root, "installer", "install.sh"),
		filepath.Join(root, "scripts", "bootstrap-cli-update.sh"),
		filepath.Join(root, "scripts", "serv_wrappers.sh"),
		filepath.Join(root, "scripts", "production-gate.sh"),
		filepath.Join(root, "scripts", "live-final-update.sh"),
	} {
		if !strings.Contains(readFile(t, file), "1.1.5") {
			t.Errorf("%s does not contain product version 1.1.5", file)
		}
	}
}

func TestUpdaterManifestNamesMatchGitHubWorkflow(t *testing.T) {
	root := repoRoot(t)
	wf := readFile(t, filepath.Join(root, "..", ".github", "workflows", "server-release.yml"))
	pkg := readFile(t, filepath.Join(root, "scripts", "package-release.sh"))
	if !strings.Contains(wf, "UPLOAD_LIST") {
		t.Fatal("release workflow must upload from the generated list")
	}
	url := updater.DefaultManifestURL()
	if !strings.Contains(url, "/release-manifest-linux-") || !strings.HasSuffix(url, ".json") {
		t.Fatalf("default manifest URL not arch-aware: %s", url)
	}
	if strings.HasSuffix(url, "/release-manifest.json") {
		t.Fatal("legacy release-manifest.json must not be default")
	}
	for _, arch := range []string{"amd64", "arm64"} {
		name := "release-manifest-linux-" + arch + ".json"
		if !strings.Contains(pkg, name) {
			t.Errorf("package upload list missing %s", name)
		}
	}
	if !strings.Contains(pkg, "release-manifest-linux-amd64.json") {
		t.Fatal("package-release.sh must emit amd64 manifest")
	}
	// May mention the legacy path only when deleting it.
	if strings.Contains(pkg, "> dist/release-manifest.json") || strings.Contains(pkg, "/release-manifest.json\"") {
		if !strings.Contains(pkg, "rm -f") {
			t.Fatal("package-release must not emit legacy release-manifest.json")
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
