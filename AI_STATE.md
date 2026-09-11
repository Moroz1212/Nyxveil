# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-11 after authoritative Control Plane CI **PASS** for **1.3.3**.  
> Product SHA: `3f9129e56afcdad49ed21a4cc8beedf557a343a9`  
> CI SHA (pushed tip at CI run): `133deb3d1497b996b091b1c344ff474b5d70012b`  
> Server product / tag SHA remains: `8fc385335a91aa753b879234999f29a2d025abfb` (`server-v1.1.13`)  
> Control Plane 1.3.3: pushed + CI green + release ZIP artifact uploaded — **not deployed**.  
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
| Server node | `1.1.13` | **Published** as GitHub Release `server-v1.1.13` (unchanged) |
| Latest published GitHub Release | `server-v1.1.13` | Exact Server CI artifact bytes |
| Control Plane | `1.3.3` | Pushed; Control Plane CI green; release ZIP artifact ready; **not deployed** |
| Windows client | `1.1.2` | Unchanged |
| Android client | `1.0.0` | Unchanged |

## Frozen Core

- SHA256: `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`
- Protected: `core/`, `server/third_party/nvp/`, `clients/windows/third_party/nvp/`
- Assert: **PASS**

## Control Plane 1.3.3 CI provenance

```
Product SHA 3f9129e56afcdad49ed21a4cc8beedf557a343a9
  → pushed with handoff tip 133deb3d1497b996b091b1c344ff474b5d70012b
  → Control Plane CI run 34619160877 (success, push)
     https://github.com/Moroz1212/Nyxveil/actions/runs/34619160877
  → Unit tests: 350 passed / 0 failed / 0 skipped
  → Integration tests: 125 passed / 0 failed / 0 skipped
  → Pack + extracted package validation: PASS
  → Artifact control-plane-release-zip id 10272001651
     digest sha256:fc6266fbbd73f5bbf9ee54d23f9eaa30383bf603cb7ebba947f0a3df765840a2
     size_in_bytes 42135248
     contains Nyxveil-ControlPlane-v1.3.3-release.zip (VERSION=1.3.3)
```

Schema: **5** unchanged / migration: **none**

### Not done

- Production CP deploy / production DB change
- LIVE reconciliation of `nv-test-227e939e`
- LIVE Server `1.1.9` → `1.1.13` update

`READY FOR CONTROL PLANE 1.3.3 DEPLOYMENT` = YES  
`PRODUCTION READY` = NO

## Advisory next action

Authorized production path: deploy rehearsal → backup CP/DB → deploy Control Plane 1.3.3 → UI reconcile old unknown update as `rolled_back_healthy` → then separate LIVE Server 1.1.9 → 1.1.13 test.

This is advisory only.
