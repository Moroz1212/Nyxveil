package controlplane

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// TrustMode identifies how Control Plane peer certificates are validated.
type TrustMode string

const (
	TrustSystem        TrustMode = "SystemTrust"
	TrustPinnedCA      TrustMode = "PinnedCA"
	TrustSelfSignedPin TrustMode = "SelfSignedPinned"
)

// TLSOptions configures the shared Control Plane TLS policy.
// Used by nyxveil-server runtime AND nyxveilctl configure preflight.
type TLSOptions struct {
	BaseURL      string // https://host:port
	SPKIPinHex   string // optional leaf SPKI pin (hex SHA-256)
	PinnedCAFile string // optional PEM roots (replaces SystemTrust roots)
}

// TLSResult is the built config plus operator-safe diagnostics (no secrets).
type TLSResult struct {
	Config               *tls.Config
	Host                 string
	ServerName           string
	TrustMode            TrustMode
	SystemRootPoolLoaded bool
	MinVersion           uint16
}

// BuildTLS constructs the single production Control Plane tls.Config.
//
// Trust rules:
//   - PinnedCAFile set → roots from that PEM only (+ optional SPKI pin)
//   - else SPKI pin set → SelfSignedPinned (pin is trust anchor; documented legacy mode)
//   - else SystemTrust → explicit x509.SystemCertPool(); fail closed if unavailable
//
// Empty SPKI pin NEVER creates an empty custom CertPool.
func BuildTLS(opts TLSOptions) (*TLSResult, error) {
	u, err := url.Parse(strings.TrimSpace(opts.BaseURL))
	if err != nil {
		return nil, fmt.Errorf("controlplane: control plane URL: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("controlplane: unsupported scheme %q", u.Scheme)
	}
	serverName := u.Hostname()
	if serverName == "" {
		return nil, fmt.Errorf("controlplane: URL missing host")
	}

	res := &TLSResult{
		Host:       serverName,
		ServerName: serverName,
		MinVersion: tls.VersionTLS12,
		Config: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			ServerName:         serverName,
			InsecureSkipVerify: false,
		},
	}

	pinHex := strings.TrimSpace(opts.SPKIPinHex)
	var wantPin []byte
	if pinHex != "" {
		wantPin, err = hex.DecodeString(pinHex)
		if err != nil {
			return nil, fmt.Errorf("controlplane: control_plane_spki_pin: %w", err)
		}
		if len(wantPin) != sha256.Size {
			return nil, fmt.Errorf("controlplane: control_plane_spki_pin: want %d bytes", sha256.Size)
		}
	}

	if caFile := strings.TrimSpace(opts.PinnedCAFile); caFile != "" {
		pemData, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("controlplane: pinned CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemData) {
			return nil, fmt.Errorf("controlplane: pinned CA: no certificates in %s", caFile)
		}
		res.Config.RootCAs = pool
		res.TrustMode = TrustPinnedCA
		res.SystemRootPoolLoaded = false
		if len(wantPin) > 0 {
			want := append([]byte(nil), wantPin...)
			res.Config.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
				return verifyLeafSPKI(rawCerts, want)
			}
		}
		return res, nil
	}

	if len(wantPin) > 0 {
		// Documented legacy mode: pin is the trust anchor for private/self-signed CP.
		want := append([]byte(nil), wantPin...)
		name := serverName
		res.Config.InsecureSkipVerify = true
		res.Config.VerifyConnection = func(cs tls.ConnectionState) error {
			return verifySelfSignedPinned(cs, name, want, time.Now())
		}
		res.TrustMode = TrustSelfSignedPin
		res.SystemRootPoolLoaded = false
		return res, nil
	}

	// SystemTrust — explicit OS roots; never empty NewCertPool().
	roots, err := loadSystemRoots()
	if err != nil {
		return nil, err
	}
	res.Config.RootCAs = roots
	res.TrustMode = TrustSystem
	res.SystemRootPoolLoaded = true
	return res, nil
}

// SystemRootsLoader is overridable in tests (inject private test roots).
var SystemRootsLoader = func() (*x509.CertPool, error) {
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("controlplane: SystemCertPool failed: %w", err)
	}
	if roots == nil {
		return nil, fmt.Errorf("controlplane: SystemCertPool returned nil")
	}
	return roots, nil
}

func loadSystemRoots() (*x509.CertPool, error) {
	return SystemRootsLoader()
}

// NewClientWithTLS builds a Client using BuildTLS (shared factory).
func NewClientWithTLS(opts TLSOptions) (*Client, *TLSResult, error) {
	tlsRes, err := BuildTLS(opts)
	if err != nil {
		return nil, nil, err
	}
	c, err := NewClient(opts.BaseURL, tlsRes.Config)
	if err != nil {
		return nil, tlsRes, err
	}
	return c, tlsRes, nil
}

// NewHTTPClient builds an http.Client with the shared TLS policy.
func NewHTTPClient(opts TLSOptions, timeout time.Duration) (*http.Client, *TLSResult, error) {
	tlsRes, err := BuildTLS(opts)
	if err != nil {
		return nil, nil, err
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = tlsRes.Config
	return &http.Client{Timeout: timeout, Transport: tr}, tlsRes, nil
}

// LogTLSFailure emits a safe diagnostic line (no secrets).
func LogTLSFailure(res *TLSResult, err error) {
	if err == nil {
		return
	}
	host, sn, mode, roots := "", "", TrustSystem, false
	if res != nil {
		host, sn, mode, roots = res.Host, res.ServerName, res.TrustMode, res.SystemRootPoolLoaded
	}
	log.Printf("runtime: CP TLS failed: host=%s trust=%s system_roots=%v server_name=%s error=%v",
		host, mode, roots, sn, err)
}

// FormatTLSFailure returns the same diagnostic without logging.
func FormatTLSFailure(res *TLSResult, err error) string {
	host, sn, mode, roots := "", "", TrustSystem, false
	if res != nil {
		host, sn, mode, roots = res.Host, res.ServerName, res.TrustMode, res.SystemRootPoolLoaded
	}
	return fmt.Sprintf("host=%s trust=%s system_roots=%v server_name=%s error=%v",
		host, mode, roots, sn, err)
}

func verifyLeafSPKI(rawCerts [][]byte, want []byte) error {
	if len(rawCerts) == 0 {
		return fmt.Errorf("controlplane: empty peer certificate")
	}
	cert, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return err
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	if subtleConstantTimeCompare(sum[:], want) != 1 {
		return fmt.Errorf("controlplane: SPKI pin mismatch")
	}
	return nil
}

func verifySelfSignedPinned(cs tls.ConnectionState, serverName string, wantPin []byte, now time.Time) error {
	if len(cs.PeerCertificates) == 0 {
		return fmt.Errorf("controlplane: empty peer certificate")
	}
	leaf := cs.PeerCertificates[0]
	if now.Before(leaf.NotBefore) {
		return fmt.Errorf("controlplane: certificate not yet valid")
	}
	if now.After(leaf.NotAfter) {
		return fmt.Errorf("controlplane: certificate expired")
	}
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	if subtleConstantTimeCompare(sum[:], wantPin) != 1 {
		return fmt.Errorf("controlplane: SPKI pin mismatch")
	}
	if err := leaf.VerifyHostname(serverName); err != nil {
		return fmt.Errorf("controlplane: hostname: %w", err)
	}
	return leafSuitableForServerAuth(leaf)
}

func leafSuitableForServerAuth(cert *x509.Certificate) error {
	if cert.KeyUsage != 0 {
		ku := cert.KeyUsage
		okKU := ku&x509.KeyUsageDigitalSignature != 0 ||
			ku&x509.KeyUsageKeyEncipherment != 0 ||
			ku&x509.KeyUsageKeyAgreement != 0
		if !okKU {
			return fmt.Errorf("controlplane: certificate KeyUsage not suitable for TLS server")
		}
	}
	if len(cert.ExtKeyUsage) == 0 && len(cert.UnknownExtKeyUsage) == 0 {
		return nil
	}
	for _, eku := range cert.ExtKeyUsage {
		if eku == x509.ExtKeyUsageAny || eku == x509.ExtKeyUsageServerAuth {
			return nil
		}
	}
	return fmt.Errorf("controlplane: certificate ExtKeyUsage missing ServerAuth")
}

func subtleConstantTimeCompare(a, b []byte) int {
	if len(a) != len(b) {
		return 0
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	if v == 0 {
		return 1
	}
	return 0
}
