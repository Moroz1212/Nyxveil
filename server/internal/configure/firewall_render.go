package configure

import (
	"fmt"
	"strconv"
	"strings"
)

// RenderNyxveilNFT returns the idempotent nftables file body for table inet nyxveil.
func RenderNyxveilNFT(opts FirewallOpts) string {
	tlsPort := opts.TLSPort
	if tlsPort <= 0 {
		tlsPort = 443
	}
	quicPort := opts.QUICPort
	if quicPort <= 0 {
		quicPort = 443
	}
	vpn := opts.VPNSubnet
	if vpn == "" {
		vpn = "10.66.0.0/24"
	}
	var b strings.Builder
	b.WriteString("# Managed by Nyxveil configure — table inet nyxveil only\n")
	b.WriteString("table inet nyxveil {\n")
	b.WriteString("  chain input {\n")
	b.WriteString("    type filter hook input priority filter - 10; policy accept;\n")
	if opts.Enable80 {
		b.WriteString("    tcp dport 80 ct state new accept comment \"nyxveil-acme-http01\"\n")
	}
	b.WriteString(fmt.Sprintf("    tcp dport %d ct state new accept comment \"nyxveil-tls\"\n", tlsPort))
	b.WriteString(fmt.Sprintf("    udp dport %d ct state new accept comment \"nyxveil-quic\"\n", quicPort))
	b.WriteString("  }\n\n")
	b.WriteString("  chain forward {\n")
	b.WriteString("    type filter hook forward priority filter - 10; policy accept;\n")
	b.WriteString("    iifname \"nyxveil0\" accept comment \"nyxveil-fwd-in\"\n")
	b.WriteString("    oifname \"nyxveil0\" accept comment \"nyxveil-fwd-out\"\n")
	b.WriteString("  }\n\n")
	b.WriteString("  chain postrouting {\n")
	b.WriteString("    type nat hook postrouting priority srcnat; policy accept;\n")
	b.WriteString(fmt.Sprintf("    ip saddr %s oifname != \"nyxveil0\" masquerade comment \"nyxveil-masq\"\n", vpn))
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String()
}

// ParseListenPortShared extracts port from listen strings (all platforms).
func ParseListenPortShared(listen string, def int) int {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		return def
	}
	if strings.HasPrefix(listen, ":") {
		if p, err := strconv.Atoi(listen[1:]); err == nil && p > 0 {
			return p
		}
	}
	if i := strings.LastIndex(listen, ":"); i >= 0 {
		if p, err := strconv.Atoi(listen[i+1:]); err == nil && p > 0 {
			return p
		}
	}
	return def
}
