package ticket_test

import (
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/nyxveil/nvp/core/auth/ticket"
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

func TestVerifyRequiresDevicePub(t *testing.T) {
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
	cfg := ticket.VerifierConfig{
		Issuer:     issuer.Issuer,
		Audience:   issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
	}
	if _, err := ticket.Verify(cfg, tok, "dev_1", ""); err != ticket.ErrSessionBinding {
		t.Fatalf("expected session binding, got %v", err)
	}
}

func TestScopedTicketNodeAndLocation(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	issuer := ticket.IssuerConfig{
		Issuer:     "https://control.nyxveil.test",
		Audience:   "nvp-node",
		KeyID:      "cp-key-1",
		PrivateKey: priv,
		TTL:        15 * time.Minute,
	}
	devPub, _, _ := ed25519.GenerateKey(nil)
	tok, err := ticket.IssueScoped(issuer, "lic_1", "dev_1", "user", "basic",
		[]string{"connect"}, []string{"fi-hel"}, []string{"node-a"}, devPub)
	if err != nil {
		t.Fatal(err)
	}
	cfg := ticket.VerifierConfig{
		Issuer:     issuer.Issuer,
		Audience:   issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
	}

	if _, err := ticket.VerifyAt(cfg, tok, "dev_1", "node-a", "fi-hel"); err != nil {
		t.Fatalf("expected valid scoped ticket, got %v", err)
	}
	if _, err := ticket.VerifyAt(cfg, tok, "dev_1", "node-b", "fi-hel"); err != ticket.ErrWrongScope {
		t.Fatalf("expected wrong scope, got %v", err)
	}
	if _, err := ticket.VerifyAt(cfg, tok, "dev_1", "", "fi-hel"); err != ticket.ErrWrongScope {
		t.Fatalf("expected wrong scope for empty node, got %v", err)
	}
	if _, err := ticket.VerifyAt(cfg, tok, "dev_1", "node-a", "fi-tmp"); err != ticket.ErrWrongLocation {
		t.Fatalf("expected wrong location, got %v", err)
	}
	if _, err := ticket.VerifyAt(cfg, tok, "dev_1", "node-a", ""); err != ticket.ErrWrongLocation {
		t.Fatalf("expected wrong location for empty location, got %v", err)
	}
}

func TestWrongIssuerRejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	devPub, _, _ := ed25519.GenerateKey(nil)
	issuer := ticket.IssuerConfig{
		Issuer: "https://evil.example", Audience: "nvp-node",
		KeyID: "cp-key-1", PrivateKey: priv, TTL: 15 * time.Minute,
	}
	tok, err := ticket.IssueWithDevice(issuer, "lic_1", "dev_1", "user", "basic", nil, nil, devPub)
	if err != nil {
		t.Fatal(err)
	}
	cfg := ticket.VerifierConfig{
		Issuer:     "https://control.nyxveil.test",
		Audience:   "nvp-node",
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
	}
	if _, err := ticket.Verify(cfg, tok, "dev_1", ""); err == nil {
		t.Fatal("wrong issuer should be rejected")
	}
}

func TestExpiredTicketRejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	devPub, _, _ := ed25519.GenerateKey(nil)
	issuer := ticket.IssuerConfig{
		Issuer: "https://control.nyxveil.test", Audience: "nvp-node",
		KeyID: "cp-key-1", PrivateKey: priv, TTL: -time.Hour,
	}
	tok, err := ticket.IssueWithDevice(issuer, "lic_1", "dev_1", "user", "basic", nil, nil, devPub)
	if err != nil {
		t.Fatal(err)
	}
	cfg := ticket.VerifierConfig{
		Issuer:     issuer.Issuer,
		Audience:   issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
	}
	if _, err := ticket.Verify(cfg, tok, "dev_1", ""); err != ticket.ErrExpired {
		t.Fatalf("expected expired, got %v", err)
	}
}

func TestNBFRejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	devPub, _, _ := ed25519.GenerateKey(nil)
	now := time.Now()
	claims := ticket.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        "tkt_nbf",
			Issuer:    "https://control.nyxveil.test",
			Audience:  jwt.ClaimStrings{"nvp-node"},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(time.Hour)),
			ExpiresAt: jwt.NewNumericDate(now.Add(2 * time.Hour)),
		},
		LicenseID:   "lic_1",
		DeviceID:    "dev_1",
		Role:        "user",
		Plan:        "basic",
		ProtocolVer: "NVP/1",
		DevicePub:   devPub,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = "cp-key-1"
	tok, err := token.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := ticket.VerifierConfig{
		Issuer:     "https://control.nyxveil.test",
		Audience:   "nvp-node",
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
	}
	if _, err := ticket.Verify(cfg, tok, "dev_1", ""); err == nil {
		t.Fatal("nbf in the future should be rejected")
	}
}

func TestWrongProtocolRejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	devPub, _, _ := ed25519.GenerateKey(nil)
	now := time.Now()
	claims := ticket.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        "tkt_proto",
			Issuer:    "https://control.nyxveil.test",
			Audience:  jwt.ClaimStrings{"nvp-node"},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		},
		LicenseID:   "lic_1",
		DeviceID:    "dev_1",
		Role:        "user",
		Plan:        "basic",
		ProtocolVer: "NVP/999",
		DevicePub:   devPub,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = "cp-key-1"
	tok, err := token.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := ticket.VerifierConfig{
		Issuer:     "https://control.nyxveil.test",
		Audience:   "nvp-node",
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
	}
	if _, err := ticket.Verify(cfg, tok, "dev_1", ""); err == nil {
		t.Fatal("wrong protocol_version should be rejected")
	}
}

func TestWrongDeviceIDRejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	devPub, _, _ := ed25519.GenerateKey(nil)
	issuer := ticket.IssuerConfig{
		Issuer: "https://control.nyxveil.test", Audience: "nvp-node",
		KeyID: "cp-key-1", PrivateKey: priv, TTL: 15 * time.Minute,
	}
	tok, err := ticket.IssueWithDevice(issuer, "lic_1", "dev_1", "user", "basic", nil, nil, devPub)
	if err != nil {
		t.Fatal(err)
	}
	cfg := ticket.VerifierConfig{
		Issuer:     issuer.Issuer,
		Audience:   issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
	}
	if _, err := ticket.Verify(cfg, tok, "other_device", ""); err != ticket.ErrWrongDevice {
		t.Fatalf("expected wrong device, got %v", err)
	}
}

func TestInvalidSignatureRejected(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	wrongPub, _, _ := ed25519.GenerateKey(nil)
	devPub, _, _ := ed25519.GenerateKey(nil)
	issuer := ticket.IssuerConfig{
		Issuer: "https://control.nyxveil.test", Audience: "nvp-node",
		KeyID: "cp-key-1", PrivateKey: priv, TTL: 15 * time.Minute,
	}
	tok, err := ticket.IssueWithDevice(issuer, "lic_1", "dev_1", "user", "basic", nil, nil, devPub)
	if err != nil {
		t.Fatal(err)
	}
	cfg := ticket.VerifierConfig{
		Issuer:     issuer.Issuer,
		Audience:   issuer.Audience,
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": wrongPub},
	}
	if _, err := ticket.Verify(cfg, tok, "dev_1", ""); err == nil {
		t.Fatal("invalid signature (wrong key) should be rejected")
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
	devPub, _, _ := ed25519.GenerateKey(nil)
	tok, _ := ticket.IssueWithDevice(issuer, "lic_1", "dev_1", "user", "basic", nil, nil, devPub)
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
