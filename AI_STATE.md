# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-11 after local Control Plane **1.3.3** commit `3f9129e56afcdad49ed21a4cc8beedf557a343a9`.  
> Server product / tag SHA remains: `8fc385335a91aa753b879234999f29a2d025abfb` (`server-v1.1.13`)  
> Control Plane 1.3.3 is **local commit only** — not pushed, not tagged, not deployed.  
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
| Server node | `1.1.13` | **Published** as GitHub Release `server-v1.1.13` (unchanged this task) |
| Latest published GitHub Release | `server-v1.1.13` | Exact Server CI artifact bytes |
| Control Plane | `1.3.3` | Local candidate: SuperAdmin unknown-update reconciliation; schema 5 unchanged |
| Windows client | `1.1.2` | Unchanged |
| Android client | `1.0.0` | Unchanged |

## Frozen Core

- SHA256: `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`
- Protected: `core/`, `server/third_party/nvp/`, `clients/windows/third_party/nvp/`
- Local assert this session: **PASS**

## Control Plane 1.3.3 (local)

Root cause addressed: terminal `UpdateNodeLatest` with `expired_outcome_unknown` / `outcome_unknown` / `rollback_failed` blocked location disruptive ops forever when no late node result arrives.

Implemented:

- `GetUnknownUpdateReconciliationPreviewAsync` / `ReconcileUnknownUpdateAsync`
- SuperAdmin-only evidence-gated Confirm Rollback / Confirm Updated
- Shared `RestoreAdminStateFromPayloadAsync`; forensic payload + audit `node.command.update.reconcile`
- UI on Operations + NodeDetails
- DB schema **unchanged** (still 5)

Local verification this session:

- Unit tests: 350 passed
- Integration tests (LocalDB): 125 passed
- `production-gate.ps1 -GateMode local`: RESULT=PARTIAL (expected SKIP without InstallDir DB)
- Pack + extracted package required-file check: PASS
- Frozen Core assert: PASS

### Not done

- Push / tag / GitHub Release for Control Plane
- Production CP deploy / production DB change
- LIVE reconciliation of `nv-test-227e939e`
- LIVE Server `1.1.9` → `1.1.13` update

`READY FOR CONTROL PLANE CI` = YES (local gates green)  
`PRODUCTION READY` = NO

## Advisory next action

1. Authoritative Control Plane CI on push/PR  
2. Package/deploy rehearsal  
3. Backup production CP/DB  
4. Deploy Control Plane 1.3.3  
5. UI reconcile old 1.1.9→1.1.12 command as `rolled_back_healthy`  
6. Confirm node exits Drain  
7. Separate LIVE test Server 1.1.9 → 1.1.13  

This is advisory only.
