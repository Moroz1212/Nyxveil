package datapath

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/authhandler"
	"github.com/nyxveil/nvp/core/control"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport/memory"
	"github.com/nyxveil/server/internal/sessions"
)

// Minimal IPv6 packet (version nibble 6). Windows Wintun commonly emits IPv6 ND
// immediately after full-tunnel routes come up — the live 1.0.5 EOF trigger.
var ipv6Packet = []byte{
	0x60, 0x00, 0x00, 0x00, 0x00, 0x10, 0x3a, 0xff,
	0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1,
	0xff, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1,
	0x87, 0x00, 0x00, 0x00,
}

func ipv4Pkt(src, dst netip.Addr) []byte {
	s, d := src.As4(), dst.As4()
	pkt := make([]byte, 20)
	pkt[0] = 0x45
	pkt[8] = 64
	pkt[9] = 1
	copy(pkt[12:16], s[:])
	copy(pkt[16:20], d[:])
	return pkt
}

type closePair struct {
	client        *session.Session
	server        *session.Session
	mgr           *sessions.Manager
	rec           *sessions.Record
	serverReadErr <-chan error
	clientReadErr <-chan error
	cancel        context.CancelFunc
}

func setupAuthTicket(t *testing.T) (tok string, devPriv ed25519.PrivateKey, verifier ticket.VerifierConfig) {
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
		Issuer: "https://control.nyxveil.test", Audience: "nvp-node",
		KeyID: "cp-key-1", PrivateKey: priv, TTL: 15 * time.Minute,
	}
	tok, err = ticket.IssueWithDevice(issuer, "lic_test", "dev_test", "user", "premium",
		[]string{"connect"}, []string{"fi"}, devPub)
	if err != nil {
		t.Fatal(err)
	}
	verifier = ticket.VerifierConfig{
		Issuer: issuer.Issuer, Audience: issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
		Revoked:    ticket.NewMemoryRevocation(),
	}
	return tok, devPriv, verifier
}

// establishPair brings up AUTH_OK sessions and installs server OnData after Allocate,
// mirroring production listeners → AttachSession order.
func establishPair(t *testing.T, onData func([]byte) error) *closePair {
	t.Helper()
	tok, devPriv, verifier := setupAuthTicket(t)
	clientConn, serverConn := memory.Pair()
	ctx, cancel := context.WithCancel(context.Background())

	clientSess := session.New(session.DefaultConfig(true))
	serverSess := session.New(session.DefaultConfig(false))
	authHandler := authhandler.NewAuthHandler("fi-hel-01", "fi", verifier)
	mgr, err := sessions.New(10, "10.66.0.0/24")
	if err != nil {
		t.Fatal(err)
	}

	serverErrCh := make(chan error, 1)
	clientErrCh := make(chan error, 1)
	var allocOnce sync.Once
	var rec *sessions.Record

	serverSess.OnControl(func(msgType byte, payload []byte) error {
		if msgType != control.TypeAuth {
			return nil
		}
		if err := authHandler.HandleAuth(ctx, serverSess, payload); err != nil {
			return err
		}
		var aerr error
		allocOnce.Do(func() {
			rec, aerr = mgr.Allocate(serverSess)
			if aerr == nil && onData != nil {
				serverSess.OnData(onData)
			}
		})
		return aerr
	})

	go func() {
		defer serverConn.Close() // mirrors listeners.handleConn defer conn.Close()
		_ = serverSess.Connect(ctx, serverConn)
		_ = serverSess.RunHandshake(ctx)
		serverErrCh <- serverSess.ReadLoop(ctx)
	}()
	go func() {
		_ = clientSess.Connect(ctx, clientConn)
		_ = clientSess.RunHandshake(ctx)
		clientErrCh <- clientSess.ReadLoop(ctx)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for clientSess.State() != session.StateAuthenticating && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	authBody, err := ticket.EncodeAuthPayload(tok, clientSess.Transcript(), devPriv)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := clientSess.SendAuth(ctx, authBody); err != nil {
		cancel()
		t.Fatal(err)
	}
	for clientSess.State() != session.StateEstablished && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if clientSess.State() != session.StateEstablished {
		cancel()
		t.Fatal("client not established")
	}
	for rec == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if rec == nil {
		cancel()
		t.Fatal("allocate missing")
	}
	return &closePair{
		client: clientSess, server: serverSess, mgr: mgr, rec: rec,
		serverReadErr: serverErrCh, clientReadErr: clientErrCh, cancel: cancel,
	}
}

// TestV116ValidateSourceErrorKillsSession proves the production close initiator:
// server-v1.1.6 AttachSession returned ValidateSource errors into Session.OnData,
// which fails ReadLoop → handleConn defer conn.Close() → client ReadLoop EOF.
func TestV116ValidateSourceErrorKillsSession(t *testing.T) {
	var p *closePair
	p = establishPair(t, func(pkt []byte) error {
		return p.mgr.ValidateSource(p.server, pkt)
	})
	defer p.cancel()

	if err := p.client.SendData(context.Background(), ipv6Packet); err != nil {
		t.Fatalf("send ipv6: %v", err)
	}

	select {
	case err := <-p.serverReadErr:
		if !errors.Is(err, sessions.ErrNotIPv4) {
			t.Fatalf("server close want ErrNotIPv4, got %v", err)
		}
		t.Logf("PROVEN close initiator=SERVER reason=%v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("expected server ReadLoop to exit after IPv6 ValidateSource error")
	}

	select {
	case err := <-p.clientReadErr:
		t.Logf("client ReadLoop exit after server close: %v (EOF expected)", err)
		if err == nil {
			t.Fatal("expected client read error after peer close")
		}
		_ = io.EOF
	case <-time.After(2 * time.Second):
		t.Fatal("client ReadLoop should observe peer close")
	}
}

// TestNoDataSurvives20s: established session without data lives past 15s
// (rules out unswept auth deadline as the live close cause).
func TestNoDataSurvives20s(t *testing.T) {
	if testing.Short() {
		t.Skip("20s soak")
	}
	var pair *closePair
	pair = establishPair(t, func(pkt []byte) error {
		_ = pair.mgr.ValidateSource(pair.server, pkt)
		return nil // drop invalid — never fail session
	})
	defer pair.cancel()

	select {
	case err := <-pair.serverReadErr:
		t.Fatalf("server died without data: %v", err)
	case err := <-pair.clientReadErr:
		t.Fatalf("client died without data: %v", err)
	case <-time.After(20 * time.Second):
		t.Log("NO-DATA 20s PASS — no ~15s auth deadline close")
	}
}

// TestEarlyIPv4DataSurvives: first valid data packet must not close the session.
func TestEarlyIPv4DataSurvives(t *testing.T) {
	var pair *closePair
	pair = establishPair(t, nil)
	defer pair.cancel()
	dev := &nullTUN{}
	bridge := New(pair.mgr, dev, 8)
	if err := bridge.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer bridge.Stop()
	bridge.AttachSession(pair.server)

	good := ipv4Pkt(pair.rec.VPNIP, netip.MustParseAddr("1.2.3.4"))
	if err := pair.client.SendData(context.Background(), good); err != nil {
		t.Fatalf("send ipv4: %v", err)
	}
	select {
	case err := <-pair.serverReadErr:
		t.Fatalf("early ipv4 killed server: %v", err)
	case err := <-pair.clientReadErr:
		t.Fatalf("early ipv4 killed client: %v", err)
	case <-time.After(500 * time.Millisecond):
		t.Log("EARLY-DATA IPv4 PASS")
	}
}

// TestAttachSessionDropsIPv6KeepsSession is the 1.1.7 fix gate.
func TestAttachSessionDropsIPv6KeepsSession(t *testing.T) {
	var pair *closePair
	pair = establishPair(t, nil)
	defer pair.cancel()
	dev := &nullTUN{}
	bridge := New(pair.mgr, dev, 8)
	if err := bridge.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer bridge.Stop()
	bridge.AttachSession(pair.server)

	if err := pair.client.SendData(context.Background(), ipv6Packet); err != nil {
		t.Fatalf("send ipv6: %v", err)
	}
	good := ipv4Pkt(pair.rec.VPNIP, netip.MustParseAddr("1.2.3.4"))
	if err := pair.client.SendData(context.Background(), good); err != nil {
		t.Fatalf("send ipv4: %v", err)
	}

	select {
	case err := <-pair.serverReadErr:
		t.Fatalf("server must survive IPv6 drop + good IPv4, got %v", err)
	case err := <-pair.clientReadErr:
		t.Fatalf("client must survive, got %v", err)
	case <-time.After(500 * time.Millisecond):
		t.Log("IPv6 dropped, session alive PASS")
	}
}

// TestSessionTransportSoak30s: established + bidirectional data for 30s.
func TestSessionTransportSoak30s(t *testing.T) {
	if testing.Short() {
		t.Skip("30s soak")
	}
	gotClient := make(chan []byte, 8)
	var pair *closePair
	pair = establishPair(t, nil)
	defer pair.cancel()
	dev := &nullTUN{}
	bridge := New(pair.mgr, dev, 64)
	if err := bridge.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer bridge.Stop()
	bridge.AttachSession(pair.server)
	pair.client.OnData(func(pkt []byte) error {
		cp := append([]byte(nil), pkt...)
		select {
		case gotClient <- cp:
		default:
		}
		return nil
	})

	c2s := ipv4Pkt(pair.rec.VPNIP, netip.MustParseAddr("8.8.8.8"))
	if err := pair.client.SendData(context.Background(), c2s); err != nil {
		t.Fatal(err)
	}
	s2c := ipv4Pkt(netip.MustParseAddr("8.8.8.8"), pair.rec.VPNIP)
	if err := pair.server.SendData(context.Background(), s2c); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gotClient:
	case <-time.After(2 * time.Second):
		t.Fatal("missing server→client data")
	}

	deadline := time.After(30 * time.Second)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-pair.serverReadErr:
			t.Fatalf("server died during soak: %v", err)
		case err := <-pair.clientReadErr:
			t.Fatalf("client died during soak: %v", err)
		case <-ticker.C:
			_ = pair.client.SendPing(context.Background())
			_ = pair.client.SendData(context.Background(), c2s)
		case <-deadline:
			t.Log("SESSION TRANSPORT SOAK 30s PASS")
			return
		}
	}
}

type nullTUN struct{}

func (nullTUN) Read([]byte) (int, error) {
	time.Sleep(50 * time.Millisecond)
	return 0, nil
}
func (nullTUN) Write(p []byte) (int, error) { return len(p), nil }
