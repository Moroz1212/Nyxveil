package ticket_test

import (
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/nyxveil/nvp/auth/ticket"
)

func FuzzTicketVerify(f *testing.F) {
	pub, _, _ := ed25519.GenerateKey(nil)
	f.Add("test.token.string")
	f.Fuzz(func(t *testing.T, tokenStr string) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic: %v", r)
			}
		}()
		cfg := ticket.VerifierConfig{
			Issuer:     "https://control.nyxveil.test",
			Audience:   "nvp-node",
			PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
		}
		_, _ = ticket.Verify(cfg, tokenStr, "", "")
	})
}

func TestAlgNoneRejected(t *testing.T) {
	token := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
		Issuer:   "https://control.nyxveil.test",
		Audience: jwt.ClaimStrings{"nvp-node"},
	})
	signed, _ := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	pub, _, _ := ed25519.GenerateKey(nil)
	cfg := ticket.VerifierConfig{
		Issuer:     "https://control.nyxveil.test",
		Audience:   "nvp-node",
		PublicKeys: map[string]ed25519.PublicKey{"x": pub},
	}
	_, err := ticket.Verify(cfg, signed, "", "")
	if err == nil {
		t.Fatal("alg=none should be rejected")
	}
}

func TestTamperedTicketRejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	issuer := ticket.IssuerConfig{
		Issuer:     "https://control.nyxveil.test",
		Audience:   "nvp-node",
		KeyID:      "cp-key-1",
		PrivateKey: priv,
		TTL:        15 * time.Minute,
	}
	tok, err := ticket.Issue(issuer, "lic_1", "dev_1", "user", "basic", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	tampered := tok[:len(tok)-4] + "XXXX"
	cfg := ticket.VerifierConfig{
		Issuer:     issuer.Issuer,
		Audience:   issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
	}
	_, err = ticket.Verify(cfg, tampered, "dev_1", "")
	if err == nil {
		t.Fatal("tampered ticket should be rejected")
	}
}

func TestRevokedTicketRejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	issuer := ticket.IssuerConfig{
		Issuer:     "https://control.nyxveil.test",
		Audience:   "nvp-node",
		KeyID:      "cp-key-1",
		PrivateKey: priv,
		TTL:        15 * time.Minute,
	}
	tok, _ := ticket.Issue(issuer, "lic_1", "dev_1", "user", "basic", nil, nil)
	rev := ticket.NewMemoryRevocation()
	rev.RevokeLicense("lic_1")
	cfg := ticket.VerifierConfig{
		Issuer:     issuer.Issuer,
		Audience:   issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
		Revoked:    rev,
	}
	_, err := ticket.Verify(cfg, tok, "dev_1", "")
	if err != ticket.ErrRevoked {
		t.Fatalf("expected revoked, got %v", err)
	}
}

func TestWrongAudienceRejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	issuer := ticket.IssuerConfig{
		Issuer:     "https://control.nyxveil.test",
		Audience:   "wrong-audience",
		KeyID:      "cp-key-1",
		PrivateKey: priv,
		TTL:        15 * time.Minute,
	}
	tok, _ := ticket.Issue(issuer, "lic_1", "dev_1", "user", "basic", nil, nil)
	cfg := ticket.VerifierConfig{
		Issuer:     issuer.Issuer,
		Audience:   "nvp-node",
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
	}
	_, err := ticket.Verify(cfg, tok, "dev_1", "")
	if err == nil {
		t.Fatal("wrong audience should be rejected")
	}
}

func BenchmarkTicketVerify(b *testing.B) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	issuer := ticket.IssuerConfig{
		Issuer:     "https://control.nyxveil.test",
		Audience:   "nvp-node",
		KeyID:      "cp-key-1",
		PrivateKey: priv,
		TTL:        15 * time.Minute,
	}
	tok, _ := ticket.Issue(issuer, "lic_1", "dev_1", "user", "basic", nil, nil)
	cfg := ticket.VerifierConfig{
		Issuer:     issuer.Issuer,
		Audience:   issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ticket.Verify(cfg, tok, "dev_1", "")
	}
}
