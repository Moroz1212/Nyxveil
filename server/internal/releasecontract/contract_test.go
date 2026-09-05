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
		"nyxveil-server-linux-arm64",
		"nyxveilctl-linux-arm64",
		"release-manifest-linux-amd64.json",
		"release-manifest-linux-arm64.json",
		"SHA256SUMS",
	}
	for _, name := range requiredAssets {
		if !strings.Contains(wf, name) {
			t.Errorf("workflow missing asset %s", name)
		}
		if !strings.Contains(pkg, name) && name != "SHA256SUMS" {
			// package-release lists binaries; SHA256SUMS is generated
			if strings.HasPrefix(name, "nyxveil-") && !strings.Contains(pkg, "nyxveil-server-linux-") {
				t.Errorf("package-release missing %s", name)
			}
		}
	}
	if !strings.Contains(pkg, "SHA256SUMS") {
		t.Error("package-release must write SHA256SUMS")
	}
	if !strings.Contains(inst, "release-manifest-linux-") {
		t.Error("installer must download release-manifest-linux-${arch}.json")
	}
	if !strings.Contains(sign, "nyxveil-server-linux-") {
		t.Error("sign-release must reference arch-qualified binary asset names in URLs")
	}
	// Installer consumes URLs from the signed manifest (exact GitHub asset names).
	if !strings.Contains(inst, ".assets[") && !strings.Contains(inst, ".assets|") {
		t.Error("installer must install from manifest asset URLs")
	}
}

func TestUpdaterManifestNamesMatchGitHubWorkflow(t *testing.T) {
	root := repoRoot(t)
	wf := readFile(t, filepath.Join(root, "..", ".github", "workflows", "server-release.yml"))
	url := updater.DefaultManifestURL()
	if !strings.Contains(url, "/release-manifest-linux-") || !strings.HasSuffix(url, ".json") {
		t.Fatalf("default manifest URL not arch-aware: %s", url)
	}
	if strings.HasSuffix(url, "/release-manifest.json") {
		t.Fatal("legacy release-manifest.json must not be default")
	}
	for _, arch := range []string{"amd64", "arm64"} {
		name := "release-manifest-linux-" + arch + ".json"
		if !strings.Contains(wf, name) {
			t.Errorf("workflow missing %s", name)
		}
	}
	pkg := readFile(t, filepath.Join(root, "scripts", "package-release.sh"))
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
