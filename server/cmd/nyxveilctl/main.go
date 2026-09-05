package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/nyxveil/server/internal/localconfig"
	"github.com/nyxveil/server/internal/paths"
	"github.com/nyxveil/server/internal/updater"
	"github.com/nyxveil/server/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	var err error
	switch cmd {
	case "version":
		fmt.Printf("nyxveilctl %s (server %s, core %s, %s)\n",
			version.ServerVersion, version.ServerVersion, version.CoreVersion, version.ProtocolVersion)
	case "status":
		err = printJSON("/status")
	case "health":
		err = cmdHealth()
	case "start":
		err = systemctl("start", "nyxveil-server")
	case "stop":
		err = systemctl("stop", "nyxveil-server")
	case "restart":
		err = systemctl("restart", "nyxveil-server")
	case "logs":
		err = journalctl(args)
	case "update":
		err = runUpdate(args)
	case "config":
		err = showConfig(args)
	case "uninstall":
		err = uninstall()
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `nyxveilctl — Nyxveil VPN node control

Usage:
  nyxveilctl status
  nyxveilctl health
  nyxveilctl start|stop|restart
  nyxveilctl logs [-f]
  nyxveilctl update [manifest-url]
  nyxveilctl config [path]
  nyxveilctl version
  nyxveilctl uninstall
`)
}

func controlBase() string {
	if v := os.Getenv("NYXVEIL_CONTROL_HTTP"); v != "" {
		return strings.TrimRight(v, "/")
	}
	if runtime.GOOS == "windows" {
		return "http://127.0.0.1:9797"
	}
	return ""
}

func fetchControl(path string) ([]byte, error) {
	base := controlBase()
	if base != "" {
		resp, err := http.Get(base + path)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	}
	return unixGETBytes(paths.ControlSocket(), path)
}

func printJSON(path string) error {
	b, err := fetchControl(path)
	if err != nil {
		return err
	}
	return printPretty(b)
}

func cmdHealth() error {
	b, err := fetchControl("/health")
	if err != nil {
		return err
	}
	if err := printPretty(b); err != nil {
		return err
	}
	var wrap struct {
		Healthy *bool `json:"healthy"`
	}
	if err := json.Unmarshal(b, &wrap); err != nil || wrap.Healthy == nil || !*wrap.Healthy {
		os.Exit(1)
	}
	return nil
}

func printPretty(b []byte) error {
	var pretty any
	if json.Unmarshal(b, &pretty) == nil {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(pretty)
	}
	fmt.Println(string(b))
	return nil
}

func unixGETBytes(sock, path string) ([]byte, error) {
	client := &http.Client{
		Transport: &http.Transport{
			Dial: func(_, _ string) (net.Conn, error) {
				return net.DialTimeout("unix", sock, 2*time.Second)
			},
		},
		Timeout: 5 * time.Second,
	}
	resp, err := client.Get("http://unix" + path)
	if err != nil {
		return nil, fmt.Errorf("control socket %s: %w", sock, err)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func systemctl(action, unit string) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("%s not supported on windows via systemctl", action)
	}
	cmd := exec.Command("systemctl", action, unit)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func journalctl(args []string) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("logs not supported on windows")
	}
	a := []string{"-u", "nyxveil-server"}
	a = append(a, args...)
	cmd := exec.Command("journalctl", a...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func resolveManifestURL(args []string) (string, error) {
	if len(args) >= 1 && strings.TrimSpace(args[0]) != "" {
		return strings.TrimSpace(args[0]), nil
	}
	cfgPath := paths.ServerConfig()
	if cfg, err := localconfig.Load(cfgPath); err == nil && strings.TrimSpace(cfg.UpdateURL) != "" {
		return strings.TrimSpace(cfg.UpdateURL), nil
	}
	return updater.DefaultManifestURL(), nil
}

func runUpdate(args []string) error {
	manifestURL, err := resolveManifestURL(args)
	if err != nil {
		return err
	}
	bin, err := os.Executable()
	if err != nil {
		bin = paths.BinaryPath()
	}
	server := paths.BinaryPath()
	if _, err := os.Stat(server); err != nil {
		server = filepath.Join(filepath.Dir(bin), "nyxveil-server")
	}
	ctlPath := filepath.Join(filepath.Dir(server), "nyxveilctl")
	ctlPrev := filepath.Join(paths.StateDir, "nyxveilctl.prev")

	fmt.Printf("fetching update manifest %s\n", manifestURL)
	resp, err := http.Get(manifestURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	m, err := updater.ParseManifest(b, updater.UpdatePublicKey)
	if err != nil {
		return err
	}
	u := updater.New(server, paths.PreviousBinary(), paths.RollbackMarker())
	u.ExtraBinaries = map[string]string{"nyxveilctl": ctlPath}
	u.ExtraPrev = map[string]string{"nyxveilctl": ctlPrev}

	health := func() bool {
		if runtime.GOOS == "windows" {
			return true
		}
		_ = restartUnit("nyxveil-server")
		return verifyServiceHealth(30)
	}

	if err := u.Apply(m, health); err != nil {
		// After Apply rolls binaries back, the running process may still be the
		// failed new version — always restart the restored previous binaries.
		if runtime.GOOS != "windows" && isUpdateRollback(err) {
			fmt.Println("update failed; restoring previous binaries and restarting service…")
			_ = restartUnit("nyxveil-server")
			if verifyServiceHealth(30) {
				return fmt.Errorf("%w; previous version restarted and healthy", err)
			}
			return fmt.Errorf("%w; previous version restarted but still unhealthy", err)
		}
		return err
	}
	fmt.Printf("updated to %s\n", m.Version)
	return nil
}

// restartUnit runs systemctl restart (overridable in tests).
var restartUnit = func(unit string) error {
	return exec.Command("systemctl", "restart", unit).Run()
}

// serviceActive reports whether the unit is active (overridable in tests).
var serviceActive = func(unit string) bool {
	return exec.Command("systemctl", "is-active", "--quiet", unit).Run() == nil
}

// ctlHealthJSON runs nyxveilctl health and returns stdout (overridable in tests).
var ctlHealthJSON = func() ([]byte, error) {
	return exec.Command("nyxveilctl", "health").CombinedOutput()
}

func isUpdateRollback(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "rolled back") || strings.Contains(msg, "health check failed")
}

func verifyServiceHealth(seconds int) bool {
	for i := 0; i < seconds; i++ {
		time.Sleep(time.Second)
		if !serviceActive("nyxveil-server") {
			continue
		}
		out, err := ctlHealthJSON()
		if err != nil {
			continue
		}
		var wrap struct {
			Healthy *bool `json:"healthy"`
		}
		if json.Unmarshal(out, &wrap) == nil && wrap.Healthy != nil && *wrap.Healthy {
			return true
		}
	}
	return false
}

func showConfig(args []string) error {
	path := paths.ServerConfig()
	if len(args) > 0 {
		path = args[0]
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func uninstall() error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("uninstall: use Windows installer / remove service manually")
	}
	_ = exec.Command("systemctl", "stop", "nyxveil-server").Run()
	_ = exec.Command("systemctl", "disable", "nyxveil-server").Run()
	_ = os.Remove(paths.ServiceUnit)
	fmt.Println("service stopped; remove /etc/nyxveil and /var/lib/nyxveil manually if desired")
	return nil
}
