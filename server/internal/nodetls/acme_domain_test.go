package nodetls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Regression: a still-valid self-signed IP leaf must NOT short-circuit ACME for a new FQDN.
func TestIssueOrRenewDoesNotShortCircuitOnDomainMismatch(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ip := net.ParseIP("46.8.218.27")
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "46.8.218.27"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{ip},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(priv)
	_ = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	_ = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600)

	// Without network ACME this will fail after skipping the early return — that proves
	// we did not return the IP cert as "ready" for the FQDN.
	_, _, _, _, err = IssueOrRenew(context.Background(), ACMEConfig{
		Domain:    "fi-hel-01.nyxveil.ru",
		Email:     "ops@example.com",
		StateDir:  filepath.Join(dir, "acme"),
		Dest:      Paths{CertFile: certPath, KeyFile: keyPath},
		Directory: "https://acme.invalid/directory", // force fail after short-circuit check
		HTTPAddr:  "127.0.0.1:0",
	})
	if err == nil {
		t.Fatal("expected ACME attempt (not early return of IP cert)")
	}
	// Live material still the IP cert (issuance failed before write).
	cert, err := Load(Paths{CertFile: certPath, KeyFile: keyPath})
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	if err := leaf.VerifyHostname("fi-hel-01.nyxveil.ru"); err == nil {
		t.Fatal("live cert unexpectedly covers FQDN — short-circuit may have rewritten")
	}
}
