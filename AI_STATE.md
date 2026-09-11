# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-11 after authoritative Server CI **PASS** for Server **1.1.14**.  
> Product SHA: `524d3151018d76d413e312b2d9bbd5e54fdd2cf5`  
> CI branch: `ci/server-1.1.14-524d315` (exact product SHA)  
> Server CI run: `34633830504` success — artifact `nyxveil-server-binaries` ready  
> **No** tag / GitHub Release / LIVE deploy / `origin/main` change from this CI stage.  
> Control Plane **1.3.3** remains previously pushed + CI green.  
> GitHub branch-protection note from prior audit: `main` reported **unprotected**

## How to use this file safely

This is a snapshot, not a desired-state manifest.

```bash
git rev-parse HEAD
git status --short
```

If HEAD differs, re-derive facts from the live repository. Never reset to this snapshot.

Prefer branch/worktree isolation for AI changes. Require explicit authorization before push/tag/release/deploy.

## Current audited component state

| Component | Source version | Audited state |
|---|---:|---|
| Protocol | `NVP/1` | Frozen |
| Core | `1.0.0` | Frozen |
| Server node | `1.1.14` | Authoritative Server CI **PASS** on product SHA; CI artifact ready; **not tagged/released** |
| Latest published GitHub Release | `server-v1.1.13` | Unchanged |
| Control Plane | `1.3.3` | Pushed; CI green; deploy status depends on operator |
| Windows client | `1.1.2` | Unchanged |
| Android client | `1.0.0` | Unchanged |

## Frozen Core

- SHA256: `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`
- Assert / artifact provenance: **PASS**

## Server 1.1.14 CI provenance

```
Product SHA 524d3151018d76d413e312b2d9bbd5e54fdd2cf5
  → branch ci/server-1.1.14-524d315 (exact)
  → Server CI run 34633830504 (success, push)
     https://github.com/Moroz1212/Nyxveil/actions/runs/34633830504
  → test job success; build job success
  → artifact nyxveil-server-binaries id 10276778433
     digest sha256:7aa620916f764c012949d39651f60bf4723f95713613c2d524879a9216042844
     size_in_bytes 48322528
     VERSION=1.1.14; amd64/arm64 binaries + manifests + SHA256SUMS present
```

`origin/main` remains at CP tip (`8d83268…`) — not updated by this CI branch push.

### Not done

- Tag `server-v1.1.14` / GitHub Release
- Merge/push product to `main` (optional; not this stage)
- Disposable Ubuntu LIVE 1.1.9 → 1.1.14 automatic `updated_healthy`
- Production deploy

`SERVER 1.1.14 AUTHORITATIVE CI` = YES  
`PRODUCTION READY` = NO  
`READY FOR IMMUTABLE RELEASE` = YES (CI bytes) — then LIVE gate before production

## Advisory next action

1. Controlled immutable `server-v1.1.14` release from CI artifact bytes (authorized separately)  
2. Disposable Ubuntu LIVE: real 1.1.9 → 1.1.14 automatic terminal report  
3. Only after LIVE PASS discuss production rollout  

This is advisory only.
