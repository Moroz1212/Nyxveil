# AGENTS.md — Nyxveil AI working rules

## Purpose

Shared safety and continuity rules for every AI agent working on Nyxveil.

These rules are designed for Cursor, Codex, Claude Code, Gemini CLI, Copilot, Cline, or another coding agent. They do not grant permission to publish, deploy, connect to production infrastructure, or discard existing work.

## Session start — mandatory

Before editing anything:

1. Read `AGENTS.md`.
2. Read `AI_STATE.md`.
3. Read the newest entries in `AI_CHANGELOG.md`.
4. Read `PROJECT.md`.
5. Read the current component-specific README/docs and its CI workflow.
6. Inspect the live worktree:

```bash
git rev-parse HEAD
git status --short
git diff --stat
```

The commit recorded in `AI_STATE.md` is a historical snapshot, not a checkout target.  
If current HEAD differs, treat version/status facts in `AI_STATE.md` as potentially stale and re-derive them from the live repository. Never reset the repository back to the snapshot just to match the state file.

At the audited snapshot, GitHub reports `main` as **unprotected**. This makes an accidental direct push more dangerous. Prefer a task branch/worktree for AI changes when Git operations are involved, and never push directly to `main` unless the user explicitly requests that action.

The user's current explicit task takes precedence over the "recommended next action" in `AI_STATE.md`. State files describe context; they are not autonomous work orders.

## Protect existing work

Before changing files:

- Preserve all pre-existing uncommitted changes.
- If the task overlaps a dirty file, inspect the diff first and modify around the existing work.
- Do not overwrite, revert, normalize, or reformat unrelated changes.
- Do not use destructive Git commands such as `git reset --hard`, `git clean -fd/-fdx`, forced checkout/restore of user changes, history rewrite, or force-push unless the user explicitly requests that exact destructive action and its consequences are understood.
- Do not delete backups, historical release artifacts, compatibility fixtures, migration history, or diagnostic evidence merely because they look unused.
- Do not let two AI agents write to the same worktree concurrently. Parallel work must use separate branches/worktrees and be reconciled explicitly.

## Repository baseline

- Repository: `Moroz1212/Nyxveil`
- Default branch: `main`
- Protocol: `NVP/1`
- Frozen protocol Core: `1.0.0`
- Frozen Core release SHA256: `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`

Static baseline recorded in `AI_STATE.md` was verified at:

`8fc385335a91aa753b879234999f29a2d025abfb`

Do not assume that commit remains current in future sessions.

## Frozen Core — default immutability

The following are protected by default:

- `core/**`
- `server/third_party/nvp/**`
- `clients/windows/third_party/nvp/**`

Do not edit them, even for formatting, cleanup, warning suppression, dependency updates, or convenience.

The current root CI explicitly treats `core/` as frozen and does **not** rewrite it with `gofmt`.  
The server build/release gates hash-lock its vendored Core.

A Core/NVP revision requires an explicit user request that clearly authorizes a protocol/Core revision. Such work must be treated as a separate compatibility project: new versioning, tests, provenance/hash updates, and coordinated vendored-copy updates. Never silently patch the frozen copy in place.

Current compatibility rules that product code must preserve:

- TLS transport: TLS 1.3 over TCP with no application ALPN.
- QUIC transport: real HTTP/3 CONNECT with ALPN `h3`.
- Automatic failover is same-location only.
- Cross-location switching requires a new application session.
- Ticket refresh must never widen authorization.
- `OpenSession` success means the session reached `ESTABLISHED`.
- MASQUE is not an enabled NVP/1 transport.

Do not claim guaranteed undetectability, censorship resistance, DPI-proof operation, or "100% security".

## Component boundaries

### Server

- Production target: Ubuntu 24.04, systemd, TUN, nftables.
- Preserve node identity and persisted state across update/rollback unless an explicit migration requires otherwise.
- Preserve meaningful lifecycle states such as active, drained, and maintenance.
- Do not weaken signed manifest, release provenance, SHA-256, TLS, auth, rollback, or fail-closed checks to make a test pass.
- Current 1.1.13 candidate behavior intentionally distinguishes drained/maintenance lifecycle from active listener readiness.
- `server/third_party/nvp` is frozen; server-owned fixes belong in server-owned code, not the vendored Core.

### Control Plane

- Production stack: Windows/.NET 10/SQL Server/HTTPS.
- `Master` is a role, not a hidden bypass.
- Tickets are location-scoped by default.
- Refresh must rebuild authorization from current entitlements and never widen scope.
- Schema/migration history must not be rewritten casually. New schema work should be additive/compatible unless the task explicitly defines a migration strategy.
- Never commit production credentials, KEKs, signing private keys, bootstrap tokens, PFX passwords, DPAPI exports, or other secrets.

### Windows client

- Preserve NVP/1 compatibility and service/GUI privilege separation.
- Its Go engine currently uses its own toolchain declaration; do not normalize toolchain versions across components without a concrete compatibility reason.
- The vendored Core under `clients/windows/third_party/nvp` is protected.
- Elevated service/network/install gates can alter the host; do not run them automatically on the user's primary workstation without explicit authorization.

### Android client

- VPN lifetime belongs to `VpnService`, not the Activity.
- Control Plane URL is currently built in rather than user-editable.
- Android bridge uses the frozen Core through the Windows vendored copy; do not modify that Core to solve Android issues.
- Installing an APK on a connected device is an external side effect. Build by default; install only when explicitly requested or when the task clearly authorizes device testing.

## Toolchain versions are component-specific

Do not "clean up" or unify these merely because they differ.

At the audited snapshot:

- root/Core module: Go 1.24
- server module: Go 1.24
- Windows engine module: Go 1.25.0
- Android bridge Go module: Go 1.26.0
- Control Plane: .NET 10

If these values change in the live repository, follow the live component configuration.

## Version discipline

Versions are independent:

- Protocol/Core
- Server
- Control Plane
- Windows client
- Android client

Do **not** bump a component version merely because code changed.

Only change `VERSION`, release notes, tags, package names, or release metadata when:

- the user explicitly requests a version/release change, or
- the active task explicitly includes preparation of the next version and the required version is known.

A source `VERSION` does not prove that a release was published. Release status requires checking the relevant tag/GitHub Release/assets.

## Verification — use live CI as source of truth

CI workflows are more authoritative than old README command examples. Before running a full gate, inspect:

- `.github/workflows/ci.yml`
- `.github/workflows/server-ci.yml`
- `.github/workflows/control-plane-ci.yml`
- component scripts/docs

### Frozen Core/root

Never run `gofmt -w ./core` or another write-format operation over the frozen Core.

On Linux, the current root CI intentionally excludes `core/platform/windows` from Linux vet/test and tests that package separately on Windows. Mirror the current workflow rather than substituting a generic repo-wide `go test ./...`.

For a product-only change, do not touch Core simply to make unrelated tests cleaner.

### Server

Use the current `server-ci.yml` and server scripts as the full gate reference.  
When server code is changed, include the frozen-Core assertion where relevant:

```bash
cd server
bash scripts/assert-frozen-core.sh
```

Live clean-host/update/rollback validation changes system state and must only be run on an explicitly authorized disposable Ubuntu 24.04 test host.

### Control Plane

Use `control-plane-ci.yml` as the exact build/test/package reference.  
Do not infer that generic `dotnet test` is equivalent to the workflow's LocalDB integration and production package gates.

### Windows client

Use the component scripts/docs. Packaging is non-production work; elevated final gates may install/control services and networking and therefore require an appropriate authorized Windows test host.

### Android client

Safe default is build/test. Do not use the `-Install` path unless device installation/testing is authorized.

## Post-change safety checks

Before declaring a task complete:

1. Inspect `git status --short`.
2. Inspect `git diff --stat` and the actual diff.
3. Confirm no unrelated files changed.
4. Confirm protected Core paths were not changed by this task unless an explicit Core revision was authorized.
5. Run the relevant component tests/gates that are safe and available.
6. Never claim an unrun test passed.
7. Never claim live readiness from unit/fixture/cross-compile tests alone.

## External-action boundary

Do not, unless the user explicitly requests the action:

- push commits,
- create/modify tags,
- publish GitHub Releases,
- deploy to a VPS/production host,
- restart production services,
- rotate production keys/certificates,
- change DNS,
- mutate production databases,
- install/uninstall software or network drivers on the user's main machine.

Preparation of local code, patches, docs, test candidates, and release artifacts is allowed without publishing.

## AI handoff after meaningful work

After a meaningful task:

1. Update `AI_STATE.md` only with facts verified from the current repository/test evidence.
2. Append a new entry to `AI_CHANGELOG.md`; do not rewrite old entries.
3. Record:
   - goal,
   - baseline HEAD,
   - files changed,
   - behavior changed,
   - version metadata changed or not changed,
   - tests actually run and results,
   - tests not run,
   - compatibility implications,
   - unresolved risks/blockers,
   - suggested next action.
4. If HEAD/state changed since the snapshot, update the snapshot metadata rather than restoring old code.
5. Unknown facts must remain explicitly unknown.
