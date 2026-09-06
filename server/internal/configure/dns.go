package configure

import (
	"fmt"
	"net"
	"strings"
)

// ResolveExpectPublicIPs returns IPs that tls-domain / public_host must resolve to.
// Prefer Options.PublicIPHint; otherwise resolve current public_host if it is an IP,
// else fail closed requiring an explicit hint or IP-form public_host.
func ResolveExpectPublicIPs(opts Options, currentPublicHost string) ([]net.IP, error) {
	hint := strings.TrimSpace(opts.PublicIPHint)
	if hint != "" {
		ip := net.ParseIP(hint)
		if ip == nil {
			return nil, fmt.Errorf("configure: --expect-public-ip %q is not a valid IP", hint)
		}
		return []net.IP{ip}, nil
	}
	host := strings.TrimSpace(currentPublicHost)
	if host == "" {
		host = strings.TrimSpace(opts.PublicHost)
	}
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	return nil, fmt.Errorf("configure: cannot determine expected public IP for DNS check (set --expect-public-ip or keep public_host as the current node IP until DNS is ready)")
}

// CheckDNSpointsHere resolves domain and requires at least one A/AAAA matching expected public IPs.
func CheckDNSpointsHere(lookup func(host string) ([]net.IP, error), domain string, expect []net.IP) error {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return fmt.Errorf("configure: domain required for DNS check")
	}
	if lookup == nil {
		lookup = net.LookupIP
	}
	got, err := lookup(domain)
	if err != nil {
		return fmt.Errorf("configure: DNS lookup %q failed: %w", domain, err)
	}
	if len(got) == 0 {
		return fmt.Errorf("configure: DNS lookup %q returned no addresses", domain)
	}
	for _, g := range got {
		for _, e := range expect {
			if g.Equal(e) {
				return nil
			}
		}
	}
	return fmt.Errorf("configure: DNS for %q does not point at this node (got %v, want one of %v) — refusing TLS change", domain, formatIPs(got), formatIPs(expect))
}

func formatIPs(ips []net.IP) string {
	parts := make([]string, 0, len(ips))
	for _, ip := range ips {
		parts = append(parts, ip.String())
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
