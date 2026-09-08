# Nyxveil Control Plane 1.3.0

## Highlights

- Node version management: SemVer status vs latest stable `server-v*` GitHub release (cached).
- Runtime version from heartbeat (`ReportedServerVersion`).
- Remote `UpdateNodeLatest` command (SuperAdmin): node pulls signed manifest via existing updater.
- Safe signing-key rotation with `Retiring` grace (old catalog/tickets remain verifiable).
- Schema version **4**.

## Companion node

Server application **1.1.9** (NVP/1 unchanged).

## Compatibility

- Additive over 1.2.0 Infrastructure Management.
- Frozen Core / clients / ticket format unchanged.
