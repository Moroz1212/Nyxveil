package main

import (
	"bytes"
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
		err = runVersion(args)
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
	case "update-resume":
		// Internal self-update handoff — requires a valid on-disk transaction journal.
		err = runUpdateResume(args)
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
  nyxveilctl update [manifest-url]      # signed update + installed production gate (one command)
  nyxveilctl bootstrap-cli --version 1.0.5 [--then-update]
  nyxveilctl config [path]              # dump server.json
  nyxveilctl configure [flags]          # existing-node reconfigure (transactional)
  nyxveilctl configure --status         # TLS/public_host/dns/SPKI summary
  nyxveilctl version [--json|--machine]
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
	// Prefer Control Plane-pinned target from durable update marker (remote UpdateNodeLatest).
	for _, dir := range []string{filepath.Dir(paths.CommandsState()), filepath.Dir(paths.NodeKey())} {
		if pinned, ok := readPinnedUpdateTarget(dir); ok {
			return updater.ManifestURLForVersion(pinned), nil
		}
	}
	cfgPath := paths.ServerConfig()
	if cfg, err := localconfig.Load(cfgPath); err == nil && strings.TrimSpace(cfg.UpdateURL) != "" {
		return strings.TrimSpace(cfg.UpdateURL), nil
	}
	return updater.DefaultManifestURL(), nil
}

func readPinnedUpdateTarget(stateDir string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(stateDir, "management", "update-command.json"))
	if err != nil {
		return "", false
	}
	var m struct {
		TargetVersion string `json:"target_version"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return "", false
	}
	v := strings.TrimSpace(strings.TrimPrefix(m.TargetVersion, "v"))
	if v == "" {
		return "", false
	}
	return v, true
}

func runBootstrapCLI(args []string) error {
	fs := flag.NewFlagSet("bootstrap-cli", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	versionFlag := fs.String("version", version.ServerVersion, "target server-vVERSION release")
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
		TestMode:    os.Getenv("NYXVEIL_TEST_MODE") == "1",
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

	reportUpdateCommandProgress("", "downloading", "Downloading release manifest and assets")
	fmt.Printf("fetching update manifest %s\n", manifestURL)
	localDir := strings.TrimSpace(os.Getenv("NYXVEIL_UPDATE_LOCAL_DIR"))
	var b []byte
	if localDir != "" {
		cleanDir, err := filepath.Abs(localDir)
		if err != nil {
			return err
		}
		cleanManifest, err := filepath.Abs(manifestURL)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(cleanDir, cleanManifest)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("updater: local manifest must be inside NYXVEIL_UPDATE_LOCAL_DIR")
		}
		b, err = os.ReadFile(cleanManifest)
		if err != nil {
			return err
		}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		if os.Getenv("NYXVEIL_TEST_MODE") != "1" {
			if err := updater.ValidateReleaseURL(manifestURL); err != nil {
				cancel()
				return err
			}
		}
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
		if err != nil {
			return err
		}
		client := &http.Client{Timeout: 60 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		b, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return err
		}
	}
	reportUpdateCommandProgress("", "verifying", "Verifying signed release manifest")
	m, err := updater.ParseManifest(b)
	if err != nil {
		return err
	}
	if localDir == "" && os.Getenv("NYXVEIL_TEST_MODE") != "1" {
		for _, asset := range m.Assets {
			if err := updater.ValidateReleaseURL(asset.URL); err != nil {
				return err
			}
		}
		if m.URL != "" {
			if err := updater.ValidateReleaseURL(m.URL); err != nil {
				return err
			}
		}
	}
	u := updater.New(server, paths.PreviousBinary(), paths.RollbackMarker())
	extraDest, extraPrev := paths.DefaultExtraInstallMaps()
	// Keep ctl beside the resolved server binary when not using the default layout.
	extraDest["nyxveilctl"] = ctlPath
	extraPrev["nyxveilctl"] = ctlPrev
	u.ExtraBinaries = extraDest
	u.ExtraPrev = extraPrev
	u.LocalDir = localDir
	u.StateDir = paths.StateDir
	u.EnforceOwnership = filemeta.MigrateACMEState
	reportUpdateCommandProgress("", "installing", "Installing verified release assets")

	health := func() bool {
		tx := &updateTransaction{
			ID:                fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid()),
			TargetVersion:     m.Version,
			ManifestURL:       manifestURL,
			LocalDir:          localDir,
			ServerPath:        server,
			CtlPath:           ctlPath,
			CtlPrev:           ctlPrev,
			PreBaseline:       preBaseline,
			PreTLS:            preTLS,
			Phase:             txPhaseAssetsInstalled,
			OwnerPID:          os.Getpid(),
			CreatedAt:         time.Now().UTC(),
			ProcessCLIAtStart: version.CLIVersion,
			PreviousVersion:   version.ServerVersion,
		}
		// Best-effort early capture when this ctl already knows the CP marker
		// (1.1.14+ parent). Legacy 1.1.9 parents omit this; update-resume re-captures.
		if err := captureCommandCorrelationFromMarker(tx); err != nil {
			fmt.Printf("update_success=false reason=command_correlation detail=%v\n", err)
			return false
		}
		if err := writeUpdateTransaction(tx); err != nil {
			fmt.Printf("update_success=false reason=transaction_journal detail=%v\n", err)
			return false
		}

		// Prefer handoff so post-check/gate run under the NEW installed ctl image.
		// Old process BuildVersion must never be treated as installed CLI version.
		if strings.TrimSpace(os.Getenv("NYXVEIL_SKIP_HANDOFF")) == "1" {
			return performPostUpdateVerification(tx)
		}
		return handoffPostCheckToNewCtl(tx)
	}

	return finishUpdate(m, u, health, preBaseline, preTLS)
}

func finishUpdate(m *updater.Manifest, u *updater.Updater, health updater.HealthCheck, preBaseline health.Baseline, preTLS filemeta.TLSOwnershipSnapshot) error {
	if err := applyUpdate(u, m, health); err != nil {
		// After Apply rolls binaries + TLS metadata back, restart previous and
		// evaluate against the PRE-UPDATE baseline (not absolute global healthy).
		if runtime.GOOS != "windows" && isUpdateRollback(err) {
			fmt.Println("update failed; restoring previous binaries/TLS ownership and restarting service…")
			if e := filemeta.EnforceRuntimeTLS(paths.StateDir); e != nil {
				return fmt.Errorf("%w; rollback TLS ownership failure: %v", err, e)
			}
			if e := verifyUpdateTLS(); e != nil {
				return fmt.Errorf("%w; rollback TLS failure: %v", err, e)
			}
			if e := restartUnit("nyxveil-server"); e != nil {
				return fmt.Errorf("%w; rollback restart failure: %v", err, e)
			}
			rb, ok := verifyRollbackHealth(preBaseline, 45)
			postTLS, _ := filemeta.CaptureTLSOwnership(paths.StateDir)
			tlsMsg := filemeta.TLSOwnershipChanged(preTLS, postTLS)
			if tlsMsg == "" {
				tlsMsg = filemeta.VerifyRuntimeTLSContract(paths.StateDir)
			}
			if ok && tlsMsg == "" {
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
	// Single operator command contract: update is not complete until the
	// installed production gate PASSes (unless explicitly skipped).
	return afterSuccessfulUpdate(m.Version)
}

// applyUpdate is the binary replace + health hook (overridable in tests).
var applyUpdate = func(u *updater.Updater, m *updater.Manifest, health updater.HealthCheck) error {
	return u.Apply(m, health)
}

// afterSuccessfulUpdate runs the installed production gate (overridable in tests).
var afterSuccessfulUpdate = defaultAfterSuccessfulUpdate

func defaultAfterSuccessfulUpdate(ver string) error {
	if strings.TrimSpace(os.Getenv("NYXVEIL_SKIP_GATE")) == "1" {
		fmt.Println("NYXVEIL_SKIP_GATE=1 - skipping production gate after update")
		return nil
	}
	fmt.Printf("running installed production gate after update to %s\n", ver)
	return execInstalledProductionGate()
}

func productionGatePath() string {
	if p := strings.TrimSpace(os.Getenv("NYXVEIL_PRODUCTION_GATE")); p != "" {
		return p
	}
	return paths.ProductionGate()
}

func execInstalledProductionGate() error {
	gate := productionGatePath()
	st, err := os.Stat(gate)
	if err != nil {
		return fmt.Errorf("production gate missing after update (%s): %w", gate, err)
	}
	if st.IsDir() {
		return fmt.Errorf("production gate path is a directory: %s", gate)
	}
	mode := "updater"
	bash, err := exec.LookPath("bash")
	if err != nil {
		return fmt.Errorf("bash required to run production gate: %w", err)
	}
	raw, err := os.ReadFile(gate)
	if err != nil {
		return fmt.Errorf("read production gate: %w", err)
	}
	// Windows checkouts may ship CRLF; shebang + \r yields "No such file or directory".
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	raw = bytes.ReplaceAll(raw, []byte("\r"), []byte("\n"))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bash, "-s", "--", gate)
	cmd.Stdin = bytes.NewReader(raw)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	env := append([]string{}, os.Environ()...)
	env = append(env, "GATE_MODE="+mode)
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		// Gate already printed RESULT=FAIL failed_gate=... diagnostic_bundle=...
		return fmt.Errorf("production gate failed after update: %w", err)
	}
	return nil
}

// restartUnit runs systemctl restart (overridable in tests).
var restartUnit = func(unit string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "systemctl", "restart", unit).Run()
}

// unitMainPID returns systemd MainPID for unit, or 0 if unavailable.
var unitMainPID = func(unit string) int {
	out, err := exec.Command("systemctl", "show", unit, "-p", "MainPID", "--value").CombinedOutput()
	if err != nil {
		return 0
	}
	var pid int
	_, _ = fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &pid)
	return pid
}

// serviceActive reports whether the unit is active (overridable in tests).
var serviceActive = func(unit string) bool {
	return exec.Command("systemctl", "is-active", "--quiet", unit).Run() == nil
}

// ctlStatusJSON runs nyxveilctl status and returns stdout (overridable in tests).
var ctlStatusJSON = func() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "nyxveilctl", "status").CombinedOutput()
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
	if last.Reason == "" {
		last.Reason = "service/status unavailable within health timeout"
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
		publicHost = fs.String("public-host", "", "set public_host (FQDN after TLS cutover)")
		dnsServers = fs.String("dns-servers", "", "comma-separated IPv4 resolvers")
		cpURL      = fs.String("control-plane-url", "", "set control_plane_url (https://host:port); SystemTrust validated")
		tlsDomain  = fs.String("tls-domain", "", "ACME FQDN (Let's Encrypt HTTP-01)")
		tlsEmail   = fs.String("tls-email", "", "ACME contact email")
		tlsCert    = fs.String("tls-cert", "", "operator certificate PEM path")
		tlsKey     = fs.String("tls-key", "", "operator private key PEM path")
		tlsReplace = fs.Bool("tls-replace", false, "overwrite existing TLS material")
		expectIP   = fs.String("expect-public-ip", "", "expected public IP for ACME DNS check")
		configPath = fs.String("config", paths.ServerConfig(), "path to server.json")
		dryRun     = fs.Bool("dry-run", false, "validate only; do not change the node")
		check      = fs.Bool("check", false, "alias for --dry-run")
		showStatus = fs.Bool("status", false, "print configure/TLS status JSON and exit")
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
