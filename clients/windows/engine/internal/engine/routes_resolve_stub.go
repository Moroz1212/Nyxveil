//go:build !windows

package engine

import "net/netip"

// ResolveHostIPs resolves hostname to IPv4 (shared with Windows applier helpers).
func ResolveHostIPs(host string) []netip.Addr {
	if ip, err := netip.ParseAddr(host); err == nil && ip.Is4() {
		return []netip.Addr{ip}
	}
	return nil
}
