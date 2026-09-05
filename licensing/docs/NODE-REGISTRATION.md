# Node registration

## Flow

1. Admin creates a **bootstrap token** (optional allowed location, max uses, expiry)
2. Raw bootstrap token shown once in admin UI
3. Node calls `POST /api/v1/nodes/register` with bootstrap token + identity material (`location_id`, Ed25519 `public_key`, …)
4. Control Plane validates token with **atomic consume** (`UPDATE ... WHERE UsedCount < MaxUses`), creates the node, stores credential public key for Frozen Core `nodeauth`
5. Subsequent node calls use Core node tokens (and optional legacy bearer if enabled)

## Idempotency / re-register

Existing `node_id` requires proof-of-possession of the registered key (Core node token). Bootstrap cannot reset an existing node's public key.

## Location binding

Bootstrap tokens may constrain `allowed_location`. Nodes register into a **`location_id`** that participates in catalog and ticket scope.

## After registration

- Heartbeats update health/metrics (Core `nodeauth` anti-replay via `LastCoreTokenUnix`)
- Config pull respects enabled / draining / maintenance / test_only
- Test-only nodes are hidden from normal users; master/test entitlements may see them per policy
