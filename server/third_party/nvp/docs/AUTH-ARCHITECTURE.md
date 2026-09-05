# Authentication Architecture

## Overview

```
License Credential (nyx_lic_...)
        → (Client ↔ Control Plane only)
Device Identity (Ed25519 keypair, local)
        →
Short-lived Access Ticket (JWT Ed25519)
        → (Client → VPN Node)
VPN Session AUTH
```

## License Credential

- Format: `nyx_lic_<id>:<secret>`
- Entered only in client application
- Used ONLY between Client and Licensing/Control Plane
- **Never sent to VPN nodes**

## Device Identity

- Created on first activation
- `device_id` + local Ed25519 keypair
- Private key never leaves device
- Control Plane stores public key
- Enables per-device access binding and max_devices enforcement

## Access Ticket (JWT)

Signed by Control Plane Ed25519 key. Claims:

| Claim | Purpose |
|-------|---------|
| jti | Unique ticket ID (revocation) |
| iss | Issuer validation |
| aud | Audience (`nvp-node`) |
| iat/nbf/exp | Time bounds |
| license_id | License reference |
| device_id | Device binding |
| role | user / master / test |
| plan | Subscription tier |
| permissions | Capability list |
| locations | Allowed location scope |
| node_scope | Optional node restriction |
| protocol_version | NVP/1 |

### Location-scoped tickets

Default ticket model: production connector issues tickets with `location_id` set and empty `node_id`. Control Plane fills JWT `Locations` from the license (optionally narrowed to the requested location) and leaves `NodeScope` empty so any healthy node in that location may be used during **same-location** failover.

Optional **node-scoped** tickets (`node_id` set → `NodeScope=[node_id]`) further restrict dials; the connector peeks `NodeScope` into `ConnectPolicy.AllowedNodeIDs`.

Cross-location connectivity requires a new `OpenSession` with a different location — Core does not auto-failover across locations.

### Ticket refresh from current entitlements

`POST /api/v1/ticket/refresh` verifies the old ticket against the requesting `device_id`, checks the license/device are still usable, then **rebuilds** a new ticket from the **current** license plan (role, permissions) and:

- `Locations` = intersection(old locations, current license allowlist); empty intersection → reject
- `NodeScope` = intersection with administrative node policy when present; if policy is unrestricted, **preserve** prior `NodeScope` (never clear it — that would widen); empty old `NodeScope` stays empty (location-scoped)

It does **not** `Reissue` a byte-copy of stale claims. Scope cannot escalate beyond current entitlements; wrong device ID or revoked/disabled license is rejected (`TestTicketRefreshWrongDeviceRejected`, `TestTicketRefreshRevokedLicenseRejected`, `TestRefreshAfterLicenseDowngradeDoesNotKeepOldRights`, `TestTicketRefreshPreservesNodeScope`, `TestTicketRefreshNeverWidensNodeScope`).

### Catalog authentication

`GET /api/v1/catalog` (and locations/nodes list) requires `Authorization: Bearer` with either a valid license token or a valid access ticket. Unauthenticated catalog requests are rejected. Responses are location-filtered and `test_only` nodes are omitted for role `user` (master may receive them).

### Validation (VPN Node)

Strict allowlist: `EdDSA` only. Rejects:

- `alg=none`
- Unexpected algorithms
- Expired / not-yet-valid
- Wrong audience / issuer
- Wrong device_id
- Wrong node_scope / locations
- Revoked (via local cache)

Offline verification — no Control Plane call per packet.

## Master Access

- Role `master` in licensing system
- Uses normal authentication flow
- No hardcoded backdoor or signature bypass
- Can access test_only nodes per Control Plane policy

## Revocation

| Event | Mechanism |
|-------|-----------|
| Subscription expired | Ticket exp claim |
| License revoked | Denylist + no reissue |
| Device revoked | Denylist |
| Ticket leaked | Short TTL bounds damage |
| Immediate block | Revocation cache push |

`GET /api/v1/revocation` requires **node service identity** (`requireNodeAuth`), not a user license token.

## Key Rotation

Control Plane maintains `current` + `next` signing keys with `kid` header. Nodes accept both during rotation window.

Independent security/cryptographic audit: **NOT PERFORMED**