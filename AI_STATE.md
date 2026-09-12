# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-12 after Server **1.1.16** + Control Plane **1.3.7** patch work on branch `patch-1.1.16-1.3.7`.  
> Schema remains **5**. Frozen Core unchanged.  
> LIVE gates: **BLOCKED** (no authorized production access on this host).  
> Preserved dirty (uncommitted): `licensing/tests/CoreInterop/verify-signed/go.mod`.

## How to use this file safely

This is a snapshot, not a desired-state manifest. Never reset the repo to match it.

## Session baseline

- Initial HEAD (branch start from main): `da1e09c1b279ff8e53616cb8a8913da8e9ca0e69`
- Branch: `patch-1.1.16-1.3.7`
- Product versions in tree: Server `1.1.16`, Control Plane `1.3.7`

## Current audited component state

| Component | Source version | Audited state |
|---|---:|---|
| Protocol | `NVP/1` | Frozen |
| Core | `1.0.0` | Frozen (hash verified) |
| Server node | `1.1.16` | Privileged ACME migration on update; local package tests PASS; release pending CI |
| Control Plane | `1.3.7` | Privileged updater service + true rollback + result reconcile + MFA UX; local tests PASS |
| Windows client | `1.1.2` | Unchanged |
| Android client | `1.0.0` | Unchanged |

## Frozen Core

- SHA256: `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`
- `assert-frozen-core.sh`: OK

## Root causes (this patch)

### Server ACME (LIVE 1.1.15)

`/var/lib/nyxveil/acme` remained `root:root` `0700` after update. `nyxveil-server` (`User=nyxveil`) cannot chmod/chown; renewal hit permission denied. Fix: privileged `MigrateACMEState` in `nyxveilctl update` / update-resume **before** health; runtime only `ValidateRuntimeACME`.

### Control Plane self-update (LIVE 1.3.5 → 1.3.6)

Updater ran as Web service identity with Program Files RX → Access denied. Misleading `rollback_failed` without restore; stuck `ReadyForHandoff` because result.json was not ingested while service stayed Running.

## Local gates (this session)

- CP Unit: **475** PASS (474 prior + MFA UX)
- CP Integration: **130** PASS
- Server: filemeta/runtime/updater/configure/releasecontract PASS; frozen core OK
- Authoritative GitHub CI / tags / LIVE: pending

## Advisory next action

1. Push branch → green Server CI + Control Plane CI on merge to `main`
2. Tag/publish `server-v1.1.16` and `control-plane-v1.3.7` from CI artifacts only
3. LIVE: elevated `production-deploy.ps1` for CP 1.3.5→1.3.7, then CP-driven Server 1.1.15→1.1.16 + RenewCertificate (**no manual chown/chmod**)
