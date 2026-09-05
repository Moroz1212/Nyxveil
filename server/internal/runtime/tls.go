package runtime

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/nyxveil/server/internal/localconfig"
)

// TLS trust modes for Control Plane HTTPS (no TrustAll):
//
//  1. SystemTrust — default public CA roots (no pin, no pinned CA file).
//  2. PinnedCA — explicit PEM root(s) via PinnedCAFile; optional SPKI pin on leaf.
//  3. SelfSignedPinned — ControlPlaneSPKIPin only: pin is the trust anchor.
//     System/public CA chain is NOT required. Still validates SPKI, hostname/SAN,
//     NotBefore/NotAfter, and leaf suitability for server auth.
func buildControlPlaneTLS(cfg *localconfig.File) (*tls.Config, error) {
	if cfg == nil {
		return &tls.Config{MinVersion: tls.VersionTLS12}, nil
	}
	u, err := url.Parse(strings.TrimSpace(cfg.ControlPlaneURL))
	if err != nil {
		return nil, fmt.Errorf("runtime: control plane URL: %w", err)
	}
	serverName := u.Hostname()
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: serverName,
	}

	pinHex := strings.TrimSpace(cfg.ControlPlaneSPKIPin)
	var wantPin []byte
	if pinHex != "" {
		wantPin, err = hex.DecodeString(pinHex)
		if err != nil {
			return nil, fmt.Errorf("runtime: control_plane_spki_pin: %w", err)
		}
		if len(wantPin) != sha256.Size {
			return nil, fmt.Errorf("runtime: control_plane_spki_pin: want %d bytes", sha256.Size)
		}
	}

	if cfg.PinnedCAFile != "" {
		// Mode: PinnedCA (+ optional leaf SPKI pin).
		pemData, err := os.ReadFile(cfg.PinnedCAFile)
		if err != nil {
			return nil, fmt.Errorf("runtime: pinned CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemData) {
			return nil, fmt.Errorf("runtime: pinned CA: no certificates in %s", cfg.PinnedCAFile)
		}
		tlsCfg.RootCAs = pool
		if len(wantPin) > 0 {
			tlsCfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
				return verifyLeafSPKI(rawCerts, wantPin)
			}
		}
		return tlsCfg, nil
	}

	if len(wantPin) > 0 {
		// Mode: SelfSignedPinned — pin is the trust anchor; skip public CA chain.
		want := append([]byte(nil), wantPin...)
		name := serverName
		tlsCfg.InsecureSkipVerify = true // custom VerifyConnection is the trust path
		tlsCfg.VerifyConnection = func(cs tls.ConnectionState) error {
			return verifySelfSignedPinned(cs, name, want, time.Now())
		}
		return tlsCfg, nil
	}

	// Mode: SystemTrust
	return tlsCfg, nil
}

func verifyLeafSPKI(rawCerts [][]byte, want []byte) error {
	if len(rawCerts) == 0 {
		return fmt.Errorf("runtime: empty peer certificate")
	}
	cert, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return err
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	if subtleConstantTimeCompare(sum[:], want) != 1 {
		return fmt.Errorf("runtime: SPKI pin mismatch")
	}
	return nil
}

func verifySelfSignedPinned(cs tls.ConnectionState, serverName string, wantPin []byte, now time.Time) error {
	if len(cs.PeerCertificates) == 0 {
		return fmt.Errorf("runtime: empty peer certificate")
	}
	leaf := cs.PeerCertificates[0]

	if now.Before(leaf.NotBefore) {
		return fmt.Errorf("runtime: certificate not yet valid")
	}
	if now.After(leaf.NotAfter) {
		return fmt.Errorf("runtime: certificate expired")
	}

	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	if subtleConstantTimeCompare(sum[:], wantPin) != 1 {
		return fmt.Errorf("runtime: SPKI pin mismatch")
	}

	if err := leaf.VerifyHostname(serverName); err != nil {
		return fmt.Errorf("runtime: hostname: %w", err)
	}

	if err := leafSuitableForServerAuth(leaf); err != nil {
		return err
	}
	return nil
}

func leafSuitableForServerAuth(cert *x509.Certificate) error {
	if cert.KeyUsage != 0 {
		ku := cert.KeyUsage
		okKU := ku&x509.KeyUsageDigitalSignature != 0 ||
			ku&x509.KeyUsageKeyEncipherment != 0 ||
			ku&x509.KeyUsageKeyAgreement != 0
		if !okKU {
			return fmt.Errorf("runtime: certificate KeyUsage not suitable for TLS server")
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
	return fmt.Errorf("runtime: certificate ExtKeyUsage missing ServerAuth")
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
