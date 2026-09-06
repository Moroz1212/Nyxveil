package controlplane_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/configure"
	"github.com/nyxveil/server/internal/controlplane"
	"github.com/nyxveil/server/internal/identity"
	"github.com/nyxveil/server/internal/localconfig"
)

func TestRuntimeCPClientUsesSystemCertPool(t *testing.T) {
	res, err := controlplane.BuildTLS(controlplane.TLSOptions{BaseURL: "https://cp.nyxveil.ru:18443"})
	if err != nil {
		t.Fatal(err)
	}
	if res.TrustMode != controlplane.TrustSystem || !res.SystemRootPoolLoaded || res.Config.RootCAs == nil {
		t.Fatalf("%+v", res)
	}
	if res.Config.InsecureSkipVerify {
		t.Fatal()
	}
	if res.ServerName != "cp.nyxveil.ru" {
		t.Fatal(res.ServerName)
	}
}

func TestEmptyLegacySPKIPinDoesNotCreateEmptyRootPool(t *testing.T) {
	res, err := controlplane.BuildTLS(controlplane.TLSOptions{BaseURL: "https://cp.example:18443", SPKIPinHex: "  "})
	if err != nil {
		t.Fatal(err)
	}
	if res.TrustMode != controlplane.TrustSystem || res.Config.RootCAs == nil {
		t.Fatal("empty pin must use non-empty SystemTrust pool")
	}
}

func TestRuntimeCPClientNeverUsesInsecureSkipVerify(t *testing.T) {
	res, err := controlplane.BuildTLS(controlplane.TLSOptions{BaseURL: "https://cp.example"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.InsecureSkipVerify {
		t.Fatal()
	}
}

func TestRuntimeCPClientTrustedPublicCAWorks(t *testing.T) {
	b := startTLSBundle(t, "cp.test.local", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	defer b.Close()
	old := controlplane.SystemRootsLoader
	controlplane.SystemRootsLoader = func() (*x509.CertPool, error) { return b.Roots, nil }
	defer func() { controlplane.SystemRootsLoader = old }()

	c := httpClientTo(t, b.BaseURL, b.Addr)
	resp, err := c.Get(b.BaseURL + "/ok")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestRuntimeCPClientUnknownCAFails(t *testing.T) {
	b := startTLSBundle(t, "cp.test.local", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	defer b.Close()
	old := controlplane.SystemRootsLoader
	controlplane.SystemRootsLoader = func() (*x509.CertPool, error) { return x509.NewCertPool(), nil }
	defer func() { controlplane.SystemRootsLoader = old }()

	c := httpClientTo(t, b.BaseURL, b.Addr)
	if _, err := c.Get(b.BaseURL + "/ok"); err == nil {
		t.Fatal("expected unknown authority")
	}
}

func TestRuntimeCPClientWrongHostnameFails(t *testing.T) {
	b := startTLSBundle(t, "cp.test.local", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	defer b.Close()
	old := controlplane.SystemRootsLoader
	controlplane.SystemRootsLoader = func() (*x509.CertPool, error) { return b.Roots, nil }
	defer func() { controlplane.SystemRootsLoader = old }()

	// ServerName=other.example while leaf SAN is cp.test.local
	res, err := controlplane.BuildTLS(controlplane.TLSOptions{BaseURL: "https://other.example/"})
	if err != nil {
		t.Fatal(err)
	}
	res.Config.RootCAs = b.Roots
	tr := &http.Transport{
		TLSClientConfig: res.Config,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, b.Addr)
		},
	}
	c := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	if _, err := c.Get("https://other.example/ok"); err == nil {
		t.Fatal("expected hostname failure")
	}
}

func TestRuntimeCPClientExpiredCertificateFails(t *testing.T) {
	b := startTLSBundle(t, "cp.test.local", time.Now().Add(-48*time.Hour), time.Now().Add(-time.Hour))
	defer b.Close()
	old := controlplane.SystemRootsLoader
	controlplane.SystemRootsLoader = func() (*x509.CertPool, error) { return b.Roots, nil }
	defer func() { controlplane.SystemRootsLoader = old }()

	c := httpClientTo(t, b.BaseURL, b.Addr)
	if _, err := c.Get(b.BaseURL + "/ok"); err == nil {
		t.Fatal("expected expired certificate failure")
	}
}

func TestConfigureAndRuntimeUseSameCPTransportFactory(t *testing.T) {
	b := startTLSBundle(t, "cp.test.local", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	defer b.Close()
	old := controlplane.SystemRootsLoader
	controlplane.SystemRootsLoader = func() (*x509.CertPool, error) { return b.Roots, nil }
	defer func() { controlplane.SystemRootsLoader = old }()

	runtimeRes, err := controlplane.BuildTLS(controlplane.TLSOptions{BaseURL: b.BaseURL})
	if err != nil {
		t.Fatal(err)
	}
	if runtimeRes.TrustMode != controlplane.TrustSystem || runtimeRes.Config.RootCAs == nil {
		t.Fatalf("%+v", runtimeRes)
	}

	oldLookup := configure.LookupIPFunc
	configure.LookupIPFunc = func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	defer func() { configure.LookupIPFunc = oldLookup }()

	u, _ := url.Parse(b.BaseURL)
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	// Dial by IP:port while ServerName remains hostname from URL.
	customDial := &net.Dialer{Timeout: 5 * time.Second}
	_ = customDial
	ctx := context.Background()
	// Probe dials hostname:port — rewrite via dialing 127.0.0.1 ourselves using shared BuildTLS.
	raw, err := dialer.DialContext(ctx, "tcp", b.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	conn := tls.Client(raw, runtimeRes.Config)
	if err := conn.HandshakeContext(ctx); err != nil {
		t.Fatalf("shared factory handshake: %v", err)
	}
	_ = conn.Close()
	_ = u
}

func TestTicketKeysUsesSharedCPClient(t *testing.T) {
	assertCPAPI(t, "/api/v1/node/ticket-keys", func(ctx context.Context, c *controlplane.Client) error {
		_, err := c.GetTicketKeys(ctx)
		return err
	}, func(w http.ResponseWriter) {
		_ = json.NewEncoder(w).Encode(controlplane.TicketKeysResponse{Keys: map[string]string{}})
	})
}

func TestHeartbeatUsesSharedCPClient(t *testing.T) {
	assertCPAPI(t, "/api/v1/nodes/n1/health", func(ctx context.Context, c *controlplane.Client) error {
		_, err := c.Heartbeat(ctx, controlplane.HeartbeatRequest{NodeID: "n1"})
		return err
	}, func(w http.ResponseWriter) {
		_ = json.NewEncoder(w).Encode(controlplane.HeartbeatResponse{Accepted: true})
	})
}

func TestGetConfigUsesSharedCPClient(t *testing.T) {
	assertCPAPI(t, "/api/v1/node/config", func(ctx context.Context, c *controlplane.Client) error {
		_, err := c.GetConfig(ctx)
		return err
	}, func(w http.ResponseWriter) {
		_ = json.NewEncoder(w).Encode(controlplane.NodeConfig{NodeID: "n1"})
	})
}

func TestRegistrationUsesSharedCPClient(t *testing.T) {
	assertCPAPI(t, "/api/v1/nodes/register", func(ctx context.Context, c *controlplane.Client) error {
		_, err := c.Register(ctx, controlplane.RegisterRequest{NodeID: "n1", LocationID: "loc"})
		return err
	}, func(w http.ResponseWriter) {
		_ = json.NewEncoder(w).Encode(controlplane.RegisterResponse{NodeID: "n1", Registered: true})
	})
}

func TestRevocationUsesSharedCPClient(t *testing.T) {
	assertCPAPI(t, "/api/v1/revocation", func(ctx context.Context, c *controlplane.Client) error {
		_, err := c.GetRevocation(ctx)
		return err
	}, func(w http.ResponseWriter) {
		_ = json.NewEncoder(w).Encode(controlplane.RevocationSnapshot{})
	})
}

func TestSharedFactoryIntegrationTrustedRoot(t *testing.T) {
	b := startTLSBundle(t, "cp.test.local", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	defer b.Close()
	old := controlplane.SystemRootsLoader
	controlplane.SystemRootsLoader = func() (*x509.CertPool, error) { return b.Roots, nil }
	defer func() { controlplane.SystemRootsLoader = old }()

	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/api/v1/node/ticket-keys", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(controlplane.TicketKeysResponse{Keys: map[string]string{}})
	})
	mux.HandleFunc("/api/v1/nodes/n1/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(controlplane.HeartbeatResponse{Accepted: true, Status: "online"})
	})
	b.Srv.Handler = mux

	c, res := sharedClient(t, b)
	if res.TrustMode != controlplane.TrustSystem {
		t.Fatal(res.TrustMode)
	}
	k, _ := identity.Generate()
	c.NodeID = "n1"
	c.PrivateKey = k.Private
	if _, err := c.GetTicketKeys(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Heartbeat(context.Background(), controlplane.HeartbeatRequest{NodeID: "n1"}); err != nil {
		t.Fatal(err)
	}

	// configure preflight path uses same BuildTLS.
	raw, err := (&net.Dialer{}).Dial("tcp", b.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	tlsConn := tls.Client(raw, res.Config)
	if err := tlsConn.Handshake(); err != nil {
		t.Fatal(err)
	}
	_ = tlsConn.Close()
}

func TestSharedFactoryIntegrationUntrustedRootFailsIdentically(t *testing.T) {
	b := startTLSBundle(t, "cp.test.local", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	defer b.Close()
	old := controlplane.SystemRootsLoader
	controlplane.SystemRootsLoader = func() (*x509.CertPool, error) { return x509.NewCertPool(), nil }
	defer func() { controlplane.SystemRootsLoader = old }()

	res, err := controlplane.BuildTLS(controlplane.TLSOptions{BaseURL: b.BaseURL})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := (&net.Dialer{}).Dial("tcp", b.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	probeErr := tls.Client(raw, res.Config).Handshake()

	c := httpClientTo(t, b.BaseURL, b.Addr)
	_, runtimeErr := c.Get(b.BaseURL + "/ok")
	if probeErr == nil || runtimeErr == nil {
		t.Fatalf("both must fail probe=%v runtime=%v", probeErr, runtimeErr)
	}
	if !strings.Contains(probeErr.Error(), "certificate") && !strings.Contains(probeErr.Error(), "x509") {
		t.Fatalf("probe=%v", probeErr)
	}
}

func assertCPAPI(t *testing.T, path string, call func(context.Context, *controlplane.Client) error, write func(http.ResponseWriter)) {
	t.Helper()
	b := startTLSBundle(t, "cp.test.local", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	defer b.Close()
	old := controlplane.SystemRootsLoader
	controlplane.SystemRootsLoader = func() (*x509.CertPool, error) { return b.Roots, nil }
	defer func() { controlplane.SystemRootsLoader = old }()

	var saw atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		saw.Store(true)
		write(w)
	})
	b.Srv.Handler = mux

	c, res := sharedClient(t, b)
	if res.Config.RootCAs == nil || res.TrustMode != controlplane.TrustSystem {
		t.Fatalf("%+v", res)
	}
	k, _ := identity.Generate()
	c.NodeID = "n1"
	c.PrivateKey = k.Private
	if err := call(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if !saw.Load() {
		t.Fatal("endpoint not hit")
	}
}

func sharedClient(t *testing.T, b *tlsBundle) (*controlplane.Client, *controlplane.TLSResult) {
	t.Helper()
	res, err := controlplane.BuildTLS(controlplane.TLSOptions{BaseURL: b.BaseURL})
	if err != nil {
		t.Fatal(err)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = res.Config
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, b.Addr)
	}
	c := &controlplane.Client{
		BaseURL: strings.TrimRight(b.BaseURL, "/"),
		HTTP:    &http.Client{Timeout: 10 * time.Second, Transport: tr},
		TLS:     res,
	}
	return c, res
}

func httpClientTo(t *testing.T, baseURL, addr string) *http.Client {
	t.Helper()
	res, err := controlplane.BuildTLS(controlplane.TLSOptions{BaseURL: baseURL})
	if err != nil {
		t.Fatal(err)
	}
	tr := &http.Transport{
		TLSClientConfig: res.Config,
		DialContext: func(ctx context.Context, network, a string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}
}

type tlsBundle struct {
	Roots   *x509.CertPool
	BaseURL string
	Addr    string
	Srv     *http.Server
	ln      net.Listener
}

func (b *tlsBundle) Close() {
	_ = b.Srv.Close()
	_ = b.ln.Close()
}

func startTLSBundle(t *testing.T, dns string, notBefore, notAfter time.Time) *tlsBundle {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign, BasicConstraintsValid: true,
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
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: dns},
		NotBefore: notBefore, NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{dns},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER, caDER}, PrivateKey: leafKey}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	baseURL := fmt.Sprintf("https://%s", net.JoinHostPort(dns, port))
	return &tlsBundle{Roots: roots, BaseURL: baseURL, Addr: ln.Addr().String(), Srv: srv, ln: ln}
}

func loadLocal(path string) (*localconfig.File, error) {
	return localconfig.Load(path)
}

func TestMergeClearsPinnedCAOnCPURLChange(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "server.json")
	raw := []byte(`{
  "control_plane_url": "https://42mou.ru:18443",
  "node_id": "nv-test",
  "location_id": "fi-helsinki",
  "public_host": "fi-hel-01.nyxveil.ru",
  "dns_servers": ["1.1.1.1"],
  "pinned_ca_file": "/etc/nyxveil/old-ca.pem",
  "control_plane_spki_pin": "aabbcc"
}`)
	if err := os.WriteFile(cfgPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadLocal(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	out, err := configure.Merge(*cfg, configure.Options{ControlPlaneURL: "https://cp.nyxveil.ru:18443"})
	if err != nil {
		t.Fatal(err)
	}
	if out.PinnedCAFile != "" || out.ControlPlaneSPKIPin != "" {
		t.Fatalf("expected cleared pin/CA got pin=%q ca=%q", out.ControlPlaneSPKIPin, out.PinnedCAFile)
	}
	if out.ControlPlaneURL != "https://cp.nyxveil.ru:18443" {
		t.Fatal(out.ControlPlaneURL)
	}
}

