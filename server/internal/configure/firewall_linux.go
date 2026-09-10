//go:build linux

package configure

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultNFTFile = "/etc/nftables.d/nyxveil.conf"
	firewallUnit   = "nyxveil-firewall"
	acmeComment    = "nyxveil-acme-http01"
)

// FirewallOpts controls nftables rewrite for table inet nyxveil only.
type FirewallOpts struct {
	NFTFile   string
	TLSPort   int
	QUICPort  int
	VPNSubnet string
	Enable80  bool // ACME HTTP-01
}

// ApplyNyxveilFirewall rewrites the Nyxveil-owned nftables file idempotently and reloads the unit.
// Never flushes the global ruleset — only replaces table inet nyxveil.
func ApplyNyxveilFirewall(opts FirewallOpts) error {
	if opts.NFTFile == "" {
		opts.NFTFile = defaultNFTFile
	}
	if opts.TLSPort <= 0 {
		opts.TLSPort = 443
	}
	if opts.QUICPort <= 0 {
		opts.QUICPort = 443
	}
	if opts.VPNSubnet == "" {
		opts.VPNSubnet = "10.66.0.0/24"
	}
	body := RenderNyxveilNFT(opts)
	if err := os.MkdirAll(filepath.Dir(opts.NFTFile), 0o755); err != nil {
		return err
	}
	if prev, err := os.ReadFile(opts.NFTFile); err == nil && string(prev) == body {
		return reloadFirewallUnit()
	}
	tmp := opts.NFTFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if out, err := firewallCommand("nft", "--check", "-f", tmp); err != nil {
		return fmt.Errorf("configure: validate nft: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	if err := os.Rename(tmp, opts.NFTFile); err != nil {
		return err
	}
	if out, err := firewallCommand("nft", "-f", opts.NFTFile); err != nil {
		return fmt.Errorf("configure: nft -f %s: %w (%s)", opts.NFTFile, err, strings.TrimSpace(string(out)))
	}
	// Unit restart also loads the file; conf must contain `destroy table` so
	// ExecStart cannot accumulate duplicate rules.
	return reloadFirewallUnit()
}

func reloadFirewallUnit() error {
	for _, args := range [][]string{{"daemon-reload"}, {"enable", firewallUnit}, {"restart", firewallUnit}} {
		if out, err := firewallCommand("systemctl", args...); err != nil {
			return fmt.Errorf("configure: firewall unit %s: %w (%s)", args[0], err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func firewallCommand(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 5 * time.Second
	return cmd.CombinedOutput()
}

// ParseListenPort extracts port from ":443" style listen strings.
func ParseListenPort(listen string, def int) int {
	return ParseListenPortShared(listen, def)
}
