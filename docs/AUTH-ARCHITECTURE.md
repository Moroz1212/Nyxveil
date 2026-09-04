# Authentication Architecture

## Overview

```
License Credential (nyx_lic_...)
        ↓ (Client ↔ Control Plane only)
Device Identity (Ed25519 keypair, local)
        ↓
Short-lived Access Ticket (JWT Ed25519)
        ↓ (Client → VPN Node)
VPN Session AUTH
```

## License Credential

- Format: `nyx_lic_<high-entropy-random>`
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

### Validation (VPN Node)

Strict allowlist: `EdDSA` only. Rejects:
- `alg=none`
- Unexpected algorithms
- Expired / not-yet-valid
- Wrong audience / issuer
- Wrong device_id
- Wrong node_scope
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

## Key Rotation

Control Plane maintains `current` + `next` signing keys with `kid` header. Nodes accept both during rotation window.

Independent security/cryptographic audit: **NOT PERFORMED**
