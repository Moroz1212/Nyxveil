# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-11 after authorized push of Server 1.1.13 candidate + Server CI verification.  
> Product SHA (Server CI GREEN): `8fc385335a91aa753b879234999f29a2d025abfb`  
> Docs tip HEAD (pushed): `812732acfccf8f398afbbc2eaa7b888c08323cc8`  
> GitHub branch-protection note from prior audit: `main` reported **unprotected**

## How to use this file safely

This is a snapshot, not a desired-state manifest.

At the start of every future session, compare it with the live repository:

```bash
git rev-parse HEAD
git status --short
```

If HEAD is different, re-check versions, releases, readiness notes, and recent commits.  
Never reset or downgrade the repository to this snapshot merely because this file is older.

The user's current task overrides the "next action" section below. Never treat this file as permission to deploy, publish, connect to production, or discard local changes.

Prefer branch/worktree isolation for AI changes and require explicit user authorization before pushing `main` (except when the user explicitly authorizes that push).

## Current audited component state

| Component | Source version | Audited state |
|---|---:|---|
| Protocol | `NVP/1` | Frozen |
| Core | `1.0.0` | Frozen |
| Server node | `1.1.13` | Candidate; **Server CI GREEN** on product SHA `8fc3853`; not published as GitHub Release |
| Latest published GitHub Release | `server-v1.1.12` | Do not treat 1.1.13 as published |
| Control Plane | `1.3.2` | Unchanged by this task |
| Windows client | `1.1.2` | Unchanged |
| Android client | `1.0.0` | Unchanged |

## Frozen Core

- release: `Nyxveil-Protocol-Core-v1.0.0-FROZEN`
- SHA256: `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`
- Protected: `core/`, `server/third_party/nvp/`, `clients/windows/third_party/nvp/`

## Server 1.1.13 — push + authoritative CI

### Commits

- Product: `8fc385335a91aa753b879234999f29a2d025abfb` — `fix(server): resolve production gate path for stdin execution` (includes drained-update lifecycle + stdin gate).
- Docs handoff tip pushed: `812732acfccf8f398afbbc2eaa7b888c08323cc8` — AI coordination docs only (`AGENTS.md`, `AI_STATE.md`, `AI_CHANGELOG.md`, `PROJECT.md`).
- `git push origin main` authorized and completed: `8fc3853..812732a`.

### Server CI (authoritative)

- Workflow: `.github/workflows/server-ci.yml`
- Run ID: `34588327249`
- URL: https://github.com/Moroz1212/Nyxveil/actions/runs/34588327249
- SHA: `8fc385335a91aa753b879234999f29a2d025abfb`
- Event: `push`
- Conclusion: **success**
- Jobs: `test` PASS, `build` PASS
- Runner: ubuntu-24.04; Go from workflow: **1.24**
- Notable Linux steps PASS: lifecycle updater gate, ACME privileged bind, nftables idempotency, Linux permissions, race tests, package/verify/bytes-identity
- Artifact: `nyxveil-server-binaries` id `10194603661` size `48269882` digest `sha256:180c12fa157e0922b0a8ce582132d01c8f9e26385bdbd589b836d67a4589e74b`

Docs-only tip `812732a` does **not** match Server CI path filters (`server/**`, workflows); no Server CI run exists for `812732a`. Root `CI` may run for that tip separately — do not conflate with Server CI.

### Release / LIVE status

- Tag `server-v1.1.13`: **not** created
- GitHub Release: **not** created
- `server-v1.1.12`: untouched
- LIVE drain→update→undrain: **not** performed
- Production deploy: **not** performed

`READY FOR server-v1.1.13 RELEASE` = YES (CI green on product SHA).  
`PRODUCTION READY` = NO.

## Advisory next action

1. Explicit user authorization to create tag/`server-v1.1.13` GitHub Release from **CI artifact bytes** of run `34588327249` (or a fresh Server CI on the chosen release SHA).  
2. Then disposable Ubuntu LIVE: `1.1.9` → CP drain → update to `1.1.13` → terminal success → undrain.  
3. Do not touch LIVE node `nv-test-227e939e` until that task is authorized.

This is advisory only.
