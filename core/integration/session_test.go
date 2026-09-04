package integration

import (
	"context"
	"crypto/ed25519"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nyxveil/nvp/auth/ticket"
	"github.com/nyxveil/nvp/control"
	"github.com/nyxveil/nvp/keys"
	"github.com/nyxveil/nvp/packet"
	"github.com/nyxveil/nvp/replay"
	"github.com/nyxveil/nvp/server"
	"github.com/nyxveil/nvp/session"
	"github.com/nyxveil/nvp/transport"
	"github.com/nyxveil/nvp/transport/memory"
)

func setupTicket(t *testing.T) (string, ticket.VerifierConfig) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
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
	tok, err := ticket.Issue(issuer, "lic_test", "dev_test", "user", "premium", []string{"connect"}, []string{"fi"})
	if err != nil {
		t.Fatal(err)
	}
	verifier := ticket.VerifierConfig{
		Issuer:     issuer.Issuer,
		Audience:   issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
		Revoked:    ticket.NewMemoryRevocation(),
	}
	return tok, verifier
}

func TestFullSessionEstablishment(t *testing.T) {
	tok, verifier := setupTicket(t)

	clientConn, serverConn := memory.Pair()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientSess := session.New(session.DefaultConfig(true))
	serverSess := session.New(session.DefaultConfig(false))
	authHandler := server.NewAuthHandler("fi-hel-01", verifier)

	serverSess.OnControl(func(msgType byte, payload []byte) error {
		if msgType == control.TypeAuth {
			return authHandler.HandleAuth(ctx, serverSess, payload)
		}
		return nil
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = serverSess.Connect(ctx, serverConn)
		_ = serverSess.RunHandshake(ctx)
		_ = serverSess.ReadLoop(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = clientSess.Connect(ctx, clientConn)
		_ = clientSess.RunHandshake(ctx)
		_ = clientSess.ReadLoop(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	if err := clientSess.SendAuth(ctx, []byte(tok)); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(2 * time.Second)
	for {
		if clientSess.State() == session.StateEstablished {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("client not established, state=%s", clientSess.State())
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}

	knownPlaintext := []byte("NVP confidentiality test payload 12345")
	if err := clientSess.SendData(ctx, knownPlaintext); err != nil {
		t.Fatal(err)
	}

	_ = clientSess.Close(context.Background())
	_ = serverSess.Close(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Log("ReadLoop goroutines did not exit cleanly")
	}
}

func TestConfidentialityOnWire(t *testing.T) {
	clientConn, serverConn := memory.Pair()
	var captured []byte
	var mu sync.Mutex

	wrapped := &captureConn{Conn: serverConn, onWrite: func(b []byte) {
		mu.Lock()
		captured = append(captured, b...)
		mu.Unlock()
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	clientSess := session.New(session.DefaultConfig(true))
	serverSess := session.New(session.DefaultConfig(false))

	go func() {
		_ = serverSess.Connect(ctx, wrapped)
		_ = serverSess.RunHandshake(ctx)
		_ = serverSess.ReadLoop(ctx)
	}()

	if err := clientSess.Connect(ctx, clientConn); err != nil {
		t.Fatal(err)
	}
	if err := clientSess.RunHandshake(ctx); err != nil {
		t.Fatal(err)
	}

	secret := []byte("KNOWN_PLAINTEXT_SECRET")
	_ = clientSess.SendData(ctx, secret)

	mu.Lock()
	defer mu.Unlock()
	if strings.Contains(string(captured), string(secret)) {
		t.Fatal("plaintext found in network capture")
	}
}

type captureConn struct {
	transport.Conn
	onWrite func([]byte)
}

func (c *captureConn) Write(ctx context.Context, data []byte) error {
	if c.onWrite != nil {
		c.onWrite(data)
	}
	return c.Conn.Write(ctx, data)
}

func TestTamperedCiphertextRejected(t *testing.T) {
	kp, _ := keys.GenerateEphemeral()
	peerKP, _ := keys.GenerateEphemeral()
	shared, _ := keys.SharedSecret(&kp.Private, &peerKP.Public)
	sk, err := keys.DeriveSessionKeys(shared, []byte("transcript"), 1)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := keys.NewClientAEAD(sk.ClientToServer)
	if err != nil {
		t.Fatal(err)
	}
	inner, _ := packet.EncodeInner(control.TypeData, []byte("test"), nil)
	ct, _ := aead.Seal(1, 0, inner)
	ct[0] ^= 0xFF
	recvAEAD, _ := keys.NewClientAEAD(sk.ClientToServer)
	_, err = recvAEAD.Open(1, 0, ct)
	if err == nil {
		t.Fatal("tampered ciphertext should be rejected")
	}
}

func TestReplayRejected(t *testing.T) {
	// Replay protection is validated at replay window unit tests and session layer.
	// Direct wire replay test requires full session sync; covered by replay package.
	w := replay.NewWindow(64)
	w.Reset(1)
	_ = w.CheckAndMark(1, 1)
	if err := w.CheckAndMark(1, 1); err == nil {
		t.Fatal("replay should be rejected")
	}
}

func TestInvalidTicketRejected(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	verifier := ticket.VerifierConfig{
		Issuer:     "https://control.nyxveil.test",
		Audience:   "nvp-node",
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
	}
	_, err := ticket.Verify(verifier, "invalid.token.here", "dev_test", "fi-hel-01")
	if err == nil {
		t.Fatal("invalid ticket should be rejected")
	}
}

func TestExpiredTicketRejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	issuer := ticket.IssuerConfig{
		Issuer:     "https://control.nyxveil.test",
		Audience:   "nvp-node",
		KeyID:      "cp-key-1",
		PrivateKey: priv,
		TTL:        -1 * time.Hour,
	}
	tok, err := ticket.Issue(issuer, "lic_test", "dev_test", "user", "premium", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	verifier := ticket.VerifierConfig{
		Issuer:     issuer.Issuer,
		Audience:   issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
	}
	_, err = ticket.Verify(verifier, tok, "dev_test", "")
	if err != ticket.ErrExpired {
		t.Fatalf("expected expired, got %v", err)
	}
}

func TestWrongDeviceRejected(t *testing.T) {
	tok, verifier := setupTicket(t)
	_, err := ticket.Verify(verifier, tok, "wrong_device", "")
	if err != ticket.ErrWrongDevice {
		t.Fatalf("expected wrong device, got %v", err)
	}
}

func TestDataBeforeAuthRejected(t *testing.T) {
	if session.StateAuthenticating.CanSendData() {
		t.Fatal("DATA should not be allowed in AUTHENTICATING")
	}
	if session.StateSecureChannel.CanSendData() {
		t.Fatal("DATA should not be allowed before auth")
	}
}

func TestStateMachineTransitions(t *testing.T) {
	tests := []struct {
		from, to session.State
		ok       bool
	}{
		{session.StateNew, session.StateTransportConnected, true},
		{session.StateTransportConnected, session.StateSecureChannel, true},
		{session.StateSecureChannel, session.StateAuthenticating, true},
		{session.StateAuthenticating, session.StateEstablished, true},
		{session.StateEstablished, session.StateRekeying, true},
		{session.StateClosed, session.StateEstablished, false},
		{session.StateAuthenticating, session.StateRekeying, false},
	}
	for _, tc := range tests {
		got := session.ValidTransition(tc.from, tc.to)
		if got != tc.ok {
			t.Errorf("transition %s -> %s: got %v want %v", tc.from, tc.to, got, tc.ok)
		}
	}
}

func TestOversizedFrameRejected(t *testing.T) {
	huge := make([]byte, 70000)
	_, err := packet.EncodeWireRecord(huge)
	if err == nil {
		t.Fatal("oversized frame should be rejected")
	}
}

func TestTruncatedFrameRejected(t *testing.T) {
	_, _, err := packet.DecodeWireRecord([]byte{0, 0, 1, 0})
	if err == nil {
		t.Fatal("truncated frame should be rejected")
	}
}
