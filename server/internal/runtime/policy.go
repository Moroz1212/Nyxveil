package runtime

import (
	"encoding/json"
	"strconv"
	"strings"
)

// listenPort extracts the TCP/UDP port from an address like ":443" or "0.0.0.0:8443".
func listenPort(addr string, def int) int {
	if def <= 0 {
		def = 443
	}
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return def
	}
	// Prefer last colon segment (handles :443 and [::]:443).
	if i := strings.LastIndex(addr, ":"); i >= 0 && i+1 < len(addr) {
		p := addr[i+1:]
		if n, err := strconv.Atoi(p); err == nil && n > 0 && n < 65536 {
			return n
		}
	}
	return def
}

type transportPolicy struct {
	TLS      *bool    `json:"tls"`
	QUIC     *bool    `json:"quic"`
	Profiles []string `json:"profiles"`
}

func parseTransportPolicy(raw string) (tlsOn, quicOn bool) {
	tlsOn, quicOn = true, true
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return tlsOn, quicOn
	}
	var p transportPolicy
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return tlsOn, quicOn
	}
	if len(p.Profiles) > 0 {
		tlsOn, quicOn = false, false
		for _, name := range p.Profiles {
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "tls":
				tlsOn = true
			case "quic":
				quicOn = true
			}
		}
		return tlsOn, quicOn
	}
	if p.TLS != nil {
		tlsOn = *p.TLS
	}
	if p.QUIC != nil {
		quicOn = *p.QUIC
	}
	return tlsOn, quicOn
}

type echPolicy struct {
	Require   *bool  `json:"require"`
	Preferred *bool  `json:"preferred"`
	Mode      string `json:"mode"`
}

// parseECHPolicy returns (requireECH, enableECHKeys).
// preferred/empty → load keys but do not require; require → RequireECH=true.
func parseECHPolicy(raw *string) (require, wantKeys bool) {
	if raw == nil {
		return false, false
	}
	s := strings.TrimSpace(*raw)
	if s == "" {
		return false, false
	}
	var p echPolicy
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		// bare string modes
		switch strings.ToLower(s) {
		case "require", `"require"`:
			return true, true
		case "preferred", `"preferred"`:
			return false, true
		default:
			return false, false
		}
	}
	mode := strings.ToLower(strings.TrimSpace(p.Mode))
	if mode == "require" || (p.Require != nil && *p.Require) {
		return true, true
	}
	if mode == "preferred" || (p.Preferred != nil && *p.Preferred) {
		return false, true
	}
	if p.Require != nil || p.Preferred != nil || mode != "" {
		return false, true
	}
	return false, false
}

// compareSemverApprox returns -1 if a<b, 0 if equal, 1 if a>b for dotted numeric versions.
func compareSemverApprox(a, b string) int {
	pa := strings.Split(strings.TrimPrefix(strings.TrimSpace(a), "v"), ".")
	pb := strings.Split(strings.TrimPrefix(strings.TrimSpace(b), "v"), ".")
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var ai, bi int
		if i < len(pa) {
			ai, _ = strconv.Atoi(digitsPrefix(pa[i]))
		}
		if i < len(pb) {
			bi, _ = strconv.Atoi(digitsPrefix(pb[i]))
		}
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	return 0
}

func digitsPrefix(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return "0"
	}
	return b.String()
}
