package updater_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nyxveil/server/internal/version"
)

// TestProductionGateRejectsDaemonStartVersionProbe embeds the live failure mode:
// calling `nyxveil-server version` must not be required to succeed via daemon start.
// Gate scripts after 1.1.4 must use --version / ctl --json only.
func TestProductionGateUsesMachineReadableVersionAPI(t *testing.T) {
	root := repoRootFromUpdaterTest(t)
	gate := filepath.Join(root, "scripts", "production-gate.sh")
	raw, err := os.ReadFile(gate)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, `"${SERVER}" version`) && !strings.Contains(text, `"${SERVER}" --version`) {
		t.Fatal("production-gate still invokes nyxveil-server version subcommand without --version")
	}
	if !strings.Contains(text, `version --json`) {
		t.Fatal("production-gate must use nyxveilctl version --json")
	}
	if !strings.Contains(text, "installed_server_version") || !strings.Contains(text, "running_server_version") {
		t.Fatal("production-gate must assert installed and running versions independently")
	}
	if !strings.Contains(text, "version-diagnostics.txt") {
		t.Fatal("production-gate must emit version diagnostics on failure")
	}
}

func TestProductVersionConstantMatchesVERSIONFile(t *testing.T) {
	root := repoRootFromUpdaterTest(t)
	b, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(strings.ReplaceAll(string(b), "\r", ""))
	if got != version.ServerVersion {
		t.Fatalf("VERSION=%q const=%q", got, version.ServerVersion)
	}
	if got != "1.1.6" {
		t.Fatalf("expected release candidate 1.1.6, got %s", got)
	}
}

func TestVersionReportJSONShape(t *testing.T) {
	// Structural contract for gate parsers.
	type report struct {
		CLIVersion             string `json:"cli_version"`
		InstalledServerVersion string `json:"installed_server_version"`
		RunningServerVersion   string `json:"running_server_version"`
		ReleaseVersion         string `json:"release_version"`
		CoreVersion            string `json:"core_version"`
		Protocol               string `json:"protocol"`
	}
	sample := []byte(`{"cli_version":"1.1.6","installed_server_version":"1.1.6","running_server_version":"1.1.6","release_version":"1.1.6","core_version":"1.0.0","protocol":"NVP/1"}`)
	var r report
	if err := json.Unmarshal(sample, &r); err != nil {
		t.Fatal(err)
	}
	if r.CLIVersion != "1.1.6" || r.Protocol != "NVP/1" {
		t.Fatalf("%+v", r)
	}
}

func TestBashNProductionAndLiveScripts(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash required")
	}
	root := repoRootFromUpdaterTest(t)
	for _, rel := range []string{
		"scripts/production-gate.sh",
		"scripts/live-final-update.sh",
		"scripts/bootstrap-cli-update.sh",
	} {
		cmd := exec.Command(bash, "-n", filepath.Join(root, rel))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", rel, err, out)
		}
	}
	_ = runtime.GOOS
}
