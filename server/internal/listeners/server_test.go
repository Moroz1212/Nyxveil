package listeners

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/server/internal/datapath"
	"github.com/nyxveil/server/internal/netcfg"
	"github.com/nyxveil/server/internal/sessions"
)

func TestStartupBothTransportsDisabledOpensNone(t *testing.T) {
	mgr, err := sessions.New(10, "10.66.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	s := New(Config{
		EnableTLS:  false,
		EnableQUIC: false,
		TLSAddr:    "127.0.0.1:0",
		QUICAddr:   "127.0.0.1:0",
	}, nil, mgr, nil)
	if s.cfg.EnableTLS || s.cfg.EnableQUIC {
		t.Fatal("both-disabled must not be rewritten to both-enabled")
	}
	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	if s.TLSOK() || s.QUICOK() {
		t.Fatalf("expected zero listeners tls=%v quic=%v", s.TLSOK(), s.QUICOK())
	}
}

func TestEnableTLSAfterBothDisabled(t *testing.T) {
	mgr, err := sessions.New(10, "10.66.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	cert := mustSelfSignedCert(t)
	s := New(Config{
		EnableTLS:  false,
		EnableQUIC: false,
		TLSAddr:    "127.0.0.1:0",
		QUICAddr:   "127.0.0.1:0",
		Cert:       cert,
	}, nil, mgr, nil)
	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	if s.TLSOK() || s.QUICOK() {
		t.Fatal("expected none")
	}
	s.SetTransports(true, false)
	if err := s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if !s.TLSOK() || s.QUICOK() {
		t.Fatalf("tls=%v quic=%v", s.TLSOK(), s.QUICOK())
	}
}

func TestEnableQUICAfterBothDisabled(t *testing.T) {
	mgr, err := sessions.New(10, "10.66.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	cert := mustSelfSignedCert(t)
	s := New(Config{
		EnableTLS:  false,
		EnableQUIC: false,
		TLSAddr:    "127.0.0.1:0",
		QUICAddr:   "127.0.0.1:0",
		Cert:       cert,
	}, nil, mgr, nil)
	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	s.SetTransports(false, true)
	if err := s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if s.TLSOK() || !s.QUICOK() {
		t.Fatalf("tls=%v quic=%v", s.TLSOK(), s.QUICOK())
	}
}

func TestTypeConfigFailureDoesNotAttachSession(t *testing.T) {
	mgr, err := sessions.New(10, "10.66.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	bridge := datapath.New(mgr, nil, 8)
	s := New(Config{SubnetCIDR: "10.66.0.0/24", MTU: 1280}, nil, mgr, bridge)

	oldSend := sendConfigFn
	oldAttach := attachSessionFn
	defer func() {
		sendConfigFn = oldSend
		attachSessionFn = oldAttach
	}()
	sendConfigFn = func(ctx context.Context, sess *session.Session, m netcfg.Message) error {
		return errors.New("forced TypeConfig failure")
	}
	var attached int
	attachSessionFn = func(b *datapath.Bridge, sess *session.Session) {
		attached++
	}

	sess := session.New(session.DefaultConfig(false))
	rec, err := mgr.Allocate(sess)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.activateAllocatedSession(context.Background(), sess, rec); err == nil {
		t.Fatal("expected TypeConfig failure")
	}
	if attached != 0 {
		t.Fatalf("attached=%d", attached)
	}
}

func TestTypeConfigFailureReleasesVPNIP(t *testing.T) {
	mgr, err := sessions.New(10, "10.66.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	s := New(Config{SubnetCIDR: "10.66.0.0/24", MTU: 1280}, nil, mgr, nil)

	oldSend := sendConfigFn
	defer func() { sendConfigFn = oldSend }()
	sendConfigFn = func(ctx context.Context, sess *session.Session, m netcfg.Message) error {
		return errors.New("forced TypeConfig failure")
	}

	sess := session.New(session.DefaultConfig(false))
	rec, err := mgr.Allocate(sess)
	if err != nil {
		t.Fatal(err)
	}
	ip := rec.VPNIP
	if err := s.activateAllocatedSession(context.Background(), sess, rec); err == nil {
		t.Fatal("expected failure")
	}
	if mgr.Count() != 0 {
		t.Fatalf("count=%d after release", mgr.Count())
	}
	if _, ok := mgr.LookupByIP(ip); ok {
		t.Fatal("VPN IP still allocated")
	}
	// Pool must accept a new allocation after release.
	sess2 := session.New(session.DefaultConfig(false))
	if _, err := mgr.Allocate(sess2); err != nil {
		t.Fatal(err)
	}
	if mgr.Count() != 1 {
		t.Fatalf("count=%d", mgr.Count())
	}
}

func mustSelfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
