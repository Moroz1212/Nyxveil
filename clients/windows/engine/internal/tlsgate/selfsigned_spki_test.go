package tlsgate_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/connector"
	"github.com/nyxveil/nvp/core/controlplane/api"
	"github.com/nyxveil/nvp/core/controlplane/catalog"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/failover"
	"github.com/nyxveil/nvp/core/transport"
	tlsstream "github.com/nyxveil/nvp/core/transport/tlsstream"
)

type certBundle struct {
	Cert       tls.Certificate
	CAPool     *x509.CertPool
	ServerName string
}

func generateCertBundle(serverName string) (*certBundle, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Nyxveil Client TLS Gate CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, err
	}
	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	if serverName == "" {
		serverName = "localhost"
	}
	srvTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: serverName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{serverName, "localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTemplate, caCert, &srvKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	cert := tls.Certificate{Certificate: [][]byte{srvDER, caDER}, PrivateKey: srvKey}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return &certBundle{Cert: cert, CAPool: pool, ServerName: serverName}, nil
}

type systemTrustProvider struct {
	sn  string
	pin []byte
}

func (p *systemTrustProvider) RootCAs() interface{}                         { return nil }
func (p *systemTrustProvider) ServerNameFor(model.NodeRegistryEntry) string { return p.sn }
func (p *systemTrustProvider) PinnedPubKeyFor(model.NodeRegistryEntry) []byte {
	return append([]byte(nil), p.pin...)
}
func (p *systemTrustProvider) ECHPolicy() transport.ECHPolicy { return "" }
func (p *systemTrustProvider) ECHConfigList() []byte          { return nil }

func spkiPin(cert tls.Certificate) []byte {
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil
	}
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return sum[:]
}

func startTLSNode(t *testing.T, bundle *certBundle) (string, func()) {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{bundle.Cert},
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

func mustPort(addr string) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		panic(err)
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		panic(err)
	}
	return port
}

func TestFrozenConnectorSelfSignedWithCorrectSPKIPin(t *testing.T) {
	bundle, err := generateCertBundle("localhost")
	if err != nil {
		t.Fatal(err)
	}
	addr, closeFn := startTLSNode(t, bundle)
	defer closeFn()
	pin := spkiPin(bundle.Cert)
	if len(pin) != 32 {
		t.Fatal("bad pin")
	}

	tr := tlsstream.NewTransport()
	_, err = tr.Dial(context.Background(), transport.DialConfig{
		Endpoint: transport.Endpoint{
			Host: "127.0.0.1", Port: mustPort(addr),
			Profiles: []transport.Profile{transport.ProfileTLSTCP},
		},
		ServerName:   bundle.ServerName,
		RootCAs:      nil,
		PinnedPubKey: pin,
		Timeout:      5 * time.Second,
	})
	if err == nil {
		t.Fatal("UNEXPECTED PASS: untrusted self-signed must not dial via SystemTrust alone")
	}
	msg := err.Error()
	if !strings.Contains(msg, "unknown authority") &&
		!strings.Contains(msg, "certificate is not trusted") &&
		!strings.Contains(strings.ToLower(msg), "x509") &&
		!strings.Contains(strings.ToLower(msg), "certificate") {
		t.Fatalf("expected x509 trust failure before SPKI, got: %v", err)
	}
	t.Logf("BLOCKED_BY_NODE_TLS_TRUST confirmed: %v", err)

	conn, err := tr.Dial(context.Background(), transport.DialConfig{
		Endpoint: transport.Endpoint{
			Host: "127.0.0.1", Port: mustPort(addr),
			Profiles: []transport.Profile{transport.ProfileTLSTCP},
		},
		ServerName:   bundle.ServerName,
		RootCAs:      bundle.CAPool,
		PinnedPubKey: pin,
		Timeout:      5 * time.Second,
	})
	if err != nil {
		t.Fatalf("trusted CA + correct pin must PASS: %v", err)
	}
	_ = conn.Close()
}

func TestFrozenConnectorSelfSignedWrongPinFails(t *testing.T) {
	bundle, err := generateCertBundle("localhost")
	if err != nil {
		t.Fatal(err)
	}
	addr, closeFn := startTLSNode(t, bundle)
	defer closeFn()

	wrong := make([]byte, 32)
	wrong[0] = 0xff
	tr := tlsstream.NewTransport()
	_, err = tr.Dial(context.Background(), transport.DialConfig{
		Endpoint: transport.Endpoint{
			Host: "127.0.0.1", Port: mustPort(addr),
			Profiles: []transport.Profile{transport.ProfileTLSTCP},
		},
		ServerName:   bundle.ServerName,
		RootCAs:      bundle.CAPool,
		PinnedPubKey: wrong,
		Timeout:      5 * time.Second,
	})
	if err == nil {
		t.Fatal("wrong pin must FAIL")
	}
	if !strings.Contains(err.Error(), "pin") {
		t.Fatalf("expected pin mismatch, got: %v", err)
	}
}

func TestFrozenConnectorRequirePinWithSystemTrustDocumentsGate(t *testing.T) {
	bundle, err := generateCertBundle("localhost")
	if err != nil {
		t.Fatal(err)
	}
	addr, closeFn := startTLSNode(t, bundle)
	defer closeFn()
	pin := spkiPin(bundle.Cert)

	pub, priv, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "cat-key-1", PrivateKey: priv}
	signed, err := signer.Sign(model.Catalog{
		Version: "1",
		Nodes: []model.NodeRegistryEntry{{
			NodeID: "n1", LocationID: "fi-hel", Enabled: true, Capacity: 10,
			ServerName: bundle.ServerName, SPKIPin: pin,
			Endpoints: []transport.Endpoint{{
				Host: "127.0.0.1", Port: mustPort(addr),
				Profiles: []transport.Profile{transport.ProfileTLSTCP},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/license/validate", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.LicenseValidateResponse{Valid: true, LicenseID: "lic1"})
	})
	mux.HandleFunc("/api/v1/device/activate", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.DeviceActivateResponse{DeviceID: "dev", Activated: true})
	})
	mux.HandleFunc("/api/v1/ticket/issue", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.TicketIssueResponse{AccessTicket: "a.b.c", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	})
	mux.HandleFunc("/api/v1/catalog", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(signed)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	reg := transport.NewRegistry()
	reg.Register(tlsstream.NewTransport())
	_, privDev, _ := ed25519.GenerateKey(nil)
	c := &connector.Connector{
		CP:                connector.NewControlPlaneClient(ts.URL),
		Registry:          reg,
		RequirePin:        true,
		CatalogVerifyKeys: catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"cat-key-1": pub}},
		Provider:          &systemTrustProvider{sn: bundle.ServerName, pin: pin},
		Policy: failover.ConnectPolicy{
			MaxNodeAttempts: 1,
			TransportRacing: transport.RacingConfig{Primary: transport.ProfileTLSTCP, Fallback: transport.ProfileTLSTCP},
			RetryDelay:      time.Millisecond,
		},
	}
	_, _, _, err = c.OpenSession(context.Background(), connector.ConnectConfig{
		LicenseToken: "lic", DeviceID: "dev", LocationID: "fi-hel",
		DevicePrivateKey: privDev,
	})
	if err == nil {
		t.Fatal("expected transport failure under SystemTrust")
	}
	t.Logf("Connector gate: %v", err)
}
