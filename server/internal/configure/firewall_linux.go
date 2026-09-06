//go:build linux

package configure

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if err := os.Rename(tmp, opts.NFTFile); err != nil {
		return err
	}
	_ = exec.Command("nft", "delete", "table", "inet", "nyxveil").Run()
	if out, err := exec.Command("nft", "-f", opts.NFTFile).CombinedOutput(); err != nil {
		return fmt.Errorf("configure: nft -f %s: %w (%s)", opts.NFTFile, err, strings.TrimSpace(string(out)))
	}
	return reloadFirewallUnit()
}

func reloadFirewallUnit() error {
	_ = exec.Command("systemctl", "daemon-reload").Run()
	_ = exec.Command("systemctl", "enable", firewallUnit).Run()
	if out, err := exec.Command("systemctl", "restart", firewallUnit).CombinedOutput(); err != nil {
		if out2, err2 := exec.Command("systemctl", "start", firewallUnit).CombinedOutput(); err2 != nil {
			return fmt.Errorf("configure: firewall unit: %v / %v (%s %s)", err, err2, strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)))
		}
	}
	return nil
}

// ParseListenPort extracts port from ":443" style listen strings.
func ParseListenPort(listen string, def int) int {
	return ParseListenPortShared(listen, def)
}

// NFTHasACME80 reports whether the managed file already opens TCP/80.
func NFTHasACME80(nftFile string) bool {
	if nftFile == "" {
		nftFile = defaultNFTFile
	}
	b, err := os.ReadFile(nftFile)
	if err != nil {
		return false
	}
	return strings.Contains(string(b), acmeComment) || strings.Contains(string(b), "tcp dport 80")
}
