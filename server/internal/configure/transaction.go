package configure

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nyxveil/server/internal/localconfig"
	"github.com/nyxveil/server/internal/nodetls"
	"github.com/nyxveil/server/internal/paths"
)

// Result summarizes a configure run (never includes private keys).
type Result struct {
	DryRun      bool     `json:"dry_run"`
	NodeID      string   `json:"node_id"`
	LocationID  string   `json:"location_id"`
	PublicHost  string   `json:"public_host"`
	DNSServers  []string `json:"dns_servers"`
	PrevSPKI    string   `json:"previous_spki_sha256,omitempty"`
	NewSPKI     string   `json:"new_spki_sha256,omitempty"`
	SPKIChanged bool     `json:"spki_changed"`
	Registered  bool     `json:"control_plane_reregistered"`
	RolledBack  bool     `json:"rolled_back,omitempty"`
	Message     string   `json:"message,omitempty"`
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
		DryRun:     opts.DryRun,
		NodeID:     merged.NodeID,
		LocationID: merged.LocationID,
		PublicHost: merged.PublicHost,
		DNSServers: append([]string(nil), merged.DNSServers...),
		PrevSPKI:   prevSPKI,
	}

	wantACME := strings.TrimSpace(opts.TLSDomain) != ""
	wantOp := opts.TLSCert != "" && opts.TLSKey != ""

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

	if opts.DryRun {
		res.Message = "dry-run OK: existing node identity preserved; DNS/config flags validated; no changes applied"
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
	_ = SnapshotFile(certPath, filepath.Join(snapDir, "tls.crt"))
	_ = SnapshotFile(keyPath, filepath.Join(snapDir, "tls.key"))
	nftFile := "/etc/nftables.d/nyxveil.conf"
	if opts.NFTFile != "" {
		nftFile = opts.NFTFile
	}
	_ = SnapshotFile(nftFile, filepath.Join(snapDir, "nyxveil.conf"))

	stageCert, stageKey := StagingTLSPaths(stateDir)
	CleanStaging(stageCert, stageKey)
	defer CleanStaging(stageCert, stageKey)

	committed := false
	rollback := func(cause error) error {
		res.RolledBack = true
		CleanStaging(stageCert, stageKey)
		_ = RestoreFile(filepath.Join(snapDir, "server.json"), cfgPath)
		_ = RestoreFile(filepath.Join(snapDir, "tls.crt"), certPath)
		_ = RestoreFile(filepath.Join(snapDir, "tls.key"), keyPath)
		_ = RestoreFile(filepath.Join(snapDir, "nyxveil.conf"), nftFile)
		if !opts.SkipFW {
			fw := opts.ExecFirewall
			if fw == nil {
				fw = ApplyNyxveilFirewall
			}
			_ = fw(FirewallOpts{
				NFTFile:   nftFile,
				TLSPort:   ParseListenPort(base.TLSListen, 443),
				QUICPort:  ParseListenPort(base.QUICListen, 443),
				VPNSubnet: base.VPNSubnetCIDR,
				Enable80:  strings.TrimSpace(base.ACMEDomain) != "",
			})
		}
		if !opts.SkipSvc {
			_ = systemctlAction(opts, "start", "nyxveil-server")
			_ = waitHealth(opts, 30)
		}
		return fmt.Errorf("configure: rolled back after failure: %w", cause)
	}

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
		_ = systemctlAction(opts, "stop", "nyxveil-server")
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
			return res, rollback(err)
		}
		if err := waitHealth(opts, 60); err != nil {
			return res, rollback(err)
		}
	}

	needRegister := res.SPKIChanged ||
		strings.TrimSpace(opts.PublicHost) != "" ||
		wantACME || wantOp
	if needRegister && !opts.SkipCP {
		reg := opts.ExecRegister
		if reg == nil {
			reg = defaultPoPRegister
		}
		if err := reg(cfgPath); err != nil {
			return res, rollback(fmt.Errorf("Control Plane same-node re-register (SPKI/endpoints) failed: %w", err))
		}
		res.Registered = true
	}

	_ = committed
	CleanStaging(stageCert, stageKey)
	res.Message = "configure OK: identity preserved; staged TLS validated then committed; config/firewall applied"
	return res, nil
}

// ACMEIssueArgs are staging-targeted ACME inputs (no live cert paths).
type ACMEIssueArgs struct {
	Domain    string
	Email     string
	StateDir  string
	StageCert string
	StageKey  string
	Replace   bool
}

func defaultIssueACME(ctx context.Context, a ACMEIssueArgs) error {
	_, _, _, _, err := nodetls.IssueOrRenew(ctx, nodetls.ACMEConfig{
		Domain:     a.Domain,
		Email:      a.Email,
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
	if effectiveUID() != 0 {
		return cmd.Run()
	}
	if path, err := exec.LookPath("runuser"); err == nil {
		args := append([]string{"-u", "nyxveil", "--", cmd.Path}, cmd.Args[1:]...)
		c := exec.Command(path, args...)
		c.Stdin = cmd.Stdin
		c.Stdout = cmd.Stdout
		c.Stderr = cmd.Stderr
		return c.Run()
	}
	return cmd.Run()
}
