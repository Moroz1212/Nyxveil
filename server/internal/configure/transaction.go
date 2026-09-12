package configure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nyxveil/server/internal/filemeta"
	"github.com/nyxveil/server/internal/localconfig"
	"github.com/nyxveil/server/internal/nodetls"
	"github.com/nyxveil/server/internal/paths"
)

// Result summarizes a configure run (never includes private keys).
type Result struct {
	DryRun     bool     `json:"dry_run"`
	NodeID     string   `json:"node_id"`
	LocationID string   `json:"location_id"`
	PublicHost string   `json:"public_host"`
	DNSServers []string `json:"dns_servers"`

	ControlPlaneURL     string `json:"control_plane_url,omitempty"`
	PrevControlPlaneURL string `json:"previous_control_plane_url,omitempty"`
	CPURLChanged        bool   `json:"control_plane_url_changed,omitempty"`
	CPConnected         bool   `json:"cp_connected,omitempty"`
	CatalogVerified     bool   `json:"catalog_verified,omitempty"`

	PrevSPKI    string `json:"previous_spki_sha256,omitempty"`
	NewSPKI     string `json:"new_spki_sha256,omitempty"`
	SPKIChanged bool   `json:"spki_changed"`
	Registered  bool   `json:"control_plane_reregistered"`
	RolledBack  bool   `json:"rolled_back,omitempty"`

	// Rollback nuance when old CP URL is no longer TLS-valid after CP hostname migration.
	RollbackConfigComplete           bool `json:"rollback_config_complete,omitempty"`
	RollbackEndpointHealthImpossible bool `json:"rollback_endpoint_health_impossible,omitempty"`

	Message string `json:"message,omitempty"`
}

// Apply runs dry-run checks or a transactional reconfigure of an existing node.
//
// ACME / TLS transition order (existing-node):
//  1. validate flags + load identity
//  2. DNS must point at expected public IP (fail before any mutation)
//  3. snapshot working config/TLS/firewall
//  4. open TCP/80 in Nyxveil nftables (idempotent) while OLD TLS stays live
//  5. issue ACME into staging paths only (tls.next.*)
//  6. validate STAGED cert for target FQDN (never against live self-signed)
//  7. only then: stop → atomic commit live TLS + server.json → start → health → PoP re-register
//
// On any failure after snapshot: restore snapshots, restore firewall, restart previous service.
func Apply(ctx context.Context, opts Options) (*Result, error) {
	if err := opts.ValidateFlags(); err != nil {
		return nil, err
	}
	cfgPath := opts.ConfigPath
	if cfgPath == "" {
		cfgPath = paths.ServerConfig()
	}
	nodeKey := opts.NodeKeyPath
	if nodeKey == "" {
		nodeKey = paths.NodeKey()
	}
	base, err := localconfig.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("configure: load existing config: %w (existing registered node required)", err)
	}
	if _, err := os.Stat(nodeKey); err != nil {
		return nil, fmt.Errorf("configure: node.key missing at %s — refusing to create a new identity", nodeKey)
	}

	merged, err := Merge(*base, opts)
	if err != nil {
		return nil, err
	}

	stateDir := paths.StateDir
	if opts.StateDir != "" {
		stateDir = opts.StateDir
	}
	certPath, keyPath := DefaultTLSPaths(merged.TLSCertFile, merged.TLSKeyFile)
	prevSPKI, _ := CurrentSPKIHex(certPath, keyPath)

	res := &Result{
		DryRun:              opts.DryRun,
		NodeID:              merged.NodeID,
		LocationID:          merged.LocationID,
		PublicHost:          merged.PublicHost,
		DNSServers:          append([]string(nil), merged.DNSServers...),
		PrevSPKI:            prevSPKI,
		ControlPlaneURL:     merged.ControlPlaneURL,
		PrevControlPlaneURL: base.ControlPlaneURL,
		CPURLChanged:        !strings.EqualFold(strings.TrimRight(base.ControlPlaneURL, "/"), strings.TrimRight(merged.ControlPlaneURL, "/")),
	}

	wantACME := strings.TrimSpace(opts.TLSDomain) != ""
	wantOp := opts.TLSCert != "" && opts.TLSKey != ""
	wantCPURL := strings.TrimSpace(opts.ControlPlaneURL) != ""

	// Pre-flight: DNS before any TLS mutation when ACME is requested.
	if wantACME {
		expect, err := ResolveExpectPublicIPs(opts, base.PublicHost)
		if err != nil {
			return res, err
		}
		if err := CheckDNSpointsHere(opts.LookupIP, opts.TLSDomain, expect); err != nil {
			return res, err
		}
	}

	// Pre-flight: validate target Control Plane BEFORE any config write.
	if wantCPURL && !opts.SkipCPURLProbe {
		prevLookup := LookupIPFunc
		LookupIPFunc = opts.LookupIP
		defer func() { LookupIPFunc = prevLookup }()
		probeFn := opts.ExecProbeCP
		if probeFn == nil {
			probeFn = func(ctx context.Context, u string) (*ControlPlaneProbeResult, error) {
				return ProbeControlPlaneAuthFromConfig(ctx, u, base, nodeKey)
			}
		}
		if _, err := probeFn(ctx, merged.ControlPlaneURL); err != nil {
			return res, err
		}
	}

	if opts.DryRun {
		res.Message = "dry-run OK: existing node identity preserved; DNS/config/CP URL flags validated; no changes applied"
		if wantCPURL {
			res.Message = "dry-run OK: control plane URL validated (TLS+auth); identity preserved; no changes applied"
		}
		return res, nil
	}

	snapDir := filepath.Join(stateDir, "configure-snapshot")
	_ = os.RemoveAll(snapDir)
	if err := os.MkdirAll(snapDir, 0o700); err != nil {
		return res, err
	}
	defer func() { _ = os.RemoveAll(snapDir) }()

	if err := SnapshotFile(cfgPath, filepath.Join(snapDir, "server.json")); err != nil {
		return res, fmt.Errorf("configure: snapshot server.json: %w", err)
	}
	if err := errors.Join(SnapshotFile(certPath, filepath.Join(snapDir, "tls.crt")), SnapshotFile(keyPath, filepath.Join(snapDir, "tls.key"))); err != nil {
		return res, fmt.Errorf("configure: snapshot TLS: %w", err)
	}
	nftFile := "/etc/nftables.d/nyxveil.conf"
	if opts.NFTFile != "" {
		nftFile = opts.NFTFile
	}
	if err := SnapshotFile(nftFile, filepath.Join(snapDir, "nyxveil.conf")); err != nil {
		return res, fmt.Errorf("configure: snapshot firewall: %w", err)
	}

	stageCert, stageKey := StagingTLSPaths(stateDir)
	CleanStaging(stageCert, stageKey)
	defer CleanStaging(stageCert, stageKey)

	committed := false
	rollback := func(cause error) error {
		res.RolledBack = true
		res.RollbackConfigComplete = true
		CleanStaging(stageCert, stageKey)
		restoreErr := errors.Join(
			RestoreFile(filepath.Join(snapDir, "server.json"), cfgPath),
			RestoreFile(filepath.Join(snapDir, "tls.crt"), certPath),
			RestoreFile(filepath.Join(snapDir, "tls.key"), keyPath),
			RestoreFile(filepath.Join(snapDir, "nyxveil.conf"), nftFile),
			filemeta.EnforceRuntimeTLS(filepath.Dir(keyPath)),
		)
		if restoreErr != nil {
			res.RollbackConfigComplete = false
			return fmt.Errorf("configure: rollback restore failed: %w", errors.Join(cause, restoreErr))
		}
		if !opts.SkipFW {
			fw := opts.ExecFirewall
			if fw == nil {
				fw = ApplyNyxveilFirewall
			}
			if err := fw(FirewallOpts{
				NFTFile:   nftFile,
				TLSPort:   ParseListenPort(base.TLSListen, 443),
				QUICPort:  ParseListenPort(base.QUICListen, 443),
				VPNSubnet: base.VPNSubnetCIDR,
				Enable80:  strings.TrimSpace(base.ACMEDomain) != "",
			}); err != nil {
				res.RollbackConfigComplete = false
				return fmt.Errorf("configure: rollback firewall failed: %w", errors.Join(cause, err))
			}
		}
		if !opts.SkipSvc {
			if err := systemctlAction(opts, "start", "nyxveil-server"); err != nil {
				return fmt.Errorf("configure: rollback service start failed: %w", errors.Join(cause, err))
			}
			if err := waitHealthDataplane(opts, 30); err != nil {
				// Old CP URL may be TLS-invalid after CP hostname migration.
				res.RollbackEndpointHealthImpossible = true
				return fmt.Errorf("configure: ROLLBACK CONFIG COMPLETE but endpoint health impossible (dataplane check: %v); original: %w", err, cause)
			}
			// Full healthy (incl. cp_connected) may fail if old CP cert no longer matches.
			if err := waitHealth(opts, 15); err != nil {
				res.RollbackEndpointHealthImpossible = true
				return fmt.Errorf("configure: ROLLBACK CONFIG COMPLETE; old control_plane_url may be unreachable/TLS-invalid; VPN datapath restored; original: %w", cause)
			}
		}
		return fmt.Errorf("configure: rolled back after failure: %w", cause)
	}
	rollbackCPAware := rollback

	// Keep OLD working TLS listeners active during ACME HTTP-01.
	// Only open Nyxveil-managed TCP/80 before requesting the challenge.
	applyFW := opts.ExecFirewall
	if applyFW == nil {
		applyFW = ApplyNyxveilFirewall
	}
	need80 := wantACME || strings.TrimSpace(merged.ACMEDomain) != ""
	if !opts.SkipFW && need80 {
		if err := applyFW(FirewallOpts{
			NFTFile:   nftFile,
			TLSPort:   ParseListenPort(merged.TLSListen, 443),
			QUICPort:  ParseListenPort(merged.QUICListen, 443),
			VPNSubnet: merged.VPNSubnetCIDR,
			Enable80:  true,
		}); err != nil {
			return res, rollback(err)
		}
		if opts.OnBeforeACME != nil {
			opts.OnBeforeACME()
		}
	}

	validateHost := merged.PublicHost
	if wantACME {
		validateHost = opts.TLSDomain
	}

	// Stage TLS material; never validate the TARGET hostname against LIVE cert.
	switch {
	case wantOp:
		if err := ValidateLeafForDomainOpts(opts.TLSCert, opts.TLSKey, validateHost, time.Now(), !opts.SkipCertTrust); err != nil {
			return res, rollback(err)
		}
		res.NewSPKI, _ = CurrentSPKIHex(opts.TLSCert, opts.TLSKey)
	case wantACME:
		if err := SeedStagingKeyFromLive(keyPath, stageKey); err != nil {
			return res, rollback(err)
		}
		acmeDir := filepath.Join(stateDir, "acme")
		issue := opts.ExecACME
		if issue == nil {
			issue = defaultIssueACME
		}
		if err := issue(ctx, ACMEIssueArgs{
			Domain:    opts.TLSDomain,
			Email:     opts.TLSEmail,
			Directory: opts.ACMEDirectory,
			StateDir:  acmeDir,
			StageCert: stageCert,
			StageKey:  stageKey,
			Replace:   true, // staging always forces domain-targeted issuance
		}); err != nil {
			return res, rollback(err)
		}
		// Explicit staged-path validation — never pass live tls.crt here.
		if err := ValidateLeafForDomainOpts(stageCert, stageKey, opts.TLSDomain, time.Now(), !opts.SkipCertTrust); err != nil {
			return res, rollback(err)
		}
		res.NewSPKI, _ = CurrentSPKIHex(stageCert, stageKey)
	default:
		// Config-only (public_host / dns) — live TLS unchanged.
		res.NewSPKI = prevSPKI
	}

	res.SPKIChanged = prevSPKI != "" && res.NewSPKI != "" && !strings.EqualFold(prevSPKI, res.NewSPKI)

	// Final commit window: stop → write live TLS + server.json → start → health → CP.
	if !opts.SkipSvc {
		if err := systemctlAction(opts, "stop", "nyxveil-server"); err != nil {
			return res, rollback(fmt.Errorf("configure: stop service: %w", err))
		}
		time.Sleep(500 * time.Millisecond)
	}

	switch {
	case wantOp:
		if err := InstallOperatorTLS(opts.TLSCert, opts.TLSKey, certPath, keyPath, opts.TLSReplace); err != nil {
			return res, rollback(err)
		}
	case wantACME:
		if err := AtomicCommitTLS(stageCert, stageKey, certPath, keyPath); err != nil {
			return res, rollback(err)
		}
	}
	_ = EnsureOwnerReadable(certPath, keyPath)
	_ = filemeta.EnforceRuntimeTLS(filepath.Dir(keyPath))

	if err := AtomicSave(cfgPath, &merged); err != nil {
		return res, rollback(err)
	}
	committed = true

	// Persist port 80 for successful ACME (renewals). Already applied above when need80.
	if !opts.SkipFW && !need80 {
		if err := applyFW(FirewallOpts{
			NFTFile:   nftFile,
			TLSPort:   ParseListenPort(merged.TLSListen, 443),
			QUICPort:  ParseListenPort(merged.QUICListen, 443),
			VPNSubnet: merged.VPNSubnetCIDR,
			Enable80:  false,
		}); err != nil {
			return res, rollback(err)
		}
	}

	if !opts.SkipSvc {
		if err := systemctlAction(opts, "start", "nyxveil-server"); err != nil {
			return res, rollbackCPAware(err)
		}
		if wantCPURL {
			if err := waitHealthCPConnected(opts, 90); err != nil {
				return res, rollbackCPAware(err)
			}
			res.CPConnected = true
		} else if err := waitHealth(opts, 60); err != nil {
			return res, rollbackCPAware(err)
		}
	}

	needRegister := res.SPKIChanged ||
		strings.TrimSpace(opts.PublicHost) != "" ||
		wantACME || wantOp || wantCPURL
	if needRegister && !opts.SkipCP {
		reg := opts.ExecRegister
		if reg == nil {
			reg = defaultPoPRegister
		}
		if err := reg(cfgPath); err != nil {
			return res, rollbackCPAware(fmt.Errorf("Control Plane same-node re-register (SPKI/endpoints/CP URL) failed: %w", err))
		}
		res.Registered = true
	}

	if wantCPURL && !opts.SkipCP {
		verify := opts.ExecVerifyCatalog
		if verify == nil {
			verify = func(ctx context.Context, p string) (*CatalogFreshness, error) {
				spki, _ := CurrentSPKIHex(certPath, keyPath)
				return DefaultVerifyCatalogAfterCPURL(ctx, p, nodeKey, spki)
			}
		}
		cat, err := verify(ctx, cfgPath)
		if err != nil {
			return res, rollbackCPAware(fmt.Errorf("catalog/management freshness verify failed: %w", err))
		}
		if cat == nil || !cat.Verified {
			return res, rollbackCPAware(fmt.Errorf("catalog/management freshness verify failed: not verified"))
		}
		res.CatalogVerified = true
	}

	_ = committed
	CleanStaging(stageCert, stageKey)
	res.NewSPKI, _ = CurrentSPKIHex(certPath, keyPath)
	res.SPKIChanged = res.PrevSPKI != "" && res.NewSPKI != "" && !strings.EqualFold(res.PrevSPKI, res.NewSPKI)
	res.Message = "configure OK: identity preserved; staged TLS validated then committed; config/firewall applied"
	if wantCPURL {
		res.Message = "configure OK: control_plane_url updated; same-node identity preserved; CP connected; catalog/management verified"
	}
	return res, nil
}

// ACMEIssueArgs are staging-targeted ACME inputs (no live cert paths).
type ACMEIssueArgs struct {
	Domain    string
	Email     string
	Directory string
	StateDir  string
	StageCert string
	StageKey  string
	Replace   bool
}

func defaultIssueACME(ctx context.Context, a ACMEIssueArgs) error {
	_, _, _, _, err := nodetls.IssueOrRenew(ctx, nodetls.ACMEConfig{
		Domain:     a.Domain,
		Email:      a.Email,
		Directory:  a.Directory,
		StateDir:   a.StateDir,
		Dest:       nodetls.Paths{CertFile: a.StageCert, KeyFile: a.StageKey},
		Replace:    a.Replace,
		AccountKey: filepath.Join(a.StateDir, "acme-account.key"),
	})
	return err
}

func systemctlAction(opts Options, action, unit string) error {
	if opts.ExecSystemctl != nil {
		return opts.ExecSystemctl(action, unit)
	}
	return exec.Command("systemctl", action, unit).Run()
}

func waitHealth(opts Options, seconds int) error {
	fn := opts.ExecHealth
	if fn == nil {
		fn = func() error {
			cmd := exec.Command("nyxveilctl", "health")
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
			}
			return nil
		}
	}
	var last error
	for i := 0; i < seconds; i++ {
		if err := fn(); err == nil {
			return nil
		} else {
			last = err
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("configure: health gate failed: %w", last)
}

// waitHealthCPConnected requires 3 consecutive samples with healthy + cp_connected.
func waitHealthCPConnected(opts Options, seconds int) error {
	const need = 3
	stable := 0
	var last error
	for i := 0; i < seconds; i++ {
		healthy, cpOK, err := readHealthFlags(opts)
		if err != nil {
			stable = 0
			last = err
			time.Sleep(time.Second)
			continue
		}
		if !healthy || !cpOK {
			stable = 0
			last = fmt.Errorf("healthy=%v cp_connected=%v", healthy, cpOK)
			time.Sleep(time.Second)
			continue
		}
		stable++
		if stable >= need {
			return nil
		}
		time.Sleep(time.Second)
	}
	if last == nil {
		last = fmt.Errorf("timeout")
	}
	return fmt.Errorf("configure: cp_connected health gate failed: %w", last)
}

// waitHealthDataplane accepts tun/tls/quic readiness even when cp_connected is false
// (management plane may be unavailable after CP hostname migration).
func waitHealthDataplane(opts Options, seconds int) error {
	if opts.ExecHealthStatus != nil {
		var last error
		for i := 0; i < seconds; i++ {
			_, _, err := opts.ExecHealthStatus()
			// Dataplane-only: if hook returns err only when process down, treat nil as OK.
			if err == nil {
				return nil
			}
			last = err
			time.Sleep(time.Second)
		}
		return last
	}
	// Fallback: control socket responding is enough for dataplane-present signal in tests/prod without JSON.
	return waitHealth(opts, seconds)
}

func readHealthFlags(opts Options) (healthy, cpConnected bool, err error) {
	if opts.ExecHealthStatus != nil {
		return opts.ExecHealthStatus()
	}
	cmd := exec.Command("nyxveilctl", "status")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Fall back to health endpoint shape.
		cmd2 := exec.Command("nyxveilctl", "health")
		out2, err2 := cmd2.CombinedOutput()
		if err2 != nil {
			return false, false, fmt.Errorf("%w / %v: %s", err, err2, strings.TrimSpace(string(out2)))
		}
		out = out2
	}
	var wrap struct {
		Healthy     *bool `json:"healthy"`
		CPConnected *bool `json:"cp_connected"`
	}
	if json.Unmarshal(out, &wrap) != nil {
		return false, false, fmt.Errorf("configure: cannot parse health JSON")
	}
	if wrap.Healthy == nil {
		return false, false, fmt.Errorf("configure: health JSON missing healthy")
	}
	cp := wrap.CPConnected != nil && *wrap.CPConnected
	return *wrap.Healthy, cp, nil
}

func defaultPoPRegister(cfgPath string) error {
	bin := paths.BinaryPath()
	if _, err := os.Stat(bin); err != nil {
		bin = "nyxveil-server"
	}
	cmd := exec.Command(bin, "--config", cfgPath, "--register-stdin")
	cmd.Stdin = strings.NewReader("\n")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return runAsNyxveil(cmd)
}

func runAsNyxveil(cmd *exec.Cmd) error {
	timeoutPath, err := exec.LookPath("timeout")
	if err != nil {
		return fmt.Errorf("configure: GNU timeout required for bounded registration: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 640*time.Second)
	defer cancel()
	run := func(path string, args ...string) error {
		c := exec.CommandContext(ctx, path, args...)
		c.Stdin, c.Stdout, c.Stderr = cmd.Stdin, cmd.Stdout, cmd.Stderr
		c.WaitDelay = 15 * time.Second
		return c.Run()
	}
	if effectiveUID() != 0 {
		return run(timeoutPath, append([]string{"-k", "15", "600", cmd.Path}, cmd.Args[1:]...)...)
	}
	// Prefer systemd-run with transient CAP_NET_BIND_SERVICE for ACME HTTP-01.
	if path, err := exec.LookPath("systemd-run"); err == nil {
		if _, err := os.Stat("/run/systemd/system"); err == nil {
			unit := fmt.Sprintf("nyxveil-register-%d-%d.service", os.Getpid(), time.Now().UnixNano())
			defer func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cleanupCancel()
				cleanup := exec.CommandContext(cleanupCtx, "systemctl", "stop", unit)
				cleanup.WaitDelay = 2 * time.Second
				_ = cleanup.Run()
			}()
			inner := []string{cmd.Path}
			inner = append(inner, cmd.Args[1:]...)
			args := []string{
				"--unit=" + unit,
				"--uid=nyxveil", "--gid=nyxveil",
				"--property=RuntimeMaxSec=600", "--property=TimeoutStopSec=15",
				"--property=KillMode=control-group",
				"--property=AmbientCapabilities=CAP_NET_BIND_SERVICE",
				"--property=CapabilityBoundingSet=CAP_NET_BIND_SERVICE",
				"--property=NoNewPrivileges=true",
				"--wait", "--pipe", "--collect", "--quiet",
			}
			if timeoutPath != "" {
				args = append(args, timeoutPath, "-k", "15", "600")
			}
			args = append(args, inner...)
			return run(path, args...)
		}
	}
	if path, err := exec.LookPath("setpriv"); err == nil {
		args := []string{
			"--reuid=nyxveil", "--regid=nyxveil", "--clear-groups",
			"--bounding-set=-all,+net_bind_service", "--no-new-privs",
			"--inh-caps=-all,+net_bind_service", "--ambient-caps=-all,+net_bind_service",
			"--", timeoutPath, "-k", "15", "600", cmd.Path,
		}
		args = append(args, cmd.Args[1:]...)
		return run(path, args...)
	}
	// Fail closed: uncapped runuser cannot bind :80 under default
	// ip_unprivileged_port_start=1024 (live gate blocker on Ubuntu 24.04).
	return fmt.Errorf("configure: systemd-run or setpriv required for transient CAP_NET_BIND_SERVICE (refuse uncapped runuser)")
}
