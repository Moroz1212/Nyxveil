package nodetls_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/transport"
	tlsstream "github.com/nyxveil/nvp/core/transport/tlsstream"
	"github.com/nyxveil/server/internal/nodetls"
)

type caBundle struct {
	leaf   tls.Certificate
	caPool *x509.CertPool
	name   string
}

func issueTrustedLeaf(t *testing.T, dnsName string) caBundle {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Nyxveil Test Public CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: dnsName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{dnsName},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return caBundle{
		leaf: tls.Certificate{
			Certificate: [][]byte{leafDER, caDER},
			PrivateKey:  leafKey,
		},
		caPool: pool,
		name:   dnsName,
	}
}

func spki(cert tls.Certificate) []byte {
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil
	}
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return sum[:]
}

func serveTLS(t *testing.T, cert tls.Certificate) (addr string, closeFn func()) {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.(*tls.Conn).Handshake()
			_ = c.Close()
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close(); <-done }
}

func dial(t *testing.T, hostPort, serverName string, roots *x509.CertPool, pin []byte) error {
	t.Helper()
	host, portStr, _ := net.SplitHostPort(hostPort)
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	tr := tlsstream.NewTransport()
	conn, err := tr.Dial(context.Background(), transport.DialConfig{
		Endpoint:     transport.Endpoint{Host: host, Port: port, Profiles: []transport.Profile{transport.ProfileTLSTCP}},
		ServerName:   serverName,
		RootCAs:      roots,
		PinnedPubKey: pin,
		Timeout:      5 * time.Second,
	})
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

// TestTrustedCertHostnameCorrectSPKIPass models SystemTrust via RootCAs=issuing CA
// (publicly trusted leaf path) + correct hostname + correct SPKI.
func TestTrustedCertHostnameCorrectSPKIPass(t *testing.T) {
	b := issueTrustedLeaf(t, "vpn.example.test")
	addr, closeFn := serveTLS(t, b.leaf)
	defer closeFn()
	pin := spki(b.leaf)
	if err := dial(t, addr, b.name, b.caPool, pin); err != nil {
		t.Fatalf("expected PASS: %v", err)
	}
}

func TestTrustedCertWrongSPKIFail(t *testing.T) {
	b := issueTrustedLeaf(t, "vpn.example.test")
	addr, closeFn := serveTLS(t, b.leaf)
	defer closeFn()
	wrong := make([]byte, 32)
	wrong[0] = 0xaa
	err := dial(t, addr, b.name, b.caPool, wrong)
	if err == nil {
		t.Fatal("expected FAIL on wrong SPKI")
	}
	if !strings.Contains(err.Error(), "pin") {
		t.Fatalf("want pin error, got %v", err)
	}
}

func TestSelfSignedCorrectSPKIFail(t *testing.T) {
	dir := t.TempDir()
	p := nodetls.Paths{CertFile: filepath.Join(dir, "tls.crt"), KeyFile: filepath.Join(dir, "tls.key")}
	if err := nodetls.GenerateSelfSignedDev(p, "localhost", true); err != nil {
		t.Fatal(err)
	}
	cert, err := nodetls.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	addr, closeFn := serveTLS(t, cert)
	defer closeFn()
	pin, err := nodetls.SPKIPinSHA256(cert)
	if err != nil {
		t.Fatal(err)
	}
	// RootCAs=nil → SystemTrust only (self-signed must fail before SPKI).
	err = dial(t, addr, "localhost", nil, pin)
	if err == nil {
		t.Fatal("expected FAIL for self-signed under SystemTrust")
	}
	if !strings.Contains(err.Error(), "unknown authority") &&
		!strings.Contains(strings.ToLower(err.Error()), "certificate") {
		t.Fatalf("expected x509 trust failure, got %v", err)
	}
}

func TestStableKeyPreservesSPKIAcrossRewrite(t *testing.T) {
	dir := t.TempDir()
	p := nodetls.Paths{CertFile: filepath.Join(dir, "tls.crt"), KeyFile: filepath.Join(dir, "tls.key")}
	if err := nodetls.GenerateSelfSignedDev(p, "a.example", true); err != nil {
		t.Fatal(err)
	}
	c1, _ := nodetls.Load(p)
	pin1, _ := nodetls.SPKIPinSHA256(c1)
	// Rewrite cert with same key file (stable key) — SPKI must match.
	key, err := nodetls.LoadOrCreateStableKey(p.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(9),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"b.example"},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	if err := nodetls.WriteLeafChain(p, [][]byte{der}, key); err != nil {
		t.Fatal(err)
	}
	c2, _ := nodetls.Load(p)
	pin2, _ := nodetls.SPKIPinSHA256(c2)
	if nodetls.PinChanged(pin1, pin2) {
		t.Fatal("SPKI changed despite stable private key")
	}
}

func TestInstallOperatorDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	dest := nodetls.Paths{CertFile: filepath.Join(dir, "tls.crt"), KeyFile: filepath.Join(dir, "tls.key")}
	if err := nodetls.GenerateSelfSignedDev(dest, "keep.example", true); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(dest.CertFile)
	srcDir := t.TempDir()
	src := nodetls.Paths{CertFile: filepath.Join(srcDir, "a.crt"), KeyFile: filepath.Join(srcDir, "a.key")}
	if err := nodetls.GenerateSelfSignedDev(src, "new.example", true); err != nil {
		t.Fatal(err)
	}
	if err := nodetls.InstallOperator(src.CertFile, src.KeyFile, dest, false); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(dest.CertFile)
	if string(before) != string(after) {
		t.Fatal("operator install overwrote existing cert without replace")
	}
}

func TestOperatorInstallReplace(t *testing.T) {
	dir := t.TempDir()
	dest := nodetls.Paths{CertFile: filepath.Join(dir, "tls.crt"), KeyFile: filepath.Join(dir, "tls.key")}
	_ = nodetls.GenerateSelfSignedDev(dest, "old.example", true)
	srcDir := t.TempDir()
	src := nodetls.Paths{CertFile: filepath.Join(srcDir, "a.crt"), KeyFile: filepath.Join(srcDir, "a.key")}
	_ = nodetls.GenerateSelfSignedDev(src, "new.example", true)
	if err := nodetls.InstallOperator(src.CertFile, src.KeyFile, dest, true); err != nil {
		t.Fatal(err)
	}
	cert, err := nodetls.Load(dest)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	if leaf.DNSNames[0] != "new.example" {
		t.Fatalf("%v", leaf.DNSNames)
	}
}
