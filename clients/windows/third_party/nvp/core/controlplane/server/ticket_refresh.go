package server

import (
	"errors"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/controlplane/model"
)

var (
	errRefreshNoLocations = errors.New("refresh left no allowed locations")
	errRefreshNoNodeScope = errors.New("refresh left empty node scope intersection")
)

// buildRefreshTicket builds a NEW access ticket from CURRENT license/device state
// intersected with the OLD ticket grant. Never widens security scope.
//
// Locations = intersection(old.Locations, current license allowlist)
//
//	(if old had no Locations and license is unrestricted, keep empty/unrestricted semantics)
//
// NodeScope = intersection(old.NodeScope, currentlyAllowedNodes)
//
//	If old NodeScope is empty → remains empty (location-scoped ticket).
//	If license/policy does not restrict nodes (allowedNodes empty) → preserve old NodeScope
//	exactly (do NOT clear it — that would widen the grant).
//
// Role/Permissions always from CURRENT plan.
func buildRefreshTicket(cfg ticket.IssuerConfig, old *ticket.Claims, lic *model.LicenseRecord, devicePub []byte) (string, error) {
	return BuildRefreshTicketWithNodePolicy(cfg, old, lic, devicePub, nil)
}

// BuildRefreshTicketWithNodePolicy is like buildRefreshTicket with an optional
// administrative node allowlist (empty = unrestricted within locations).
func BuildRefreshTicketWithNodePolicy(cfg ticket.IssuerConfig, old *ticket.Claims, lic *model.LicenseRecord, devicePub []byte, allowedNodes []string) (string, error) {
	role := ticket.RoleForPlan(lic.Plan)
	perms := ticket.PermissionsForPlan(lic.Plan)
	locations := refreshLocations(old.Locations, lic.Locations)
	if len(lic.Locations) > 0 && len(locations) == 0 {
		return "", errRefreshNoLocations
	}
	nodeScope, err := refreshNodeScope(old.NodeScope, allowedNodes)
	if err != nil {
		return "", err
	}
	return ticket.IssueScoped(cfg, lic.LicenseID, old.DeviceID, role, lic.Plan, perms, locations, nodeScope, devicePub)
}

func refreshLocations(oldLocs, allowed []string) []string {
	if len(allowed) == 0 {
		// Unrestricted license: keep old locations as-is (no expansion possible).
		return append([]string(nil), oldLocs...)
	}
	if len(oldLocs) == 0 {
		// Old ticket was unrestricted geographically but license now has an allowlist —
		// new grant is bounded by current license only (not a widening of old grant).
		return append([]string(nil), allowed...)
	}
	return intersectStrings(oldLocs, allowed)
}

func refreshNodeScope(oldScope, allowedNodes []string) ([]string, error) {
	if len(oldScope) == 0 {
		// Location-scoped ticket remains location-scoped.
		return nil, nil
	}
	if len(allowedNodes) == 0 {
		// No administrative node restriction: preserve prior NodeScope (never clear).
		return append([]string(nil), oldScope...), nil
	}
	out := intersectStrings(oldScope, allowedNodes)
	if len(out) == 0 {
		return nil, errRefreshNoNodeScope
	}
	return out, nil
}

func intersectStrings(a, b []string) []string {
	set := make(map[string]struct{}, len(b))
	for _, x := range b {
		set[x] = struct{}{}
	}
	out := make([]string, 0, len(a))
	seen := make(map[string]struct{}, len(a))
	for _, x := range a {
		if _, ok := set[x]; !ok {
			continue
		}
		if _, dup := seen[x]; dup {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return out
}
