package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nyxveil/server/internal/paths"
	"github.com/nyxveil/server/internal/version"
)

// VersionReport is the authoritative machine-readable version model.
// Fields are independent — UNKNOWN is never substituted from another source.
type VersionReport struct {
	CLIVersion             string `json:"cli_version"`              // current process (may be old during self-update)
	InstalledCLIVersion    string `json:"installed_cli_version"`    // from on-disk nyxveilctl binary (new process)
	InstalledServerVersion string `json:"installed_server_version"` // from on-disk nyxveil-server binary
	RunningServerVersion   string `json:"running_server_version"`   // from daemon control socket
	ReleaseVersion         string `json:"release_version"`          // share VERSION
	CoreVersion            string `json:"core_version"`
	Protocol               string `json:"protocol"`
}

const versionUnknown = "unknown"

func collectVersionReport() VersionReport {
	return VersionReport{
		CLIVersion:             version.CLIVersion,
		InstalledCLIVersion:    installedCLIVersion(),
		InstalledServerVersion: installedServerVersion(),
		RunningServerVersion:   runningServerVersion(),
		ReleaseVersion:         releaseVersion(),
		CoreVersion:            version.CoreVersion,
		Protocol:               version.ProtocolVersion,
	}
}

func printVersion(w io.Writer) {
	printVersionReport(w, collectVersionReport(), false)
}

func printVersionReport(w io.Writer, r VersionReport, asJSON bool) {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(r)
		return
	}
	fmt.Fprintf(w, "cli_version=%s\n", r.CLIVersion)
	fmt.Fprintf(w, "installed_cli_version=%s\n", r.InstalledCLIVersion)
	fmt.Fprintf(w, "installed_server_version=%s\n", r.InstalledServerVersion)
	fmt.Fprintf(w, "running_server_version=%s\n", r.RunningServerVersion)
	fmt.Fprintf(w, "release_version=%s\n", r.ReleaseVersion)
	fmt.Fprintf(w, "core_version=%s\n", r.CoreVersion)
	fmt.Fprintf(w, "protocol=%s\n", r.Protocol)
}

func runVersion(args []string) error {
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		case "--machine":
			// key=value machine format (default text layout).
		case "-h", "--help":
			fmt.Fprintln(os.Stderr, "usage: nyxveilctl version [--json|--machine]")
			return nil
		default:
			return fmt.Errorf("version: unknown argument %q", a)
		}
	}
	if os.Getenv("NYXVEIL_VERSION_PROBE") == "1" {
		// A binary provenance probe must not recursively probe other binaries or
		// wait for the daemon control socket during its restart.
		printVersionReport(os.Stdout, VersionReport{CLIVersion: version.CLIVersion}, asJSON)
		return nil
	}
	printVersionReport(os.Stdout, collectVersionReport(), asJSON)
	return nil
}

func releaseVersion() string {
	candidates := []string{
		strings.TrimSpace(os.Getenv("NYXVEIL_SHARE_VERSION")),
		paths.ShareVersion(),
	}
	if share := strings.TrimSpace(os.Getenv("NYXVEIL_SHARE_DIR")); share != "" {
		candidates = append(candidates, filepath.Join(share, "VERSION"))
	}
	seen := map[string]bool{}
	for _, p := range candidates {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		v := strings.TrimSpace(strings.ReplaceAll(string(b), "\r", ""))
		if v != "" {
			return v
		}
	}
	return versionUnknown
}

func installedCLIVersion() string {
	candidates := []string{
		strings.TrimSpace(os.Getenv("NYXVEIL_CTL_BINARY")),
		filepath.Join(paths.BinDir, "nyxveilctl"),
		"/usr/local/bin/nyxveilctl",
	}
	// Avoid recursive probe forks when this process is itself a version probe.
	if os.Getenv("NYXVEIL_VERSION_PROBE") == "1" {
		return version.CLIVersion
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, exe)
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "nyxveilctl"))
	}
	seen := make(map[string]bool)
	for _, candidate := range candidates {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		cmd := exec.CommandContext(ctx, candidate, "version", "--json")
		cmd.Env = append(os.Environ(), "NYXVEIL_VERSION_PROBE=1")
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			continue
		}
		var wrap struct {
			CLIVersion          string `json:"cli_version"`
			InstalledCLIVersion string `json:"installed_cli_version"`
		}
		if json.Unmarshal(out, &wrap) == nil {
			// When probing the installed binary as a NEW process, its cli_version
			// IS the installed CLI version (compile-time of that file).
			if v := strings.TrimSpace(wrap.CLIVersion); v != "" && v != versionUnknown {
				return v
			}
			if v := strings.TrimSpace(wrap.InstalledCLIVersion); v != "" && v != versionUnknown {
				return v
			}
		}
	}
	return versionUnknown
}

func installedServerVersion() string {
	candidates := []string{
		strings.TrimSpace(os.Getenv("NYXVEIL_SERVER_BINARY")),
		paths.BinaryPath(),
		"/usr/local/bin/nyxveil-server",
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "nyxveil-server"))
	}
	seen := make(map[string]bool)
	for _, candidate := range candidates {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		// Prefer --version (historical), then subcommand "version" (1.1.4+).
		for _, args := range [][]string{{"--version"}, {"version"}, {"version", "--json"}} {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			out, err := exec.CommandContext(ctx, candidate, args...).CombinedOutput()
			cancel()
			if err != nil {
				continue
			}
			if got := parseServerVersion(out); got != "" {
				return got
			}
		}
	}
	return versionUnknown
}

func runningServerVersion() string {
	if b, err := fetchControl("/status"); err == nil {
		var st struct {
			Running       bool   `json:"running"`
			ServerVersion string `json:"server_version"`
		}
		if json.Unmarshal(b, &st) == nil {
			if st.Running && strings.TrimSpace(st.ServerVersion) != "" {
				return strings.TrimSpace(st.ServerVersion)
			}
			return versionUnknown
		}
	}
	if b, err := fetchControl("/version"); err == nil {
		if got := parseServerVersion(b); got != "" {
			return got
		}
	}
	return versionUnknown
}

func parseServerVersion(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, "{") {
		var v struct {
			ServerVersion string `json:"server_version"`
			Version       string `json:"version"`
		}
		if json.Unmarshal(raw, &v) == nil {
			if v.ServerVersion != "" {
				return strings.TrimSpace(v.ServerVersion)
			}
			return strings.TrimSpace(v.Version)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "server_version=") {
			return strings.TrimSpace(strings.TrimPrefix(line, "server_version="))
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nyxveil-server" {
			return fields[1]
		}
	}
	return ""
}

// assertVersionsMatchTarget verifies installed+running+release match the update target.
// current_process cli_version (version.CLIVersion) is intentionally NOT required to
// match after a self-update — the old process image remains until handoff/re-exec.
func assertVersionsMatchTarget(want string) error {
	want = strings.TrimSpace(want)
	if want == "" {
		return fmt.Errorf("empty target version")
	}
	r := collectVersionReport()
	var errs []string
	if r.InstalledCLIVersion != want {
		errs = append(errs, fmt.Sprintf("installed_cli_version=%s", r.InstalledCLIVersion))
	}
	if r.InstalledServerVersion != want {
		errs = append(errs, fmt.Sprintf("installed_server_version=%s", r.InstalledServerVersion))
	}
	if r.RunningServerVersion != want {
		errs = append(errs, fmt.Sprintf("running_server_version=%s", r.RunningServerVersion))
	}
	if r.ReleaseVersion != want {
		errs = append(errs, fmt.Sprintf("release_version=%s", r.ReleaseVersion))
	}
	if r.CoreVersion != version.CoreVersion {
		errs = append(errs, fmt.Sprintf("core_version=%s", r.CoreVersion))
	}
	if r.Protocol != version.ProtocolVersion {
		errs = append(errs, fmt.Sprintf("protocol=%s", r.Protocol))
	}
	if len(errs) > 0 {
		return fmt.Errorf("version mismatch want=%s (%s)", want, strings.Join(errs, ", "))
	}
	return nil
}
