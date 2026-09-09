package configure

import (
	"fmt"
	"os"
	"path/filepath"
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
	b.WriteString("# destroy makes `nft -f` idempotent (no duplicate rules on re-apply).\n")
	b.WriteString("destroy table inet nyxveil\n")
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

// NFTHasACME80 reports whether the managed file already opens TCP/80.
func NFTHasACME80(nftFile string) bool {
	if nftFile == "" {
		return false
	}
	b, err := os.ReadFile(nftFile)
	if err != nil {
		return false
	}
	s := string(b)
	return strings.Contains(s, "nyxveil-acme-http01") || strings.Contains(s, "tcp dport 80")
}

// WriteNFTFileOnly persists RenderNyxveilNFT to opts.NFTFile (no nft/systemctl).
// Used by tests and as a safe dry helper; production Linux uses ApplyNyxveilFirewall.
func WriteNFTFileOnly(opts FirewallOpts) error {
	if opts.NFTFile == "" {
		return fmt.Errorf("configure: NFTFile required")
	}
	if err := os.MkdirAll(filepath.Dir(opts.NFTFile), 0o755); err != nil {
		return err
	}
	body := RenderNyxveilNFT(opts)
	tmp := opts.NFTFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, opts.NFTFile)
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
