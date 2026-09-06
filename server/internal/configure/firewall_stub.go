//go:build !linux

package configure

import "fmt"

// FirewallOpts is a stub on non-Linux.
type FirewallOpts struct {
	NFTFile   string
	TLSPort   int
	QUICPort  int
	VPNSubnet string
	Enable80  bool
}

func ApplyNyxveilFirewall(opts FirewallOpts) error {
	return fmt.Errorf("configure: nftables firewall apply is Linux-only")
}

func ParseListenPort(listen string, def int) int {
	return ParseListenPortShared(listen, def)
}
