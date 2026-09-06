package configure

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nyxveil/server/internal/nodetls"
	"github.com/nyxveil/server/internal/paths"
)

// CertInfo is operator-safe TLS status (no private key).
type CertInfo struct {
	Present    bool     `json:"present"`
	Subject    string   `json:"subject,omitempty"`
	Issuer     string   `json:"issuer,omitempty"`
	SANs       []string `json:"sans,omitempty"`
	NotBefore  string   `json:"not_before,omitempty"`
	NotAfter   string   `json:"not_after,omitempty"`
	SPKIHex    string   `json:"spki_sha256,omitempty"`
	Thumbprint string   `json:"cert_thumbprint,omitempty"`
	TLSMode    string   `json:"tls_mode,omitempty"` // acme|operator|self-signed|missing
	ACMEDomain string   `json:"acme_domain,omitempty"`
}

// ValidateLeafForDomain checks parse, key match (via Load), validity window, SAN, ServerAuth.
// When requireSystemTrust is true, also verifies against the system trust store (no InsecureSkipVerify).
func ValidateLeafForDomain(certPath, keyPath, domain string, now time.Time) error {
	return ValidateLeafForDomainOpts(certPath, keyPath, domain, now, true)
}

// ValidateLeafForDomainOpts is ValidateLeafForDomain with optional system-trust gate.
func ValidateLeafForDomainOpts(certPath, keyPath, domain string, now time.Time, requireSystemTrust bool) error {
	cert, err := nodetls.Load(nodetls.Paths{CertFile: certPath, KeyFile: keyPath})
	if err != nil {
		return fmt.Errorf("configure: load TLS material: %w", err)
	}
	if len(cert.Certificate) == 0 {
		return fmt.Errorf("configure: empty certificate")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return fmt.Errorf("configure: parse leaf: %w", err)
	}
	if now.IsZero() {
		now = time.Now()
	}
	if now.Before(leaf.NotBefore) {
		return fmt.Errorf("configure: certificate not yet valid (NotBefore=%s)", leaf.NotBefore.UTC().Format(time.RFC3339))
	}
	if now.After(leaf.NotAfter) {
		return fmt.Errorf("configure: certificate expired (NotAfter=%s)", leaf.NotAfter.UTC().Format(time.RFC3339))
	}
	domain = strings.TrimSpace(domain)
	if domain != "" {
		if err := leaf.VerifyHostname(domain); err != nil {
			return fmt.Errorf("configure: SAN/hostname mismatch for %q: %w", domain, err)
		}
	}
	if err := leafHasServerAuth(leaf); err != nil {
		return err
	}
	if !requireSystemTrust {
		return nil
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return fmt.Errorf("configure: SystemCertPool failed: %w", err)
	}
	if roots == nil {
		return fmt.Errorf("configure: SystemCertPool returned nil")
	}
	inter := x509.NewCertPool()
	for i := 1; i < len(cert.Certificate); i++ {
		if c, e := x509.ParseCertificate(cert.Certificate[i]); e == nil {
			inter.AddCert(c)
		}
	}
	opts := x509.VerifyOptions{
		DNSName:       domain,
		Roots:         roots,
		Intermediates: inter,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if _, err := leaf.Verify(opts); err != nil {
		return fmt.Errorf("configure: system trust verification failed: %w", err)
	}
	return nil
}

func leafHasServerAuth(leaf *x509.Certificate) error {
	if len(leaf.ExtKeyUsage) == 0 && len(leaf.UnknownExtKeyUsage) == 0 {
		// Many CAs omit EKU; require either empty (unlimited) or ServerAuth present.
		return nil
	}
	for _, eku := range leaf.ExtKeyUsage {
		if eku == x509.ExtKeyUsageAny || eku == x509.ExtKeyUsageServerAuth {
			return nil
		}
	}
	return fmt.Errorf("configure: certificate ExtKeyUsage missing ServerAuth")
}

// CurrentSPKIHex returns leaf SPKI SHA-256 hex or empty if missing.
func CurrentSPKIHex(certPath, keyPath string) (string, error) {
	if !nodetls.Exists(nodetls.Paths{CertFile: certPath, KeyFile: keyPath}) {
		return "", nil
	}
	cert, err := nodetls.Load(nodetls.Paths{CertFile: certPath, KeyFile: keyPath})
	if err != nil {
		return "", err
	}
	pin, err := nodetls.SPKIPinSHA256(cert)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(pin), nil
}

// InspectCert builds CertInfo for status output.
func InspectCert(certPath, keyPath, acmeDomain string) CertInfo {
	info := CertInfo{ACMEDomain: acmeDomain}
	if acmeDomain != "" {
		info.TLSMode = "acme"
	}
	if !nodetls.Exists(nodetls.Paths{CertFile: certPath, KeyFile: keyPath}) {
		info.TLSMode = "missing"
		return info
	}
	cert, err := nodetls.Load(nodetls.Paths{CertFile: certPath, KeyFile: keyPath})
	if err != nil {
		info.TLSMode = "unreadable"
		return info
	}
	info.Present = true
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return info
	}
	info.Subject = leaf.Subject.String()
	info.Issuer = leaf.Issuer.String()
	info.SANs = append([]string{}, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		info.SANs = append(info.SANs, ip.String())
	}
	info.NotBefore = leaf.NotBefore.UTC().Format(time.RFC3339)
	info.NotAfter = leaf.NotAfter.UTC().Format(time.RFC3339)
	if pin, err := nodetls.SPKIPinSHA256(cert); err == nil {
		info.SPKIHex = hex.EncodeToString(pin)
	}
	sum := sha256.Sum256(leaf.Raw)
	info.Thumbprint = hex.EncodeToString(sum[:])
	if info.TLSMode == "" {
		info.TLSMode = "operator"
	}
	// Heuristic: self-signed if issuer == subject and not in system trust.
	if leaf.Issuer.String() == leaf.Subject.String() {
		info.TLSMode = "self-signed"
	}
	return info
}

// DefaultTLSPaths returns state-dir cert/key, honoring cfg overrides.
func DefaultTLSPaths(certFile, keyFile string) (string, string) {
	c, k := certFile, keyFile
	if c == "" {
		c = paths.TLSCert()
	}
	if k == "" {
		k = paths.TLSKey()
	}
	return c, k
}

// InstallOperatorTLS copies operator material with validation.
func InstallOperatorTLS(srcCert, srcKey, destCert, destKey string, replace bool) error {
	return nodetls.InstallOperator(srcCert, srcKey, nodetls.Paths{CertFile: destCert, KeyFile: destKey}, replace)
}

// EnsureOwnerReadable is a no-op placeholder; ownership is enforced on Linux by install helpers.
func EnsureOwnerReadable(certPath, keyPath string) error {
	if st, err := os.Stat(certPath); err == nil && st.Mode().Perm()&0o044 == 0 {
		_ = os.Chmod(certPath, 0o644)
	}
	if st, err := os.Stat(keyPath); err == nil && st.Mode().Perm()&0o077 != 0 {
		_ = os.Chmod(keyPath, 0o600)
	}
	return nil
}
