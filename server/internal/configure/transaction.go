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
	DryRun           bool   `json:"dry_run"`
	NodeID           string `json:"node_id"`
	LocationID       string `json:"location_id"`
	PublicHost       string `json:"public_host"`
	DNSServers       []string `json:"dns_servers"`
	PrevSPKI         string `json:"previous_spki_sha256,omitempty"`
	NewSPKI          string `json:"new_spki_sha256,omitempty"`
	SPKIChanged      bool   `json:"spki_changed"`
	Registered       bool   `json:"control_plane_reregistered"`
	RolledBack       bool   `json:"rolled_back,omitempty"`
	Message          string `json:"message,omitempty"`
}

// Apply runs dry-run checks or a transactional reconfigure of an existing node.
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

	// Pre-flight: DNS before any TLS mutation when ACME is requested.
	if strings.TrimSpace(opts.TLSDomain) != "" {
		expect, err := ResolveExpectPublicIPs(opts, base.PublicHost)
		if err != nil {
			return res, err
		}
		lookup := opts.LookupIP
		if err := CheckDNSpointsHere(lookup, opts.TLSDomain, expect); err != nil {
			return res, err
		}
	}

	if opts.DryRun {
		res.Message = "dry-run OK: existing node identity preserved; DNS/config flags validated; no changes applied"
		return res, nil
	}

	snapDir := filepath.Join(paths.StateDir, "configure-snapshot")
	if opts.StateDir != "" {
		snapDir = filepath.Join(opts.StateDir, "configure-snapshot")
	}
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

	rollback := func(cause error) error {
		res.RolledBack = true
		_ = RestoreFile(filepath.Join(snapDir, "server.json"), cfgPath)
		_ = RestoreFile(filepath.Join(snapDir, "tls.crt"), certPath)
		_ = RestoreFile(filepath.Join(snapDir, "tls.key"), keyPath)
		_ = RestoreFile(filepath.Join(snapDir, "nyxveil.conf"), nftFile)
		if !opts.SkipFW {
			_ = ApplyNyxveilFirewall(FirewallOpts{
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

	sysctl := opts.ExecSystemctl
	if sysctl == nil {
		sysctl = func(action, unit string) error {
			return exec.Command("systemctl", action, unit).Run()
		}
	}

	if !opts.SkipSvc {
		_ = sysctl("stop", "nyxveil-server")
		time.Sleep(500 * time.Millisecond)
	}

	need80 := strings.TrimSpace(merged.ACMEDomain) != "" || strings.TrimSpace(opts.TLSDomain) != ""
	if !opts.SkipFW {
		if err := ApplyNyxveilFirewall(FirewallOpts{
			NFTFile:   nftFile,
			TLSPort:   ParseListenPort(merged.TLSListen, 443),
			QUICPort:  ParseListenPort(merged.QUICListen, 443),
			VPNSubnet: merged.VPNSubnetCIDR,
			Enable80:  need80,
		}); err != nil {
			return res, rollback(err)
		}
	}

	if err := AtomicSave(cfgPath, &merged); err != nil {
		return res, rollback(err)
	}

	// TLS material
	if opts.TLSCert != "" && opts.TLSKey != "" {
		if err := InstallOperatorTLS(opts.TLSCert, opts.TLSKey, certPath, keyPath, opts.TLSReplace); err != nil {
			return res, rollback(err)
		}
		host := merged.PublicHost
		if strings.TrimSpace(opts.TLSDomain) != "" {
			host = opts.TLSDomain
		}
		if err := ValidateLeafForDomainOpts(certPath, keyPath, host, time.Now(), !opts.SkipCertTrust); err != nil {
			return res, rollback(err)
		}
	} else if strings.TrimSpace(opts.TLSDomain) != "" {
		acmeState := paths.StateDir
		if opts.StateDir != "" {
			acmeState = opts.StateDir
		}
		acmeDir := filepath.Join(acmeState, "acme")
		_, _, _, _, err := nodetls.IssueOrRenew(ctx, nodetls.ACMEConfig{
			Domain:     opts.TLSDomain,
			Email:      opts.TLSEmail,
			StateDir:   acmeDir,
			Dest:       nodetls.Paths{CertFile: certPath, KeyFile: keyPath},
			Replace:    opts.TLSReplace,
			AccountKey: filepath.Join(acmeDir, "acme-account.key"),
		})
		if err != nil {
			return res, rollback(err)
		}
		if err := ValidateLeafForDomainOpts(certPath, keyPath, opts.TLSDomain, time.Now(), !opts.SkipCertTrust); err != nil {
			return res, rollback(err)
		}
	}
	_ = EnsureOwnerReadable(certPath, keyPath)

	newSPKI, _ := CurrentSPKIHex(certPath, keyPath)
	res.NewSPKI = newSPKI
	res.SPKIChanged = prevSPKI != "" && newSPKI != "" && !strings.EqualFold(prevSPKI, newSPKI)

	needRegister := res.SPKIChanged ||
		strings.TrimSpace(opts.PublicHost) != "" ||
		strings.TrimSpace(opts.TLSDomain) != "" ||
		(opts.TLSCert != "" && opts.TLSKey != "")
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

	if !opts.SkipSvc {
		if err := sysctl("start", "nyxveil-server"); err != nil {
			return res, rollback(err)
		}
		if err := waitHealth(opts, 60); err != nil {
			return res, rollback(err)
		}
	}

	res.Message = "configure OK: identity preserved; config/TLS/firewall applied"
	return res, nil
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
	// Prefer service user when available.
	if err := runAsNyxveil(cmd); err != nil {
		return err
	}
	return nil
}

func runAsNyxveil(cmd *exec.Cmd) error {
	// Best-effort: if already non-root or helpers missing, run directly.
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
