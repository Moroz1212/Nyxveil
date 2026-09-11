# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-11 after local Server **1.1.14** candidate `524d3151018d76d413e312b2d9bbd5e54fdd2cf5`.  
> Control Plane **1.3.3** remains pushed + CI green (not necessarily deployed).  
> Published Server release remains `server-v1.1.13` (`8fc3853…`).  
> Server 1.1.14 is **local only** — no push/tag/release/deploy.  
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
| Server node | `1.1.14` | **Local candidate** — durable CP CommandID capture + marker-missing recovery |
| Latest published GitHub Release | `server-v1.1.13` | Unchanged |
| Control Plane | `1.3.3` | Pushed; CI green; deploy status depends on operator |
| Windows client | `1.1.2` | Unchanged |
| Android client | `1.0.0` | Unchanged |

## Frozen Core

- SHA256: `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`
- Assert this session: **PASS**

## Server 1.1.14 (local)

Root cause addressed: after successful 1.1.9→1.1.13 handoff, legacy parent deleted
`update-command.json` while reporting with canceled context; 1.1.13 could not finish
`updated_healthy` from a committed journal that lacked `command_id`.

Fix: `update-resume` captures CommandID from the still-present marker into the durable
transaction before restart; daemon recovers terminal outcomes from correlated journals
when the marker is gone; fail-closed without guessing; pending-result retry/idempotency.

### Not done

- Push / tag / GitHub Release for Server 1.1.14
- Disposable Ubuntu LIVE regression with real Server 1.1.9 artifact
- Production deploy

`READY FOR SERVER CI` = YES (local Go tests/gates green)  
`PRODUCTION READY` = NO  
`READY FOR LIVE 1.1.9 → 1.1.14 TEST` = YES (after CI/package)

## Advisory next action

1. Push Server 1.1.14 → authoritative Server CI  
2. Package from CI bytes  
3. Disposable Ubuntu LIVE: real 1.1.9 → 1.1.14 automatic `updated_healthy`  
4. Only then discuss `server-v1.1.14` release  

This is advisory only.
