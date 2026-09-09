package nvp

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net"
	"strconv"
	"testing"
	"time"
	_ "unsafe"

	qt "github.com/nyxveil/client-ios/bridge/quictransport"
	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/authhandler"
	"github.com/nyxveil/nvp/core/control"
	"github.com/nyxveil/nvp/core/controlplane/catalog"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
)

// Test-only peer uses the same TypeConfig shim as server/internal/netcfg/send.go.
//
//go:linkname sendControl github.com/nyxveil/nvp/core/session.(*Session).sendControl
func sendControl(*session.Session, context.Context, byte, []byte) error

type testSink struct {
	packets chan []byte
	failed  chan string
}

func (s *testSink) Receive(p []byte) {
	select {
	case s.packets <- p:
	default:
	}
}
func (s *testSink) Failed(code string) {
	select {
	case s.failed <- code:
	default:
	}
}

func TestBridgeQUICFrozenCoreInterop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	ln, err := qt.NewTransport().Listen(ctx, "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: priv}}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	devicePub, devicePriv, _ := ed25519.GenerateKey(rand.Reader)
	issuer := ticket.IssuerConfig{Issuer: "test", Audience: "nvp-node", KeyID: "test", PrivateKey: priv, TTL: time.Minute}
	tok, err := ticket.IssueWithDevice(issuer, "license", "device", "user", "test", []string{"connect"}, []string{"fi"}, devicePub)
	if err != nil {
		t.Fatal(err)
	}
	serverErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept(ctx)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		s := session.New(session.DefaultConfig(false))
		defer s.Close(context.Background())
		handler := authhandler.NewAuthHandler("fi-02", "fi", ticket.VerifierConfig{Issuer: "test", Audience: "nvp-node", PublicKeys: map[string]ed25519.PublicKey{"test": pub}, Revoked: ticket.NopRevocation{}})
		s.OnControl(func(kind byte, data []byte) error {
			if kind != control.TypeAuth {
				return nil
			}
			if err := handler.HandleAuth(ctx, s, data); err != nil {
				return err
			}
			return sendControl(s, ctx, control.TypeConfig, []byte(`{"vpn_ip":"10.8.0.2","vpn_prefix":24,"mtu":1400,"gateway":"10.8.0.1","dns_servers":["10.8.0.1"]}`))
		})
		s.OnData(func(p []byte) error { return s.WritePacket(ctx, p) })
		if err := s.Connect(ctx, conn); err != nil {
			serverErr <- err
			return
		}
		if err := s.RunHandshake(ctx); err != nil {
			serverErr <- err
			return
		}
		serverErr <- s.ReadLoop(ctx)
	}()
	host, ps, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(ps)
	pin := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	cat := model.Catalog{Version: "1", ExpiresAt: time.Now().Add(time.Minute), Locations: []model.Location{{LocationID: "fi", Enabled: true}},
		Nodes: []model.NodeRegistryEntry{{NodeID: "fi-02", LocationID: "fi", Enabled: true, Capacity: 10, ServerName: "localhost", SPKIPin: pin[:],
			Endpoints: []transport.Endpoint{{Host: host, Port: port, Profiles: []transport.Profile{transport.ProfileQUICUDP}}}}}}
	signed, _ := (&catalog.Signer{KeyID: "test", PrivateKey: priv}).Sign(cat)
	raw, _ := json.Marshal(signed)
	keys := map[string]string{"test": base64.StdEncoding.EncodeToString(pub)}
	keyJSON, _ := json.Marshal(keys)
	if err := VerifyCatalog(raw, keyJSON); err != nil {
		t.Fatal(err)
	}
	signed.Catalog.Nodes[0].SPKIPin[0] ^= 1
	tampered, _ := json.Marshal(signed)
	if VerifyCatalog(tampered, keyJSON) == nil {
		t.Fatal("accepted catalog tampering")
	}
	in, _ := json.Marshal(input{LocationID: "fi", Ticket: tok, Seed: devicePriv.Seed(), Catalog: raw, Keys: keys})
	sink := &testSink{packets: make(chan []byte, 8), failed: make(chan string, 1)}
	e := NewEngine(sink)
	e.roots = pool
	defer e.Close()
	config, err := e.Begin(string(in))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MTU int    `json:"mtu"`
		VPN string `json:"vpn_ip"`
	}
	if json.Unmarshal([]byte(config), &cfg) != nil || cfg.MTU < 576 || cfg.MTU >= 1400 || cfg.VPN != "10.8.0.2" {
		t.Fatalf("config: %s", config)
	}
	p := make([]byte, 28)
	p[0] = 0x45
	p[3] = 28
	copy(p[12:16], []byte{10, 8, 0, 2})
	copy(p[16:20], []byte{10, 8, 0, 1})
	if err := e.Send(p); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-sink.packets:
		if !bytes.Equal(p, got) {
			t.Fatal("packet changed")
		}
	case err := <-serverErr:
		t.Fatalf("peer: %v", err)
	case failure := <-sink.failed:
		t.Fatal(failure)
	case <-ctx.Done():
		t.Fatal("packet timeout")
	}
	e.Close()
	e.Close()
	if e.Send(p) == nil {
		t.Fatal("send after close succeeded")
	}
	if _, err := e.Begin(string(in)); err == nil {
		t.Fatal("reused single-use engine")
	}
	// A second HTTP/3 CONNECT is accepted, but no peer runs its NVP handshake.
	// Manual disconnect must interrupt Begin without waiting for the 45s budget.
	e2 := NewEngine(sink)
	e2.roots = pool
	defer e2.Close()
	cancelled := make(chan error, 1)
	go func() { _, err := e2.Begin(string(in)); cancelled <- err }()
	time.Sleep(100 * time.Millisecond)
	e2.Close()
	select {
	case err := <-cancelled:
		if err == nil {
			t.Fatal("cancelled handshake succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("manual disconnect didn't interrupt handshake")
	}
}

func TestPacketValidation(t *testing.T) {
	for _, p := range [][]byte{nil, make([]byte, 20), {0x60, 0, 0, 0}, {0x45, 0, 0, 20}} {
		if validIPv4(p) {
			t.Fatal("accepted malformed IP packet")
		}
	}
}
