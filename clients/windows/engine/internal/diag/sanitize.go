package diag

import (
	"regexp"
	"strings"
)

var (
	reBearer      = regexp.MustCompile(`(?i)(Authorization:\s*Bearer\s+)\S+`)
	reLicenseHdr  = regexp.MustCompile(`(?i)(X-License-Token\s*[:=]\s*)\S+`)
	reNyxLic      = regexp.MustCompile(`(?i)\b(nyx_lic_[A-Za-z0-9_-]+)(:[^\s,"']+)`)
	reAccessTok   = regexp.MustCompile(`(?i)\b(rvpn_access_|nyx_access_|access_ticket\s*[:=]\s*)([A-Za-z0-9._\-+/=]{12,})`)
	reJWT         = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)
	rePrivKeyPEM  = regexp.MustCompile(`(?s)-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----`)
	reHexSecret   = regexp.MustCompile(`(?i)\b(seed|secret|private[_-]?key|device_private_key)\s*[:=]\s*[0-9a-f]{16,}`)
)

// Sanitize redacts common secret patterns from diagnostic text.
func Sanitize(s string) string {
	if s == "" {
		return s
	}
	out := s
	out = rePrivKeyPEM.ReplaceAllString(out, "[REDACTED_PRIVATE_KEY]")
	out = reBearer.ReplaceAllString(out, "${1}[REDACTED]")
	out = reLicenseHdr.ReplaceAllString(out, "${1}[REDACTED]")
	out = reNyxLic.ReplaceAllString(out, "${1}:[REDACTED]")
	out = reAccessTok.ReplaceAllString(out, "${1}[REDACTED]")
	out = reJWT.ReplaceAllString(out, "[REDACTED_JWT]")
	out = reHexSecret.ReplaceAllString(out, "${1}=[REDACTED]")
	// Truncate absurdly long lines (avoid dumping catalogs).
	if len(out) > 2000 {
		out = out[:2000] + "…[truncated]"
	}
	return strings.TrimSpace(out)
}
