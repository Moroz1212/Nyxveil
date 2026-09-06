package server

import (
	"crypto/ed25519"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/controlplane/model"
)

func parseLicenseToken(token string) (id, secret string) {
	id, secret, ok := strings.Cut(token, ":")
	if !ok {
		return token, ""
	}
	return id, secret
}

func licenseIDFromToken(token string) string {
	id, _ := parseLicenseToken(token)
	return id
}

func licenseTokenValid(lic *model.LicenseRecord, token string) bool {
	return licenseTokenValidWith(nil, lic, token)
}

// secretMatcher verifies stored license secrets (HMAC verifier / legacy / plaintext).
type secretMatcher interface {
	MatchSecret(stored, candidate string) (bool, error)
}

func licenseTokenValidWith(m secretMatcher, lic *model.LicenseRecord, token string) bool {
	if lic == nil {
		return false
	}
	id, secret := parseLicenseToken(token)
	if id != lic.LicenseID {
		return false
	}
	if lic.Secret == "" || secret == "" {
		return false
	}
	if m != nil {
		ok, err := m.MatchSecret(lic.Secret, secret)
		return err == nil && ok
	}
	return subtle.ConstantTimeCompare([]byte(secret), []byte(lic.Secret)) == 1
}

func licenseUsable(lic *model.LicenseRecord) bool {
	return lic != nil && lic.Enabled && !lic.Revoked && !time.Now().After(lic.ExpiresAt)
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func validDevicePublicKey(pk []byte) bool {
	return len(pk) == ed25519.PublicKeySize
}

func copyFilterNodes(nodes []model.NodeRegistryEntry, role string) []model.NodeRegistryEntry {
	out := make([]model.NodeRegistryEntry, 0, len(nodes))
	for _, n := range nodes {
		if role != "master" && n.TestOnly {
			continue
		}
		out = append(out, n)
	}
	return out
}

func filterNodesByLocations(nodes []model.NodeRegistryEntry, allowed []string) []model.NodeRegistryEntry {
	if len(allowed) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(allowed))
	for _, loc := range allowed {
		set[loc] = struct{}{}
	}
	out := make([]model.NodeRegistryEntry, 0, len(nodes))
	for _, n := range nodes {
		if _, ok := set[n.LocationID]; ok {
			out = append(out, n)
		}
	}
	return out
}

func filterLocationsByIDs(locations []model.Location, allowed []string) []model.Location {
	if len(allowed) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(allowed))
	for _, loc := range allowed {
		set[loc] = struct{}{}
	}
	out := make([]model.Location, 0, len(locations))
	for _, loc := range locations {
		if _, ok := set[loc.LocationID]; ok {
			out = append(out, loc)
		}
	}
	return out
}

// catalogCaller is an authenticated catalog/nodes/locations client.
type catalogCaller struct {
	Role      string
	Locations []string
}

func catalogRoleFromRequest(r *http.Request, cfg Config) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return "user"
	}
	tok := strings.TrimPrefix(h, "Bearer ")
	claims, err := ticket.VerifyIdentity(ticket.VerifierConfig{
		Issuer:     cfg.Issuer.Issuer,
		Audience:   cfg.Issuer.Audience,
		PublicKeys: PublicKeys(cfg),
	}, tok, "")
	if err != nil || claims.Role != "master" {
		return "user"
	}
	return "master"
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
}

func looksLikeLicenseToken(tok string) bool {
	id, secret, ok := strings.Cut(tok, ":")
	return ok && id != "" && secret != "" && !strings.Contains(tok, ".")
}
