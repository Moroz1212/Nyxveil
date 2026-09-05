package ticket

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
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
	ErrWrongLocation  = errors.New("wrong location")
	ErrSessionBinding = errors.New("session binding failed")
)

// Canonical ticket permission for establishing a VPN session.
const PermissionConnect = "connect"

const maxIssuedAtSkew = 5 * time.Minute

// HasPermission reports whether claims include the named permission.
func (c *Claims) HasPermission(perm string) bool {
	if c == nil {
		return false
	}
	for _, p := range c.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// PermissionsForPlan returns the permission set for a license plan.
// Normal plans always include PermissionConnect.
func PermissionsForPlan(plan string) []string {
	_ = plan
	return []string{PermissionConnect}
}

// RoleForPlan maps a license plan to the ticket role claim.
func RoleForPlan(plan string) string {
	if plan == "master" {
		return "master"
	}
	return "user"
}

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
	DevicePub   []byte   `json:"device_pub,omitempty"`
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
	mu       sync.RWMutex
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

func (m *MemoryRevocation) RevokeJTI(jti string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jtis[jti] = true
}
func (m *MemoryRevocation) RevokeLicense(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.licenses[id] = true
}
func (m *MemoryRevocation) RevokeDevice(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.devices[id] = true
}

func (m *MemoryRevocation) IsRevoked(jti, licenseID, deviceID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.jtis[jti] || m.licenses[licenseID] || m.devices[deviceID] {
		return true
	}
	return false
}

// Snapshot returns copies of revoked IDs for API responses.
func (m *MemoryRevocation) Snapshot() (jtis, licenses, devices []string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for k := range m.jtis {
		jtis = append(jtis, k)
	}
	for k := range m.licenses {
		licenses = append(licenses, k)
	}
	for k := range m.devices {
		devices = append(devices, k)
	}
	return jtis, licenses, devices
}

// Issue creates a signed access ticket JWT.
func Issue(cfg IssuerConfig, licenseID, deviceID, role, plan string, permissions, locations []string) (string, error) {
	return IssueWithDevice(cfg, licenseID, deviceID, role, plan, permissions, locations, nil)
}

// IssueWithDevice creates a ticket bound to a device Ed25519 public key.
func IssueWithDevice(cfg IssuerConfig, licenseID, deviceID, role, plan string, permissions, locations []string, devicePub ed25519.PublicKey) (string, error) {
	return IssueScoped(cfg, licenseID, deviceID, role, plan, permissions, locations, nil, devicePub)
}

// IssueScoped creates a ticket bound to a device key and optional node scope.
func IssueScoped(cfg IssuerConfig, licenseID, deviceID, role, plan string, permissions, locations, nodeScope []string, devicePub ed25519.PublicKey) (string, error) {
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
		NodeScope:   nodeScope,
		ProtocolVer: "NVP/1",
		DevicePub:   devicePub,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = cfg.KeyID
	return token.SignedString(cfg.PrivateKey)
}

// Reissue creates a new ticket preserving security-sensitive claims from old.
// Prefer Control Plane refresh that rebuilds from the CURRENT license instead —
// Reissue copies old Role/Permissions/Locations/NodeScope and can retain stale rights.
func Reissue(cfg IssuerConfig, old *Claims) (string, error) {
	if old == nil {
		return "", ErrInvalidToken
	}
	return IssueScoped(cfg, old.LicenseID, old.DeviceID, old.Role, old.Plan,
		append([]string(nil), old.Permissions...),
		append([]string(nil), old.Locations...),
		append([]string(nil), old.NodeScope...),
		append([]byte(nil), old.DevicePub...),
	)
}

// PeekClaims parses ticket claims without cryptographic verification.
// Used only for client-side routing hints (e.g. failover location allowlists)
// after a ticket was freshly issued by a trusted Control Plane.
func PeekClaims(tokenString string) (*Claims, error) {
	parser := jwt.NewParser()
	tok, _, err := parser.ParseUnverified(tokenString, &Claims{})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	claims, ok := tok.Claims.(*Claims)
	if !ok {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// Verify validates a ticket string and returns claims.
func Verify(cfg VerifierConfig, tokenString, expectedDeviceID, expectedNodeID string) (*Claims, error) {
	return VerifyAt(cfg, tokenString, expectedDeviceID, expectedNodeID, "")
}

// VerifyIdentity validates crypto, expiry, device binding, and revocation without
// enforcing node/location scope (used for ticket refresh and catalog auth).
func VerifyIdentity(cfg VerifierConfig, tokenString, expectedDeviceID string) (*Claims, error) {
	return verifyClaims(cfg, tokenString, expectedDeviceID, "", "", false)
}

// VerifyAt validates a ticket with optional node and location scope checks.
func VerifyAt(cfg VerifierConfig, tokenString, expectedDeviceID, expectedNodeID, expectedLocationID string) (*Claims, error) {
	return verifyClaims(cfg, tokenString, expectedDeviceID, expectedNodeID, expectedLocationID, true)
}

func verifyClaims(cfg VerifierConfig, tokenString, expectedDeviceID, expectedNodeID, expectedLocationID string, checkScope bool) (*Claims, error) {
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

	if claims.ProtocolVer != "NVP/1" {
		return nil, ErrInvalidToken
	}

	if claims.IssuedAt != nil {
		iat := claims.IssuedAt.Time
		if iat.After(time.Now().Add(maxIssuedAtSkew)) {
			return nil, ErrInvalidToken
		}
	}

	if expectedDeviceID != "" && claims.DeviceID != expectedDeviceID {
		return nil, ErrWrongDevice
	}

	if checkScope {
		if len(claims.NodeScope) > 0 {
			if expectedNodeID == "" {
				return nil, ErrWrongScope
			}
			if !containsString(claims.NodeScope, expectedNodeID) {
				return nil, ErrWrongScope
			}
		}

		if len(claims.Locations) > 0 {
			if expectedLocationID == "" {
				return nil, ErrWrongLocation
			}
			if !containsString(claims.Locations, expectedLocationID) {
				return nil, ErrWrongLocation
			}
		}
	}

	if cfg.Revoked != nil && cfg.Revoked.IsRevoked(claims.ID, claims.LicenseID, claims.DeviceID) {
		return nil, ErrRevoked
	}

	if len(claims.DevicePub) != ed25519.PublicKeySize {
		return nil, ErrSessionBinding
	}

	return claims, nil
}

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func randomID(prefix string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b)), nil
}
