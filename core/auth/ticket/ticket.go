package ticket

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidToken   = errors.New("invalid access ticket")
	ErrExpired        = errors.New("ticket expired")
	ErrWrongAudience  = errors.New("wrong audience")
	ErrWrongDevice    = errors.New("wrong device")
	ErrWrongAlgorithm = errors.New("unexpected signing algorithm")
	ErrRevoked        = errors.New("ticket revoked")
	ErrWrongScope     = errors.New("wrong node scope")
)

// Allowed algorithms - strict allowlist.
var allowedAlgorithms = map[string]bool{
	jwt.SigningMethodEdDSA.Alg(): true,
}

// Claims defines NVP access ticket JWT claims.
type Claims struct {
	jwt.RegisteredClaims
	LicenseID   string   `json:"license_id"`
	DeviceID    string   `json:"device_id"`
	Role        string   `json:"role"`
	Plan        string   `json:"plan"`
	Permissions []string `json:"permissions"`
	Locations   []string `json:"locations,omitempty"`
	NodeScope   []string `json:"node_scope,omitempty"`
	ProtocolVer string   `json:"protocol_version"`
}

// IssuerConfig holds Control Plane signing configuration.
type IssuerConfig struct {
	Issuer     string
	Audience   string
	KeyID      string
	PrivateKey ed25519.PrivateKey
	TTL        time.Duration
}

// VerifierConfig holds VPN node verification configuration.
type VerifierConfig struct {
	Issuer     string
	Audience   string
	PublicKeys map[string]ed25519.PublicKey
	Revoked    RevocationCache
}

// RevocationCache checks ticket/license/device revocation.
type RevocationCache interface {
	IsRevoked(jti, licenseID, deviceID string) bool
}

// NopRevocation is a no-op revocation cache.
type NopRevocation struct{}

func (NopRevocation) IsRevoked(_, _, _ string) bool { return false }

// MemoryRevocation is an in-memory revocation list for tests.
type MemoryRevocation struct {
	jtis     map[string]bool
	licenses map[string]bool
	devices  map[string]bool
}

func NewMemoryRevocation() *MemoryRevocation {
	return &MemoryRevocation{
		jtis:     make(map[string]bool),
		licenses: make(map[string]bool),
		devices:  make(map[string]bool),
	}
}

func (m *MemoryRevocation) RevokeJTI(jti string)    { m.jtis[jti] = true }
func (m *MemoryRevocation) RevokeLicense(id string) { m.licenses[id] = true }
func (m *MemoryRevocation) RevokeDevice(id string)  { m.devices[id] = true }

func (m *MemoryRevocation) IsRevoked(jti, licenseID, deviceID string) bool {
	if m.jtis[jti] || m.licenses[licenseID] || m.devices[deviceID] {
		return true
	}
	return false
}

// Issue creates a signed access ticket JWT.
func Issue(cfg IssuerConfig, licenseID, deviceID, role, plan string, permissions, locations []string) (string, error) {
	if cfg.TTL == 0 {
		cfg.TTL = 15 * time.Minute
	}
	now := time.Now()
	jti, err := randomID("tkt")
	if err != nil {
		return "", err
	}

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			Issuer:    cfg.Issuer,
			Audience:  jwt.ClaimStrings{cfg.Audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(cfg.TTL)),
		},
		LicenseID:   licenseID,
		DeviceID:    deviceID,
		Role:        role,
		Plan:        plan,
		Permissions: permissions,
		Locations:   locations,
		ProtocolVer: "NVP/1",
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = cfg.KeyID
	return token.SignedString(cfg.PrivateKey)
}

// Verify validates a ticket string and returns claims.
func Verify(cfg VerifierConfig, tokenString, expectedDeviceID, expectedNodeID string) (*Claims, error) {
	keyFunc := func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, ErrWrongAlgorithm
		}
		if !allowedAlgorithms[t.Method.Alg()] {
			return nil, ErrWrongAlgorithm
		}
		kid, _ := t.Header["kid"].(string)
		pub, ok := cfg.PublicKeys[kid]
		if !ok {
			return nil, fmt.Errorf("unknown key id: %s", kid)
		}
		return pub, nil
	}

	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithAudience(cfg.Audience),
		jwt.WithIssuer(cfg.Issuer),
	)

	token, err := parser.ParseWithClaims(tokenString, &Claims{}, keyFunc)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpired
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	if expectedDeviceID != "" && claims.DeviceID != expectedDeviceID {
		return nil, ErrWrongDevice
	}

	if len(claims.NodeScope) > 0 && expectedNodeID != "" {
		allowed := false
		for _, n := range claims.NodeScope {
			if n == expectedNodeID {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, ErrWrongScope
		}
	}

	if cfg.Revoked != nil && cfg.Revoked.IsRevoked(claims.ID, claims.LicenseID, claims.DeviceID) {
		return nil, ErrRevoked
	}

	return claims, nil
}

func randomID(prefix string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b)), nil
}
