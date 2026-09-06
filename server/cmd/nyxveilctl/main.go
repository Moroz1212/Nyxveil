package main

import (
	"context"
	"encoding/json"
	"flag"
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

	"github.com/nyxveil/server/internal/configure"
	"github.com/nyxveil/server/internal/filemeta"
	"github.com/nyxveil/server/internal/health"
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
	case "bootstrap-cli":
		err = runBootstrapCLI(args)
	case "config":
		err = showConfig(args)
	case "configure":
		err = runConfigure(args)
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
  nyxveilctl bootstrap-cli --version 1.0.5 [--then-update]
  nyxveilctl config [path]              # dump server.json
  nyxveilctl configure [flags]          # existing-node reconfigure (transactional)
  nyxveilctl configure --status         # TLS/public_host/dns/SPKI summary
  nyxveilctl version
  nyxveilctl uninstall

bootstrap-cli (legacy ≤1.0.4 updaters):
  Replace ONLY /usr/local/sbin/nyxveilctl from a signed release.
  Does not stop the server or touch TLS/config/identity.
  Prefer scripts/bootstrap-cli-update.sh on nodes that still run ctl 1.0.3.

configure flags (existing registered node only — preserves node_id / node.key):
  --public-host HOST
  --dns-servers IP,IP
  --control-plane-url URL   # https://cp.example:18443 (SystemTrust validated)
  --tls-domain FQDN
  --tls-email EMAIL
  --tls-cert PATH --tls-key PATH [--tls-replace]
  --expect-public-ip IP   # required for ACME DNS check when public_host is already an FQDN
  --check | --dry-run     # validate only; no changes
  --status                # print configure/TLS/CP status JSON
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

func runBootstrapCLI(args []string) error {
	fs := flag.NewFlagSet("bootstrap-cli", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	versionFlag := fs.String("version", "1.0.11", "target server-vVERSION release")
	manifestURL := fs.String("manifest-url", "", "override signed manifest URL")
	ctlPath := fs.String("ctl-path", "", "nyxveilctl install path (default beside nyxveil-server)")
	thenUpdate := fs.Bool("then-update", false, "after CLI replace, run full nyxveilctl update")
	if err := fs.Parse(args); err != nil {
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
	dest := *ctlPath
	if dest == "" {
		dest = filepath.Join(filepath.Dir(server), "nyxveilctl")
	}
	url := strings.TrimSpace(*manifestURL)
	if url == "" {
		url = updater.ManifestURLForVersion(*versionFlag)
	}

	fmt.Printf("bootstrap-cli: verifying signed manifest %s (CLI-only; server untouched)\n", url)
	res, err := updater.BootstrapCLI(updater.BootstrapCLIOpts{
		ManifestURL: url,
		WantVersion: *versionFlag,
		CtlPath:     dest,
	})
	if err != nil {
		return err
	}
	fmt.Printf("bootstrap-cli: installed nyxveilctl %s sha256=%s (replaced=%v)\n", res.Version, res.NewSHA, res.Replaced)
	fmt.Println("bootstrap-cli: nyxveil-server / TLS / server.json / identity were NOT modified")

	if *thenUpdate {
		fmt.Println("bootstrap-cli: launching fixed updater…")
		return runUpdate([]string{url})
	}
	fmt.Printf("next: sudo %s update\n", dest)
	return nil
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

	preBaseline, preTLS, err := capturePreUpdateBaseline(15)
	if err != nil {
		return fmt.Errorf("updater: capture pre-update baseline: %w", err)
	}
	fmt.Printf("pre-update baseline: running=%v accepting=%v bridge_ok=%v tls_ok=%v quic_ok=%v tun_ready=%v cp_connected=%v healthy=%v identity_present=%v version_blocked=%v dataplane_ok=%v\n",
		preBaseline.Running, preBaseline.Accepting, preBaseline.BridgeOK, preBaseline.TLSOK, preBaseline.QUICOK,
		preBaseline.TUNReady, preBaseline.CPConnected, preBaseline.Healthy, preBaseline.IdentityPresent,
		preBaseline.VersionBlocked, preBaseline.DataplaneOK)

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
	u.StateDir = paths.StateDir
	u.EnforceOwnership = filemeta.EnforceRuntimeTLS

	health := func() bool {
		if runtime.GOOS == "windows" {
			return true
		}
		_ = restartUnit("nyxveil-server")
		res, ok := verifyPostUpdateHealth(preBaseline, 45)
		if ok {
			fmt.Printf("update_success=%v dataplane_healthy=%v management_plane_connected=%v preexisting_management_degradation=%v\n",
				res.UpdateSuccess, res.DataplaneHealthy, res.ManagementPlaneConnected, res.PreexistingManagementDegradation)
			if res.Reason != "" {
				fmt.Printf("update note: %s\n", res.Reason)
			}
		}
		return ok
	}

	if err := u.Apply(m, health); err != nil {
		// After Apply rolls binaries + TLS metadata back, restart previous and
		// evaluate against the PRE-UPDATE baseline (not absolute global healthy).
		if runtime.GOOS != "windows" && isUpdateRollback(err) {
			fmt.Println("update failed; restoring previous binaries/TLS ownership and restarting service…")
			_ = filemeta.EnforceRuntimeTLS(paths.StateDir)
			_ = restartUnit("nyxveil-server")
			rb, ok := verifyRollbackHealth(preBaseline, 45)
			postTLS, _ := filemeta.CaptureTLSOwnership(paths.StateDir)
			tlsMsg := filemeta.TLSOwnershipChanged(preTLS, postTLS)
			if tlsMsg == "" {
				tlsMsg = filemeta.VerifyRuntimeTLSContract(paths.StateDir)
			}
			if ok {
				fmt.Printf("rollback_complete=%v baseline_restored=%v\n", rb.Complete, rb.BaselineRestored)
				if tlsMsg != "" {
					fmt.Printf("warning: TLS ownership verification failed: %s\n", tlsMsg)
				}
				return fmt.Errorf("%w; previous version restarted; baseline restored", err)
			}
			msg := fmt.Errorf("%w; ROLLBACK INCOMPLETE: %s", err, rb.Reason)
			if tlsMsg != "" {
				msg = fmt.Errorf("%w; TLS ownership: %s", msg, tlsMsg)
			}
			return msg
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

// ctlStatusJSON runs nyxveilctl status and returns stdout (overridable in tests).
var ctlStatusJSON = func() ([]byte, error) {
	return exec.Command("nyxveilctl", "status").CombinedOutput()
}

// ctlHealthJSON retained for configure/tests that still probe /health.
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

// controlSocketReady reports whether the node control socket exists (overridable in tests).
var controlSocketReady = func() bool {
	st, err := os.Stat(paths.ControlSocket())
	return err == nil && !st.IsDir()
}

func capturePreUpdateBaseline(seconds int) (health.Baseline, filemeta.TLSOwnershipSnapshot, error) {
	var zero health.Baseline
	var tls filemeta.TLSOwnershipSnapshot
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	var last error
	for time.Now().Before(deadline) {
		if runtime.GOOS != "windows" {
			if !serviceActive("nyxveil-server") || !controlSocketReady() {
				last = fmt.Errorf("service/socket not ready")
				time.Sleep(time.Second)
				continue
			}
		}
		out, err := ctlStatusJSON()
		if err != nil {
			last = err
			time.Sleep(time.Second)
			continue
		}
		st, err := health.ParseStatusJSON(out)
		if err != nil {
			last = err
			time.Sleep(time.Second)
			continue
		}
		tls, _ = filemeta.CaptureTLSOwnership(paths.StateDir)
		return health.CaptureBaseline(st), tls, nil
	}
	if last == nil {
		last = fmt.Errorf("timeout")
	}
	return zero, tls, last
}

func verifyPostUpdateHealth(pre health.Baseline, seconds int) (health.UpdateResult, bool) {
	const needStable = 3
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	stable := 0
	var last health.UpdateResult
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		if !serviceActive("nyxveil-server") || !controlSocketReady() {
			stable = 0
			continue
		}
		out, err := ctlStatusJSON()
		if err != nil {
			stable = 0
			continue
		}
		st, err := health.ParseStatusJSON(out)
		if err != nil {
			stable = 0
			continue
		}
		last = health.EvaluatePostUpdate(pre, st)
		if !last.OK {
			stable = 0
			continue
		}
		stable++
		if stable >= needStable {
			return last, true
		}
	}
	return last, false
}

func verifyRollbackHealth(pre health.Baseline, seconds int) (health.RollbackResult, bool) {
	const needStable = 3
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	stable := 0
	var last health.RollbackResult
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		if !serviceActive("nyxveil-server") || !controlSocketReady() {
			stable = 0
			continue
		}
		out, err := ctlStatusJSON()
		if err != nil {
			stable = 0
			continue
		}
		st, err := health.ParseStatusJSON(out)
		if err != nil {
			stable = 0
			continue
		}
		last = health.EvaluateRollbackSuccess(pre, st)
		if !last.Complete {
			stable = 0
			continue
		}
		stable++
		if stable >= needStable {
			return last, true
		}
	}
	if last.Reason == "" {
		last.Reason = "could not restore baseline within timeout"
		last.Incomplete = true
	}
	return last, false
}

// verifyServiceHealth is the strict global-healthy gate (legacy). Prefer baseline-aware
// verifyPostUpdateHealth / verifyRollbackHealth for updates.
func verifyServiceHealth(seconds int) bool {
	pre := health.Baseline{CPConnected: true, Healthy: true, DataplaneOK: true, Running: true, Accepting: true, BridgeOK: true, TLSOK: true, QUICOK: true, TUNReady: true, IdentityPresent: true}
	_, ok := verifyPostUpdateHealth(pre, seconds)
	return ok
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

func runConfigure(args []string) error {
	fs := flag.NewFlagSet("configure", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		publicHost   = fs.String("public-host", "", "set public_host (FQDN after TLS cutover)")
		dnsServers   = fs.String("dns-servers", "", "comma-separated IPv4 resolvers")
		cpURL        = fs.String("control-plane-url", "", "set control_plane_url (https://host:port); SystemTrust validated")
		tlsDomain    = fs.String("tls-domain", "", "ACME FQDN (Let's Encrypt HTTP-01)")
		tlsEmail     = fs.String("tls-email", "", "ACME contact email")
		tlsCert      = fs.String("tls-cert", "", "operator certificate PEM path")
		tlsKey       = fs.String("tls-key", "", "operator private key PEM path")
		tlsReplace   = fs.Bool("tls-replace", false, "overwrite existing TLS material")
		expectIP     = fs.String("expect-public-ip", "", "expected public IP for ACME DNS check")
		configPath   = fs.String("config", paths.ServerConfig(), "path to server.json")
		dryRun       = fs.Bool("dry-run", false, "validate only; do not change the node")
		check        = fs.Bool("check", false, "alias for --dry-run")
		showStatus   = fs.Bool("status", false, "print configure/TLS status JSON and exit")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showStatus {
		v, err := configure.LoadStatus(*configPath)
		if err != nil {
			return err
		}
		return configure.PrintStatusJSON(v)
	}
	opts := configure.Options{
		ConfigPath:      *configPath,
		PublicHost:      *publicHost,
		DNSServers:      *dnsServers,
		ControlPlaneURL: *cpURL,
		TLSDomain:       *tlsDomain,
		TLSEmail:        *tlsEmail,
		TLSCert:         *tlsCert,
		TLSKey:          *tlsKey,
		TLSReplace:      *tlsReplace,
		DryRun:          *dryRun || *check,
		PublicIPHint:    *expectIP,
	}
	if opts.PublicHost == "" && opts.DNSServers == "" && opts.TLSDomain == "" && opts.TLSCert == "" && opts.ControlPlaneURL == "" {
		return fmt.Errorf("configure: specify at least one of --public-host, --dns-servers, --control-plane-url, --tls-domain, or --tls-cert/--tls-key (or --status)")
	}
	res, err := configure.Apply(context.Background(), opts)
	if res != nil {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
	}
	return err
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
