# Nyxveil Control Plane 1.3.2

## Focus

Production hardening for installer bootstrap contract alignment and remote UpdateNodeLatest safety.

### Changes

- Existing-node registration requires **fresh bootstrap + PoP** (atomic consume with advertisement update)
- `UpdateNodeLatest` pins immutable `TargetVersion` from ServerReleaseService
- Backend location safety: refuse disruptive commands when last eligible healthy sibling in location; refuse concurrent disruptive ops in same location
- Claim-time recheck of location safety

Companion Server: **1.1.11** (installer always requires bootstrap; ACME registration uses parent context timeout; update marker durability + pinned manifest via marker).
