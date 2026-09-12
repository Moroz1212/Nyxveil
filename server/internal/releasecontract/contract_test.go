package releasecontract_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/nyxveil/server/internal/updater"
	"github.com/nyxveil/server/internal/version"
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
	sign := readFile(t, filepath.Join(root, "scripts", "make-release-manifest.go"))

	requiredAssets := []string{
		"nyxveil-server-linux-amd64",
		"nyxveilctl-linux-amd64",
		"nyxveil-catalog-verify-linux-amd64",
		"nyxveil-server-linux-arm64",
		"nyxveilctl-linux-arm64",
		"nyxveil-catalog-verify-linux-arm64",
		"production-gate.sh",
		"nyxveil-update.service",
		"50-nyxveil-management.rules",
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
		t.Error("make-release-manifest must include production-gate and share-version assets")
	}
	if !strings.Contains(sign, "nyxveil-update-service") || !strings.Contains(sign, "nyxveil-management-polkit") {
		t.Error("make-release-manifest must include management update unit and polkit assets")
	}
	for _, field := range []string{"Destination:", "Mode:", "Required: true"} {
		if !strings.Contains(sign, field) {
			t.Errorf("make-release-manifest missing authoritative asset field %s", field)
		}
	}
	if !strings.Contains(inst, "production-gate") || !strings.Contains(inst, "/usr/local/share/nyxveil") {
		t.Error("installer must install production-gate under /usr/local/share/nyxveil")
	}
	if !strings.Contains(inst, "nyxveil-update.service") || !strings.Contains(inst, "50-nyxveil-management.rules") {
		t.Error("installer must install management update unit and polkit rule")
	}
	if !strings.Contains(inst, "release-manifest-linux-") {
		t.Error("installer must download release-manifest-linux-${arch}.json")
	}
	if !strings.Contains(sign, "nyxveil-server-linux-") {
		t.Error("make-release-manifest must reference arch-qualified binary asset names in URLs")
	}
	if !strings.Contains(inst, ".assets[") && !strings.Contains(inst, ".assets|") {
		t.Error("installer must install from manifest asset URLs")
	}
	for _, name := range updater.RequiredAssetNames {
		if name == "nyxveil-server" {
			continue
		}
		if !strings.Contains(sign, name) {
			t.Errorf("make-release-manifest missing required asset %s", name)
		}
	}
}

func TestProductVersionConsistency(t *testing.T) {
	root := repoRoot(t)
	want := strings.TrimSpace(strings.ReplaceAll(readFile(t, filepath.Join(root, "VERSION")), "\r", ""))
	if want != "1.1.17" {
		t.Fatalf("VERSION=%q want 1.1.17", want)
	}
	if version.ServerVersion != want || version.CLIVersion != want {
		t.Fatalf("version.go Server=%q CLI=%q want %q", version.ServerVersion, version.CLIVersion, want)
	}

	checks := []struct {
		rel   string
		regex string
	}{
		{"internal/version/version.go", `ServerVersion\s*=\s*"` + regexp.QuoteMeta(want) + `"`},
		{"internal/version/version.go", `CLIVersion\s*=\s*"` + regexp.QuoteMeta(want) + `"`},
		// install.sh must not silently pin an older default than VERSION; either
		// exact VERSION default or dynamic stable resolution (NYXVEIL_VERSION_RESOLVE).
		{"installer/install.sh", `(NYXVEIL_VERSION_RESOLVE|resolve_stable_server_version|NYXVEIL_VERSION:-` + regexp.QuoteMeta(want) + `})`},
		{"scripts/live-final-update.sh", `DEFAULT_VERSION="` + regexp.QuoteMeta(want) + `"`},
		{"scripts/bootstrap-cli-update.sh", `NYXVEIL_BOOTSTRAP_VERSION:-` + regexp.QuoteMeta(want) + `}`},
		{"scripts/serv_wrappers.sh", `NYXVEIL_BOOTSTRAP_VERSION:-` + regexp.QuoteMeta(want) + `}`},
		{"scripts/production-gate.sh", `NYXVEIL_EXPECTED_VERSION:-` + regexp.QuoteMeta(want) + `}`},
	}
	for _, c := range checks {
		body := readFile(t, filepath.Join(root, filepath.FromSlash(c.rel)))
		re := regexp.MustCompile(c.regex)
		if !re.MatchString(body) {
			t.Errorf("%s missing product-version pattern %s", c.rel, c.regex)
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

func TestManagementAssetsHaveFixedAllowlist(t *testing.T) {
	root := repoRoot(t)
	sign := readFile(t, filepath.Join(root, "scripts", "make-release-manifest.go"))
	if !strings.Contains(sign, `paths.UpdateServiceUnit()`) || !strings.Contains(sign, `paths.ManagementPolkitRule()`) {
		t.Fatal("make-release-manifest must use fixed path helpers for management assets")
	}
	if !strings.Contains(sign, `"0644"`) {
		t.Fatal("management assets must be mode 0644")
	}
	unit := readFile(t, filepath.Join(root, "systemd", "nyxveil-update.service"))
	if !strings.Contains(unit, "Type=oneshot") || !strings.Contains(unit, "User=root") {
		t.Fatal("update unit contract broken")
	}
	if !strings.Contains(unit, "ExecStart=/usr/local/sbin/nyxveilctl update") {
		t.Fatal("update unit ExecStart must be fixed nyxveilctl update")
	}
	rule := readFile(t, filepath.Join(root, "systemd", "50-nyxveil-management.rules"))
	if strings.Contains(rule, "power-off") {
		t.Fatal("polkit rule must not authorize power-off")
	}
	if !strings.Contains(rule, `subject.user !== "nyxveil"`) {
		t.Fatal("polkit rule must bind nyxveil user")
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
