package runtime

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/localconfig"
)

func TestListenPort(t *testing.T) {
	if listenPort(":8443", 443) != 8443 {
		t.Fatal()
	}
	if listenPort("", 443) != 443 {
		t.Fatal()
	}
	if listenPort("0.0.0.0:9443", 443) != 9443 {
		t.Fatal()
	}
}

func TestParseTransportPolicy(t *testing.T) {
	tlsOn, quicOn := parseTransportPolicy(`{"tls":true,"quic":false}`)
	if !tlsOn || quicOn {
		t.Fatalf("%v %v", tlsOn, quicOn)
	}
	tlsOn, quicOn = parseTransportPolicy(`{"profiles":["quic"]}`)
	if tlsOn || !quicOn {
		t.Fatalf("%v %v", tlsOn, quicOn)
	}
	tlsOn, quicOn = parseTransportPolicy("")
	if !tlsOn || !quicOn {
		t.Fatal("defaults")
	}
}

func TestParseECHPolicy(t *testing.T) {
	s := `{"mode":"require"}`
	req, keys := parseECHPolicy(&s)
	if !req || !keys {
		t.Fatal()
	}
	s2 := `{"preferred":true}`
	req, keys = parseECHPolicy(&s2)
	if req || !keys {
		t.Fatal()
	}
	req, keys = parseECHPolicy(nil)
	if req || keys {
		t.Fatal()
	}
}

func TestCompareSemverApprox(t *testing.T) {
	if compareSemverApprox("1.0.0", "1.0.1") >= 0 {
		t.Fatal()
	}
	if compareSemverApprox("1.2.0", "1.1.9") <= 0 {
		t.Fatal()
	}
}

func TestBuildControlPlaneTLS_PinnedCAAndSPKI(t *testing.T) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
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
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leafCert, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatal(err)
	}
	pin := sha256.Sum256(leafCert.RawSubjectPublicKeyInfo)

	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o644); err != nil {
		t.Fatal(err)
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{leafDER, caDER},
			PrivateKey:  leafKey,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	cfg := &localconfig.File{
		ControlPlaneURL:     "https://127.0.0.1/",
		PinnedCAFile:        caPath,
		ControlPlaneSPKIPin: hex.EncodeToString(pin[:]),
	}
	tlsCfg, err := buildControlPlaneTLS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if tlsCfg.InsecureSkipVerify {
		t.Fatal("PinnedCA must not set InsecureSkipVerify")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}, Timeout: 5 * time.Second}
	resp, err := client.Get("https://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	cfg.ControlPlaneSPKIPin = hex.EncodeToString(make([]byte, 32))
	badCfg, err := buildControlPlaneTLS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	badClient := &http.Client{Transport: &http.Transport{TLSClientConfig: badCfg}, Timeout: 5 * time.Second}
	if _, err := badClient.Get("https://" + ln.Addr().String() + "/"); err == nil {
		t.Fatal("expected SPKI pin failure")
	}
}

func TestSelfSignedPinned_CorrectPinPassesWithoutSystemTrust(t *testing.T) {
	ln, pin, cleanup := startSelfSignedTLS(t, "cp.test.local", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	defer cleanup()

	cfg := &localconfig.File{
		ControlPlaneURL:     "https://cp.test.local/",
		ControlPlaneSPKIPin: pin,
	}
	tlsCfg, err := buildControlPlaneTLS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !tlsCfg.InsecureSkipVerify {
		t.Fatal("SelfSignedPinned must skip system chain (pin is trust anchor)")
	}
	if tlsCfg.VerifyConnection == nil {
		t.Fatal("expected VerifyConnection")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}, Timeout: 5 * time.Second}
	resp, err := client.Get("https://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestSelfSignedPinned_WithoutPinFails(t *testing.T) {
	ln, _, cleanup := startSelfSignedTLS(t, "cp.test.local", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	defer cleanup()

	cfg := &localconfig.File{ControlPlaneURL: "https://cp.test.local/"}
	tlsCfg, err := buildControlPlaneTLS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}, Timeout: 5 * time.Second}
	if _, err := client.Get("https://" + ln.Addr().String() + "/"); err == nil {
		t.Fatal("system trust must reject self-signed without pin")
	}
}

func TestSelfSignedPinned_WrongPinFails(t *testing.T) {
	ln, _, cleanup := startSelfSignedTLS(t, "cp.test.local", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	defer cleanup()

	cfg := &localconfig.File{
		ControlPlaneURL:     "https://cp.test.local/",
		ControlPlaneSPKIPin: hex.EncodeToString(make([]byte, 32)),
	}
	tlsCfg, err := buildControlPlaneTLS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}, Timeout: 5 * time.Second}
	if _, err := client.Get("https://" + ln.Addr().String() + "/"); err == nil {
		t.Fatal("expected wrong SPKI failure")
	}
}

func TestSelfSignedPinned_WrongHostnameFails(t *testing.T) {
	ln, pin, cleanup := startSelfSignedTLS(t, "cp.test.local", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	defer cleanup()

	cfg := &localconfig.File{
		ControlPlaneURL:     "https://other.example/",
		ControlPlaneSPKIPin: pin,
	}
	tlsCfg, err := buildControlPlaneTLS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}, Timeout: 5 * time.Second}
	if _, err := client.Get("https://" + ln.Addr().String() + "/"); err == nil {
		t.Fatal("expected hostname failure")
	}
}

func TestSelfSignedPinned_ExpiredFails(t *testing.T) {
	ln, pin, cleanup := startSelfSignedTLS(t, "cp.test.local", time.Now().Add(-48*time.Hour), time.Now().Add(-time.Hour))
	defer cleanup()

	cfg := &localconfig.File{
		ControlPlaneURL:     "https://cp.test.local/",
		ControlPlaneSPKIPin: pin,
	}
	tlsCfg, err := buildControlPlaneTLS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}, Timeout: 5 * time.Second}
	if _, err := client.Get("https://" + ln.Addr().String() + "/"); err == nil {
		t.Fatal("expected expired certificate failure")
	}
}

func startSelfSignedTLS(t *testing.T, dnsName string, notBefore, notAfter time.Time) (net.Listener, string, func()) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: dnsName},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{dnsName},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pin := sha256.Sum256(cert.RawSubjectPublicKeyInfo)

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{der},
			PrivateKey:  key,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	return ln, hex.EncodeToString(pin[:]), func() { _ = ln.Close() }
}

func TestGenerateSelfSignedIPUsesIPAddresses(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "tls.crt")
	keyFile := filepath.Join(dir, "tls.key")
	if err := generateSelfSigned(certFile, keyFile, "203.0.113.10"); err != nil {
		t.Fatal(err)
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.DNSNames) != 0 {
		t.Fatalf("DNSNames=%v", parsed.DNSNames)
	}
	if len(parsed.IPAddresses) != 1 || parsed.IPAddresses[0].String() != "203.0.113.10" {
		t.Fatalf("IPAddresses=%v", parsed.IPAddresses)
	}
}

func TestHeartbeatBackoffIncreases(t *testing.T) {
	base := time.Second
	d1 := heartbeatBackoff(1, base)
	d3 := heartbeatBackoff(3, base)
	if d3 < d1 {
		t.Fatalf("expected growth %v vs %v", d3, d1)
	}
}
