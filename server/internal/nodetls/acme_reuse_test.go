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
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeDomainLeaf(t *testing.T, certPath, keyPath, domain string, notAfter time.Time) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{domain},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestIssueOrRenewReusesFreshDomainCert(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	domain := "fi-hel-02.nyxveil.ru"
	writeDomainLeaf(t, certPath, keyPath, domain, time.Now().Add(60*24*time.Hour))

	beforeCert, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}

	// Directory URL is invalid on purpose: reuse must short-circuit before any ACME I/O.
	cert, prev, neu, changed, err := IssueOrRenew(context.Background(), ACMEConfig{
		Domain:    domain,
		StateDir:  filepath.Join(dir, "acme"),
		Dest:      Paths{CertFile: certPath, KeyFile: keyPath},
		Directory: "https://acme.invalid/directory",
		HTTPAddr:  "127.0.0.1:0",
	})
	if err != nil {
		t.Fatalf("fresh cert reuse must not contact ACME: %v", err)
	}
	if changed {
		t.Fatal("reuse must not report pin change")
	}
	if len(prev) == 0 || len(neu) == 0 {
		t.Fatal("expected SPKI pins on reuse")
	}
	if string(prev) != string(neu) {
		t.Fatal("reuse pins must match")
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("empty cert on reuse")
	}
	afterCert, _ := os.ReadFile(certPath)
	afterKey, _ := os.ReadFile(keyPath)
	if string(afterCert) != string(beforeCert) || string(afterKey) != string(beforeKey) {
		t.Fatal("reuse must not rewrite live TLS material")
	}
}

func TestIssueOrRenewDoesNotReuseNearExpiry(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	domain := "fi-hel-02.nyxveil.ru"
	// Inside 30-day renewal window → must attempt ACME (and fail on invalid directory).
	writeDomainLeaf(t, certPath, keyPath, domain, time.Now().Add(10*24*time.Hour))

	_, _, _, _, err := IssueOrRenew(context.Background(), ACMEConfig{
		Domain:    domain,
		StateDir:  filepath.Join(dir, "acme"),
		Dest:      Paths{CertFile: certPath, KeyFile: keyPath},
		Directory: "https://acme.invalid/directory",
		HTTPAddr:  "127.0.0.1:0",
	})
	if err == nil {
		t.Fatal("near-expiry leaf must not short-circuit issuance")
	}
}
