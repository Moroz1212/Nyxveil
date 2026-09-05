package authhandler

import (
	"context"
	"crypto/ed25519"
	"sync"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/control"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport/memory"
)

func TestAuthHandlerRateLimit(t *testing.T) {
	h := NewAuthHandler("node-1", "", ticketVerifierStub())
	h.MaxPending = 1

	s1 := session.New(session.DefaultConfig(true))
	s2 := session.New(session.DefaultConfig(true))

	done := make(chan struct{})
	go func() {
		_ = h.HandleAuth(context.Background(), s1, []byte("bad"))
		close(done)
	}()

	err := h.HandleAuth(context.Background(), s2, []byte("bad"))
	if err == nil {
		t.Fatal("expected rate limit")
	}
	<-done
}

func TestAuthHandlerInvalidTicket(t *testing.T) {
	h := NewAuthHandler("node-1", "", ticketVerifierStub())
	s := session.New(session.DefaultConfig(true))
	err := h.HandleAuth(context.Background(), s, []byte("not-a-jwt"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestTicketWithoutConnectPermissionRejected(t *testing.T) {
	tok, devPriv, verifier := issueTestTicket(t, nil) // no permissions
	err := runAuth(t, tok, devPriv, verifier)
	if err == nil {
		t.Fatal("expected AUTH_FAIL without connect permission")
	}
	if err != ErrMissingConnectPermission {
		t.Fatalf("want ErrMissingConnectPermission, got %v", err)
	}
}

func TestConnectPermissionAccepted(t *testing.T) {
	tok, devPriv, verifier := issueTestTicket(t, []string{ticket.PermissionConnect})
	if err := runAuth(t, tok, devPriv, verifier); err != nil {
		t.Fatalf("connect permission should authenticate: %v", err)
	}
}

func ticketVerifierStub() ticket.VerifierConfig {
	return ticket.VerifierConfig{
		Issuer:     "https://x",
		Audience:   "nvp-node",
		PublicKeys: map[string]ed25519.PublicKey{},
		Revoked:    ticket.NopRevocation{},
	}
}

func issueTestTicket(t *testing.T, perms []string) (string, ed25519.PrivateKey, ticket.VerifierConfig) {
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
	tok, err := ticket.IssueWithDevice(issuer, "lic_test", "dev_test", "user", "premium", perms, []string{"fi"}, devPub)
	if err != nil {
		t.Fatal(err)
	}
	verifier := ticket.VerifierConfig{
		Issuer:     issuer.Issuer,
		Audience:   issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
		Revoked:    ticket.NewMemoryRevocation(),
	}
	return tok, devPriv, verifier
}

func runAuth(t *testing.T, tok string, devPriv ed25519.PrivateKey, verifier ticket.VerifierConfig) error {
	t.Helper()
	clientConn, serverConn := memory.Pair()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientSess := session.New(session.DefaultConfig(true))
	serverSess := session.New(session.DefaultConfig(false))
	authHandler := NewAuthHandler("fi-hel-01", "fi", verifier)

	var authErr error
	var mu sync.Mutex
	serverSess.OnControl(func(msgType byte, payload []byte) error {
		if msgType == control.TypeAuth {
			err := authHandler.HandleAuth(ctx, serverSess, payload)
			mu.Lock()
			authErr = err
			mu.Unlock()
			return err
		}
		return nil
	})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = serverSess.Connect(ctx, serverConn)
		_ = serverSess.RunHandshake(ctx)
		_ = serverSess.ReadLoop(ctx)
	}()
	go func() {
		defer wg.Done()
		_ = clientSess.Connect(ctx, clientConn)
		_ = clientSess.RunHandshake(ctx)
		_ = clientSess.ReadLoop(ctx)
	}()

	deadlineHS := time.After(5 * time.Second)
	for clientSess.State() != session.StateAuthenticating {
		select {
		case <-deadlineHS:
			cancel()
			wg.Wait()
			t.Fatalf("handshake did not reach AUTHENTICATING (state=%s)", clientSess.State())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	authBody, err := ticket.EncodeAuthPayload(tok, clientSess.Transcript(), devPriv)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientSess.SendAuth(ctx, authBody); err != nil {
		cancel()
		wg.Wait()
		t.Fatalf("SendAuth: %v", err)
	}

	deadline := time.After(10 * time.Second)
	for {
		mu.Lock()
		err := authErr
		mu.Unlock()
		if err != nil {
			cancel()
			wg.Wait()
			return err
		}
		if clientSess.State() == session.StateEstablished {
			cancel()
			wg.Wait()
			return nil
		}
		select {
		case <-deadline:
			cancel()
			wg.Wait()
			mu.Lock()
			err := authErr
			mu.Unlock()
			if err != nil {
				return err
			}
			t.Fatalf("timeout waiting for auth result (client state=%s)", clientSess.State())
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// Ensure auth fail reasons compile.
var _ = control.AuthFailInvalidTicket
