# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-12 after **Server 1.1.16** + **Control Plane 1.3.7** releases.  
> Schema remains **5**. Frozen Core unchanged.  
> LIVE gates: **BLOCKED** (no authorized production access).  
> Preserved dirty: `licensing/tests/CoreInterop/verify-signed/go.mod`.

## How to use this file safely

This is a snapshot, not a desired-state manifest. Never reset the repo to match it.

## Session baseline → product

| Fact | Value |
|---|---|
| Initial HEAD | `da1e09c1b279ff8e53616cb8a8913da8e9ca0e69` |
| Product / main HEAD | `d9a9902946d583de5a0ad321772be6a7e2d6782b` |
| PR | https://github.com/Moroz1212/Nyxveil/pull/4 |

## Component versions

| Component | Version | Release |
|---|---:|---|
| Protocol | NVP/1 | Frozen |
| Core | 1.0.0 | Frozen hash `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b` |
| Server | 1.1.16 | https://github.com/Moroz1212/Nyxveil/releases/tag/server-v1.1.16 |
| Control Plane | 1.3.7 | https://github.com/Moroz1212/Nyxveil/releases/tag/control-plane-v1.3.7 |
| Windows client | 1.1.2 | Unchanged |
| Android client | 1.0.0 | Unchanged |

## CI / release evidence

- Server CI (main push): https://github.com/Moroz1212/Nyxveil/actions/runs/34684116451 — SUCCESS  
  Artifact `nyxveil-server-binaries` digest `sha256:e4a442a2abe7e2d2fcbcf5701080686403b902ec740d8c5461d9dc798d31a4f6`
- Server Release workflow: SUCCESS for `server-v1.1.16`
- Control Plane CI (main push): https://github.com/Moroz1212/Nyxveil/actions/runs/34684116427 — SUCCESS  
  ZIP SHA256 `4FDBB98D303081C4102DFFAC4BF2CBC201852B420931011D55552C7CEC571465` (download-back matched)
- Tags immutable: `server-v1.1.14`=`524d315…`, `server-v1.1.15`=`197a533…` unchanged

## Root causes fixed in tree

1. **Server ACME:** privileged `MigrateACMEState` on update before health; runtime `ValidateRuntimeACME` only.
2. **CP self-update:** LocalSystem `NyxveilControlPlaneUpdater` + request.json handoff; true rollback; result.json reconcile; config nesting; MFA modal UX.

## LIVE (blocked)

- CP 1.3.5 → 1.3.7 via elevated `production-deploy.ps1`
- Server 1.1.15 → 1.1.16 via CP (no manual chown)
- RenewCertificate after migration

## Advisory next action

Authorized operator: deploy CP 1.3.7 with production-deploy, then update Server 1.1.16 and renew cert — **no manual FS/ACL repairs**.
