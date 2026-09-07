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
	CLIVersion             string `json:"cli_version"`
	InstalledServerVersion string `json:"installed_server_version"`
	RunningServerVersion   string `json:"running_server_version"`
	ReleaseVersion         string `json:"release_version"`
	CoreVersion            string `json:"core_version"`
	Protocol               string `json:"protocol"`
}

const versionUnknown = "unknown"

func collectVersionReport() VersionReport {
	return VersionReport{
		CLIVersion:             version.CLIVersion,
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

// assertVersionsMatchTarget verifies installed+running+cli+release match the update target.
func assertVersionsMatchTarget(want string) error {
	want = strings.TrimSpace(want)
	if want == "" {
		return fmt.Errorf("empty target version")
	}
	r := collectVersionReport()
	var errs []string
	if r.CLIVersion != want {
		errs = append(errs, fmt.Sprintf("cli_version=%s", r.CLIVersion))
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
