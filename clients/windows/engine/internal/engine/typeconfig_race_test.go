package engine

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/nyxveil/client-windows/internal/netcfg"
	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/authhandler"
	"github.com/nyxveil/nvp/core/control"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
	tlsstream "github.com/nyxveil/nvp/core/transport/tlsstream"
)

// sessionSendControl mirrors server/internal/netcfg SendConfig shim so the
// integration test can push TypeConfig exactly like production server-v1.1.6.
//
//go:linkname sessionSendControl github.com/nyxveil/nvp/core/session.(*Session).sendControl
func sessionSendControl(s *session.Session, ctx context.Context, msgType byte, payload []byte) error

var _ = unsafe.Sizeof(0)

func sendTypeConfigLikeServer(ctx context.Context, sess *session.Session, msg netcfg.Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return sessionSendControl(sess, ctx, control.TypeConfig, payload)
}

type raceCertBundle struct {
	Cert       tls.Certificate
	CAPool     *x509.CertPool
	ServerName string
}

func generateRaceCertBundle(t *testing.T) *raceCertBundle {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "NVP Race CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srvTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTemplate, caCert, &srvKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(srvDER)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return &raceCertBundle{
		Cert: tls.Certificate{
			Certificate: [][]byte{srvDER, caDER},
			PrivateKey:  srvKey,
			Leaf:        leaf,
		},
		CAPool:     pool,
		ServerName: "localhost",
	}
}

type typeConfigServerHarness struct {
	bundle  *raceCertBundle
	tok     string
	devPriv ed25519.PrivateKey
	ln      transport.Listener
	closeFn func()
}

func startTypeConfigPushServer(t *testing.T, pushConfig bool) *typeConfigServerHarness {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	devPub, devPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	issuer := ticket.IssuerConfig{
		Issuer:     "https://control.nyxveil.test",
		Audience:   "nvp-node",
		KeyID:      "cp-key-1",
		PrivateKey: priv,
		TTL:        15 * time.Minute,
	}
	tok, err := ticket.IssueScoped(issuer, "lic_test", "dev_test", "user", "premium",
		[]string{"connect"}, []string{"fi"}, []string{"fi-hel-01"}, devPub)
	if err != nil {
		t.Fatal(err)
	}
	verifier := ticket.VerifierConfig{
		Issuer:     issuer.Issuer,
		Audience:   issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
		Revoked:    ticket.NewMemoryRevocation(),
	}
	bundle := generateRaceCertBundle(t)
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{bundle.Cert},
		MinVersion:   tls.VersionTLS13,
	}
	ln, err := tlsstream.NewTransport().Listen(context.Background(), "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	authHandler := authhandler.NewAuthHandler("fi-hel-01", "fi", verifier)
	acceptCtx, acceptCancel := context.WithCancel(context.Background())
	go func() {
		for {
			conn, err := ln.Accept(acceptCtx)
			if err != nil {
				return
			}
			go func(c transport.Conn) {
				defer c.Close()
				sess := session.New(session.DefaultConfig(false))
				sess.OnControl(func(msgType byte, payload []byte) error {
					if msgType != control.TypeAuth {
						return nil
					}
					if err := authHandler.HandleAuth(acceptCtx, sess, payload); err != nil {
						return err
					}
					// Production server-v1.1.6: SendConfig immediately after AUTH_OK,
					// before the client's WaitEstablished temp reader has exited.
					if !pushConfig {
						return nil
					}
					msg := netcfg.Message{
						VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1420,
						Gateway: "10.66.0.1", DNSServers: []string{"203.0.113.53"},
					}
					return sendTypeConfigLikeServer(context.Background(), sess, msg)
				})
				if err := sess.Connect(acceptCtx, c); err != nil {
					return
				}
				if err := sess.RunHandshake(acceptCtx); err != nil {
					return
				}
				_ = sess.ReadLoop(acceptCtx)
			}(conn)
		}
	}()
	h := &typeConfigServerHarness{
		bundle: bundle, tok: tok, devPriv: devPriv, ln: ln,
		closeFn: func() { acceptCancel(); _ = ln.Close() },
	}
	t.Cleanup(h.closeFn)
	return h
}

func (h *typeConfigServerHarness) dialClient(t *testing.T) (*session.Session, transport.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	host, portStr, err := net.SplitHostPort(h.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := tlsstream.NewTransport().Dial(ctx, transport.DialConfig{
		Endpoint:   transport.Endpoint{Host: host, Port: port},
		ServerName: h.bundle.ServerName,
		RootCAs:    h.bundle.CAPool,
		Timeout:    5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	sess := session.New(session.DefaultConfig(true))
	if err := sess.Connect(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if err := sess.RunHandshake(ctx); err != nil {
		t.Fatal(err)
	}
	return sess, conn
}

// TestTypeConfigDroppedDuringWaitEstablishedWithoutCatcher reproduces the live
// Windows E2E failure: AUTH_OK then immediate TypeConfig while OnControl is nil
// → frame discarded → waitTypeConfig times out.
func TestTypeConfigDroppedDuringWaitEstablishedWithoutCatcher(t *testing.T) {
	h := startTypeConfigPushServer(t, true)
	sess, conn := h.dialClient(t)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	authBody, err := ticket.EncodeAuthPayload(h.tok, sess.Transcript(), h.devPriv)
	if err != nil {
		t.Fatal(err)
	}
	// Intentionally NO armTypeConfigCatcher — legacy buggy path.
	if err := sess.SendAuth(ctx, authBody); err != nil {
		t.Fatal(err)
	}
	if err := sess.WaitEstablished(ctx); err != nil {
		t.Fatal(err)
	}
	if sess.State() != session.StateEstablished {
		t.Fatalf("state=%s", sess.State())
	}

	mgr := NewManager(Options{})
	mgr.ConfigWait = 400 * time.Millisecond
	_, err = mgr.waitTypeConfig(context.Background(), sess, conn)
	if err == nil {
		t.Fatal("expected TypeConfig timeout when catcher was not armed before WaitEstablished")
	}
	if !strings.Contains(err.Error(), "TypeConfig timeout") {
		t.Fatalf("err=%v want TypeConfig timeout", err)
	}
}

// TestTypeConfigReceivedWhenCatcherArmedBeforeWaitEstablished is the fixed path:
// arm catcher → AUTH → WaitEstablished → TypeConfig already buffered → no timeout.
func TestTypeConfigReceivedWhenCatcherArmedBeforeWaitEstablished(t *testing.T) {
	h := startTypeConfigPushServer(t, true)
	sess, conn := h.dialClient(t)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	authBody, err := ticket.EncodeAuthPayload(h.tok, sess.Transcript(), h.devPriv)
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(Options{})
	mgr.ConfigWait = 2 * time.Second
	mgr.armTypeConfigCatcher(sess)
	if err := sess.SendAuth(ctx, authBody); err != nil {
		t.Fatal(err)
	}
	if err := sess.WaitEstablished(ctx); err != nil {
		t.Fatal(err)
	}

	msg, err := mgr.waitTypeConfig(context.Background(), sess, conn)
	if err != nil {
		t.Fatalf("waitTypeConfig: %v", err)
	}
	if msg.VPNIP != "10.66.0.2" || msg.VPNPrefix != 24 || msg.Gateway != "10.66.0.1" {
		t.Fatalf("bad netcfg: %+v", msg)
	}
	if len(msg.DNSServers) == 0 || msg.DNSServers[0] != "203.0.113.53" {
		t.Fatalf("bad dns: %+v", msg.DNSServers)
	}
	if msg.MTU != 1420 {
		t.Fatalf("mtu=%d", msg.MTU)
	}
}

// TestTypeConfigAuthThenConfigRegression is the permanent CLIENT→AUTH→TypeConfig
// regression covering the production server push timing over real TLS.
func TestTypeConfigAuthThenConfigRegression(t *testing.T) {
	TestTypeConfigReceivedWhenCatcherArmedBeforeWaitEstablished(t)
}
