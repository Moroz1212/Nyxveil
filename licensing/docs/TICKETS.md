# Access tickets (NVP/1)

## Purpose

Short-lived, device-bound credentials authorizing a VPN session to nodes within an allowed scope.

## Claims (Frozen Core)

| Claim | Value / notes |
|-------|----------------|
| `aud` | **`nvp-node`** (exact; must match Frozen Core verifier) |
| `iss` | Control Plane issuer (`Signing:Issuer`) |
| `locations` | Allowed location codes / ids from license scope |
| `node_scope` | Optional node pin list |
| `protocol_version` | `NVP/1` |
| `device_pub` | Activated device Ed25519 public key |

Interop gate: `scripts/test-core-interop.ps1` (`TICKET_CS_TO_GO`).

## Invariants (frozen)

1. **Location-scoped** — tickets carry allowed locations; they are not global network passes
2. **Same-location failover only** — automatic failover stays inside the ticket location
3. **Refresh never widens** — refresh may rotate lifetime/crypto material but must not add locations or broaden node scope
4. **Device-bound** — ticket is tied to the activated device public key
5. **`connect` permission** — required for session establishment
6. **Master is a role** — elevated entitlements via plan/role, not protocol escape hatches

## Issue

`POST /api/v1/ticket/issue` validates license + device, computes scope from license allow-list and available nodes, signs ticket with current Ed25519 key.

## Refresh

`POST /api/v1/ticket/refresh` verifies prior ticket and issues a successor with **equal or narrower** scope.

## Revocation

Revoked JTIs / licenses / devices appear in node revocation snapshots. Nodes must enforce revocation lists.
