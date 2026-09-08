package runtime

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/configure"
	"github.com/nyxveil/server/internal/controlplane"
	"github.com/nyxveil/server/internal/localconfig"
	"github.com/nyxveil/server/internal/nodetls"
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

	dir := tempDir(t)
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
	tlsRes, err := buildControlPlaneTLS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if tlsRes.Config.InsecureSkipVerify {
		t.Fatal("PinnedCA must not set InsecureSkipVerify")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsRes.Config}, Timeout: 5 * time.Second}
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
	badClient := &http.Client{Transport: &http.Transport{TLSClientConfig: badCfg.Config}, Timeout: 5 * time.Second}
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
	tlsRes, err := buildControlPlaneTLS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !tlsRes.Config.InsecureSkipVerify {
		t.Fatal("SelfSignedPinned must skip system chain (pin is trust anchor)")
	}
	if tlsRes.Config.VerifyConnection == nil {
		t.Fatal("expected VerifyConnection")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsRes.Config}, Timeout: 5 * time.Second}
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
	tlsRes, err := buildControlPlaneTLS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsRes.Config}, Timeout: 5 * time.Second}
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
	tlsRes, err := buildControlPlaneTLS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsRes.Config}, Timeout: 5 * time.Second}
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
	tlsRes, err := buildControlPlaneTLS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsRes.Config}, Timeout: 5 * time.Second}
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
	tlsRes, err := buildControlPlaneTLS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsRes.Config}, Timeout: 5 * time.Second}
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
	dir := tempDir(t)
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

func TestRuntimeCPClientUsesSharedFactory(t *testing.T) {
	res, err := buildControlPlaneTLS(&localconfig.File{ControlPlaneURL: "https://cp.nyxveil.ru:18443"})
	if err != nil {
		t.Fatal(err)
	}
	if res.TrustMode != controlplane.TrustSystem {
		t.Fatalf("mode=%s", res.TrustMode)
	}
	if !res.SystemRootPoolLoaded || res.Config.RootCAs == nil {
		t.Fatal("SystemTrust must load SystemCertPool into RootCAs")
	}
	if res.Config.InsecureSkipVerify {
		t.Fatal()
	}
}

func TestRuntimeACMEStagingFailurePreservesLiveTLS(t *testing.T) {
	dir := tempDir(t)
	certFile := filepath.Join(dir, "tls.crt")
	keyFile := filepath.Join(dir, "tls.key")
	if err := generateSelfSigned(certFile, keyFile, "node.example"); err != nil {
		t.Fatal(err)
	}
	beforeCert, _ := os.ReadFile(certFile)
	beforeKey, _ := os.ReadFile(keyFile)

	n := &Node{
		acmeIssuer: func(_ context.Context, cfg nodetls.ACMEConfig) (tls.Certificate, []byte, []byte, bool, error) {
			if cfg.Dest.CertFile == certFile || cfg.Dest.KeyFile == keyFile {
				t.Fatal("ACME issuer received live TLS paths")
			}
			_ = os.WriteFile(cfg.Dest.CertFile, []byte("invalid staged cert"), 0o644)
			return tls.Certificate{}, nil, nil, false, errors.New("simulated issuance failure")
		},
	}
	_, _, _, _, err := n.issueACME(context.Background(), localconfig.File{
		ACMEDomain:  "node.example",
		TLSCertFile: certFile,
		TLSKeyFile:  keyFile,
	})
	if err == nil {
		t.Fatal("expected staged issuance failure")
	}
	afterCert, _ := os.ReadFile(certFile)
	afterKey, _ := os.ReadFile(keyFile)
	if string(afterCert) != string(beforeCert) || string(afterKey) != string(beforeKey) {
		t.Fatal("live TLS changed after staged ACME failure")
	}
}

// Fresh ACME issuance then immediate restart must reuse the live leaf and must
// not open a second ACME order (register + first daemon start path).
func TestRuntimeFreshACMEReuseOnImmediateRestart(t *testing.T) {
	dir := tempDir(t)
	certFile := filepath.Join(dir, "tls.crt")
	keyFile := filepath.Join(dir, "tls.key")
	domain := "fi-hel-02.nyxveil.ru"
	cfg := localconfig.File{
		ACMEDomain:  domain,
		TLSCertFile: certFile,
		TLSKeyFile:  keyFile,
	}

	var orders atomic.Int32
	n := &Node{
		validateStagedTLS: func(cert, key, d string, now time.Time) error {
			return configure.ValidateLeafForDomainOpts(cert, key, d, now, false)
		},
		advertiseSPKI:     func(context.Context, []byte) error { return nil },
		verifyCatalogSPKI: func(context.Context, []byte) error { return nil },
		verifyServedSPKI:  func(context.Context, []byte) error { return nil },
		reloadTLS:         func(tls.Certificate) error { return nil },
		acmeIssuer: func(_ context.Context, acfg nodetls.ACMEConfig) (tls.Certificate, []byte, []byte, bool, error) {
			orders.Add(1)
			if acfg.Dest.CertFile == certFile || acfg.Dest.KeyFile == keyFile {
				t.Fatal("issuer must stage away from live paths")
			}
			priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				return tls.Certificate{}, nil, nil, false, err
			}
			tmpl := &x509.Certificate{
				SerialNumber: big.NewInt(7),
				Subject:      pkix.Name{CommonName: domain},
				NotBefore:    time.Now().Add(-time.Hour),
				NotAfter:     time.Now().Add(60 * 24 * time.Hour),
				KeyUsage:     x509.KeyUsageDigitalSignature,
				ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
				DNSNames:     []string{domain},
			}
			der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
			if err != nil {
				return tls.Certificate{}, nil, nil, false, err
			}
			if err := nodetls.WriteLeafChain(acfg.Dest, [][]byte{der}, priv); err != nil {
				return tls.Certificate{}, nil, nil, false, err
			}
			cert, err := nodetls.Load(acfg.Dest)
			if err != nil {
				return tls.Certificate{}, nil, nil, false, err
			}
			pin, err := nodetls.SPKIPinSHA256(cert)
			if err != nil {
				return tls.Certificate{}, nil, nil, false, err
			}
			return cert, nil, pin, true, nil
		},
	}

	cert1, _, _, _, err := n.issueACME(context.Background(), cfg)
	if err != nil {
		t.Fatalf("first issuance: %v", err)
	}
	if orders.Load() != 1 {
		t.Fatalf("orders=%d want 1 after first issuance", orders.Load())
	}
	if !nodetls.Exists(nodetls.Paths{CertFile: certFile, KeyFile: keyFile}) {
		t.Fatal("live TLS missing after first issuance")
	}
	if len(cert1.Certificate) == 0 {
		t.Fatal("empty certificate after first issuance")
	}

	beforeCert, _ := os.ReadFile(certFile)
	beforeKey, _ := os.ReadFile(keyFile)

	cert2, _, _, changed, err := n.issueACME(context.Background(), cfg)
	if err != nil {
		t.Fatalf("immediate restart reuse: %v", err)
	}
	if changed {
		t.Fatal("reuse must not change SPKI")
	}
	if orders.Load() != 1 {
		t.Fatalf("duplicate ACME order on restart: orders=%d want 1", orders.Load())
	}
	afterCert, _ := os.ReadFile(certFile)
	afterKey, _ := os.ReadFile(keyFile)
	if string(afterCert) != string(beforeCert) || string(afterKey) != string(beforeKey) {
		t.Fatal("reuse must not rewrite live TLS")
	}
	if len(cert2.Certificate) == 0 {
		t.Fatal("empty certificate on reuse")
	}
}
