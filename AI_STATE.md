# AI_STATE.md — Nyxveil current project state

> Updated after Server 1.1.13 release-candidate finalization on 2026-09-11.  
> Source HEAD: `8fc385335a91aa753b879234999f29a2d025abfb`  
> Commit: `fix(server): resolve production gate path for stdin execution`  
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

Because `main` was unprotected at the prior audit, an AI agent must not assume GitHub will stop an accidental direct push. Prefer branch/worktree isolation for AI changes and require explicit user authorization before pushing `main`.

## Current audited component state

| Component | Source version | Audited state |
|---|---:|---|
| Protocol | `NVP/1` | Frozen |
| Core | `1.0.0` | Frozen; `docs/CORE-READINESS.md` says ready for server/client product integration |
| Server node | `1.1.13` | Local candidate; build/package/verify completed from HEAD `8fc3853` on Windows host |
| Latest published GitHub Release | `server-v1.1.12` | Do not treat 1.1.13 as published |
| Control Plane | `1.3.2` | Windows/.NET 10 + SQL Server + Blazor/API implementation present |
| Windows client | `1.1.2` | Source present; README contains stale 1.0.0 artifact wording |
| Android client | `1.0.0` | Source, bridge, scripts, Gradle project present |

## Frozen Core

Source of truth:

`docs/CORE-READINESS.md`

Frozen release identity used by server/client vendoring:

- release: `Nyxveil-Protocol-Core-v1.0.0-FROZEN`
- SHA256: `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`

Protected default paths:

- `core/`
- `server/third_party/nvp/`
- `clients/windows/third_party/nvp/`

Important: current root CI explicitly says **do not rewrite Frozen Core with gofmt**. It excludes `core/platform/windows` from Linux vet/test and tests that package on Windows separately.

## Server node — 1.1.13 candidate status

`server/VERSION` = `1.1.13`  
`internal/version.ServerVersion` / `CLIVersion` = `1.1.13`  
Module: `github.com/nyxveil/server`  
Declared Go: `1.24` (Server CI). Local Windows host used Go `1.27.0` for cross-compile only — **GitHub Server CI Go 1.24 remains the authoritative binary producer**.

Documented in `server/SERVER-1.1.13.md`:

- Lifecycle-aware remote update/rollback (drained/maintenance ≠ active-listener failure).
- Strict DataplaneOK retained for active nodes.
- Old ctl (1.1.9) handoff / terminal journal / undrain-by-CP semantics.
- `production-gate` updater mode + stdin/`bash -s` path fix (`BASH_SOURCE` unbound).

### Finalization completed 2026-09-11 (this continuation)

From HEAD `8fc385335a91aa753b879234999f29a2d025abfb`:

- Fresh wipe of `server/dist/` then `build-release.sh` + `package-release.sh`.
- `verify-release.sh`, `verify-artifact-set.sh`, `assert-release-bytes-identity.sh` → PASS for `1.1.13`.
- Frozen Core assert → PASS (`7b13097…`).
- Local Go tests `go test ./...` → PASS (Windows).
- `go vet ./...` → PASS.
- gofmt against Git index LF blobs → PASS (working-tree CRLF on Windows makes bare `gofmt -l` false-fail).
- Update lifecycle Go packages (`health`, `nyxveilctl`, `runtime`) → PASS.
- stdin production-gate init (`bash -s`) → PASS.
- Shell `test-update-lifecycle-gate.sh` full Python scenario block → SKIP locally (Windows Store `python3` stub; needs real Python/Linux CI).

No server product source was modified during this finalization continuation.

### Not done / not claimed

- No `git push`, tag, or GitHub Release for `server-v1.1.13`.
- No LIVE Ubuntu 24.04 drain→update→undrain E2E.
- No production node touch.
- Local Windows cross-compile bytes are **not** production-publishable substitutes for Server CI artifacts (Go toolchain differs).

## Control Plane / licensing

`licensing/VERSION` = `1.3.2` (unchanged by this task).

## Windows / Android clients

Unchanged by this task. See prior audit notes for README drift.

## Advisory next action

1. Push HEAD (or a PR branch) so GitHub **Server CI** (ubuntu-24.04, Go 1.24) produces authoritative artifacts.  
2. Only after green Server CI: operator-authorized disposable Ubuntu 24.04 LIVE drained-update gate (`1.1.9` → `1.1.13`).  
3. Do **not** publish `server-v1.1.13` or touch LIVE until user explicitly authorizes.

This is advisory only.
