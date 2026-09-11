# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-11 after publishing GitHub Release **server-v1.1.13**.  
> Product / tag SHA: `8fc385335a91aa753b879234999f29a2d025abfb`  
> Main docs tip may be newer than the release tag (do not conflate).  
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
| Server node | `1.1.13` | **Published** as GitHub Release `server-v1.1.13` from CI bytes |
| Latest published GitHub Release | `server-v1.1.13` | Exact Server CI artifact bytes |
| Control Plane | `1.3.2` | Unchanged by this release task |
| Windows client | `1.1.2` | Unchanged |
| Android client | `1.0.0` | Unchanged |

## Frozen Core

- SHA256: `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`
- Protected: `core/`, `server/third_party/nvp/`, `clients/windows/third_party/nvp/`

## Server 1.1.13 release provenance

```
Product SHA 8fc385335a91aa753b879234999f29a2d025abfb
  → Server CI run 34588327249 (success, push, Go 1.24)
  → artifact nyxveil-server-binaries id 10194603661
     digest sha256:180c12fa157e0922b0a8ce582132d01c8f9e26385bdbd589b836d67a4589e74b
     size 48269882
  → annotated tag server-v1.1.13 → 8fc3853
  → Server Release run 34614557064 (success, no rebuild)
  → GitHub Release id 387133550
     https://github.com/Moroz1212/Nyxveil/releases/tag/server-v1.1.13
```

Release workflow steps PASS: Frozen Core assert, download CI artifact + digest check, verify-release / verify-artifact-set / assert-release-bytes-identity, create release + upload.

Published assets: 18 (full UPLOAD-LIST). Downloaded assets verified against released SHA256SUMS = PASS.

### Not done

- LIVE `1.1.9` → drain → `1.1.13` → undrain regression (**not** run)
- Production deployment (**not** done)
- Node `nv-test-227e939e` untouched

`RELEASE PUBLISHED` = YES  
`READY FOR LIVE 1.1.9 → 1.1.13 TEST` = YES  
`PRODUCTION READY` = NO

## Advisory next action

Authorized disposable LIVE remote-update gate: Control Panel drain → old ctl handoff → Server 1.1.13 → terminal success → CP undrain → accepting/TLS/QUIC/heartbeat 1.1.13.

This is advisory only.
