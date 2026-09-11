# AI_CHANGELOG.md — Nyxveil AI handoff log

This file is append-only for AI handoffs introduced after the repository already existed.  
Git history and component release history remain authoritative for earlier development.

Do not rewrite historical entries. If a previous entry is wrong, append a correction.

---

## 2026-09-11 — Audited AI coordination baseline

Repository snapshot:

- repository: `Moroz1212/Nyxveil`
- branch: `main`
- HEAD: `8fc385335a91aa753b879234999f29a2d025abfb`
- commit: `fix(server): resolve production gate path for stdin execution`

### Handoff files introduced

- `AGENTS.md`
- `AI_STATE.md`
- `AI_CHANGELOG.md`
- `PROJECT.md`

### Verified snapshot facts

- Protocol: NVP/1.
- Frozen Core: 1.0.0.
- Frozen Core SHA256 used by vendored provenance: `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`.
- Server source: 1.1.13 local candidate.
- Latest published GitHub Release observed: `server-v1.1.12`.
- Control Plane: 1.3.2.
- Windows client `VERSION`: 1.1.2.
- Android client: 1.0.0.
- Snapshot HEAD had successful observed root `CI` and `Server CI`.

### Safety audit corrections incorporated

The handoff rules explicitly prevent:

- write-formatting Frozen Core,
- generic Linux `go test ./...` being substituted for the Core CI's platform-aware package selection,
- automatic version bumps on ordinary code changes,
- destructive Git cleanup/reset of existing work,
- concurrent AI writes in one worktree,
- autonomous push/tag/release/deploy/production actions,
- automatic elevated Windows/service/network gates,
- automatic APK installation,
- treating the snapshot commit as a rollback target.

### Known documentation drift recorded

- root README uses `client/` while actual clients are under `clients/`;
- root README understates current product implementation as scaffolding;
- Windows README references 1.0.0 artifacts while `VERSION` is 1.1.2;
- Windows `THIRD_PARTY_CORE.md` contains copied server-specific wording/path references.

### Product code changes

None.

### Tests executed by preparation of this handoff

No local repository build/test command was executed while generating these documentation files.

Observed GitHub CI evidence for snapshot HEAD:

- root `CI`: success
- `Server CI`: success

No live Ubuntu, Windows infrastructure E2E, Android device install, or production deployment was performed by this handoff preparation.

### Future entry template

For every meaningful AI task, append:

- date/time or date,
- task/goal,
- baseline HEAD,
- files changed,
- behavioral effect,
- version metadata changed/not changed,
- tests actually run + result,
- tests not run,
- compatibility impact,
- unresolved risks/blockers,
- next suggested action.

---

## 2026-09-11 — Server 1.1.13 release-candidate finalization (local)

### Goal

Finalize Nyxveil Server **1.1.13** candidate after Codex lifecycle work: confirm tests that can run on Windows, rebuild/package/verify release artifacts from the actual HEAD, update AI handoff. Do **not** redo lifecycle logic. Do **not** push/tag/release/LIVE.

### Baseline / final HEAD

- Initial HEAD: `8fc385335a91aa753b879234999f29a2d025abfb`
- Final product HEAD: `8fc385335a91aa753b879234999f29a2d025abfb` (unchanged; no server source edits)
- Handoff commit (this entry): see git log after commit of `AGENTS.md` / `AI_STATE.md` / `AI_CHANGELOG.md` / `PROJECT.md`

### Files changed (this continuation)

- `AI_STATE.md` — refreshed for 1.1.13 package finalization facts
- `AI_CHANGELOG.md` — this entry
- `AGENTS.md`, `PROJECT.md` — first commit of coordination baseline already present as untracked from prior audit prep (no product logic)

### Behavior / version

- Server remains **1.1.13** (no bump).
- Frozen Core **1.0.0** / NVP/1 unchanged; SHA256 `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b` PASS via `assert-frozen-core.sh`.
- Update lifecycle / drained validation / stdin gate code **not** rewritten.

### Tests actually run

| Check | Result | Notes |
|---|---|---|
| gofmt (Git index LF blobs) | PASS | Working-tree `gofmt -l` false-fails on Windows CRLF |
| `go vet ./...` | PASS | |
| `go test -timeout 180s ./...` | PASS | Windows host |
| `go test ./internal/health` | PASS | lifecycle fixtures |
| `go test ./cmd/nyxveilctl -run Lifecycle\|Handoff\|Update\|Drain\|Version` | PASS | |
| `go test ./internal/runtime -run Update\|Undrain\|Terminal\|Lifecycle\|Drain` | PASS | |
| `go test ./internal/releasecontract` | PASS | expects 1.1.13 |
| `scripts/test-update-lifecycle-gate.sh` | SKIP (partial) | stdin init OK; Python scenario block needs real `python3` (WindowsApps stub) |
| stdin `production-gate` `bash -s` init | PASS | no `BASH_SOURCE` unbound |
| `test-install`, curl-installer, post-reg, version-resolution, pinned-installer, remote gate contracts, firewall-txn, installed-modes, release-origin, clean-host-contract, acme-bind static/mock, nft idempotency skip | PASS / SKIP as labeled | |
| `build-release.sh` amd64+arm64 | PASS | local Go **1.27.0** cross-compile |
| `package-release.sh` + `verify-release.sh` + `verify-artifact-set.sh` + `assert-release-bytes-identity.sh` | PASS | version **1.1.13** |

### Tests not run

- GitHub Server CI (ubuntu-24.04, Go 1.24) — not pushed
- `go test -race` — not re-run in this continuation (prior Codex/CI coverage claimed; not re-executed here)
- Root Linux ACME privileged bind / nftables live apply as root
- systemd / TUN / real CP / LIVE drain→update→undrain E2E
- Publish / tag / production deploy

### Compatibility

- No NVP/1 or Frozen Core change.
- Candidate still requires disposable Ubuntu LIVE gate before any production claim.
- Local package bytes ≠ CI publish bytes (toolchain Go 1.27 local vs CI 1.24).

### Risks / blockers

- None blocking **GitHub Server CI** submission.
- Windows Store `python3` stub prevents full shell lifecycle scenario matrix locally.
- `main` may still be unprotected — do not push without explicit user request.

### Next suggested action

User-authorized push/PR → wait for green Server CI → disposable Ubuntu 24.04 drained remote-update LIVE gate → only then consider release authorization.

---

## 2026-09-11 — Push Server 1.1.13 candidate + confirm Server CI GREEN

### Goal

Authorized `git push origin main` of the 1.1.13 candidate tip; confirm authoritative GitHub Server CI on Linux; no tag/release/LIVE.

### Baseline / pushed

- Initial HEAD: `812732acfccf8f398afbbc2eaa7b888c08323cc8`
- Product SHA: `8fc385335a91aa753b879234999f29a2d025abfb`
- Pushed range: `8fc3853..812732a` → `origin/main`
- Force push: no

### Files changed (this entry)

- `AI_STATE.md` — record push + Server CI green facts
- `AI_CHANGELOG.md` — this entry
- (prior tip commit already contained handoff docs)

### Server / Core

- Server **1.1.13** unchanged
- Core **1.0.0** / NVP/1 unchanged
- Frozen Core SHA256 `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b` — assert OK locally before push

### Server CI

- Run: `34588327249`
- URL: https://github.com/Moroz1212/Nyxveil/actions/runs/34588327249
- SHA: `8fc385335a91aa753b879234999f29a2d025abfb` (product; not the docs-only tip)
- Event: push; conclusion: **success**
- Jobs: `test` PASS; `build` PASS
- Go: **1.24** (workflow `actions/setup-go`)
- Artifact: `nyxveil-server-binaries` id `10194603661` digest `sha256:180c12fa157e0922b0a8ce582132d01c8f9e26385bdbd589b836d67a4589e74b` size 48269882

Docs tip `812732a` did not start Server CI (path filters). Do not claim Server CI for the docs commit.

### CI blockers fixed this task

None (pre-existing product CI already green).

### Not done

- Tag / GitHub Release / production deploy / LIVE update
- Force push
- Touching LIVE node `nv-test-227e939e`

### Next suggested action

Authorized `server-v1.1.13` release from CI artifact bytes, then LIVE `1.1.9`→`1.1.13` drained update gate.

---

## 2026-09-11 — Publish GitHub Release server-v1.1.13

### Goal

Create annotated tag `server-v1.1.13` on product SHA only; let Server Release workflow publish exact Server CI artifact bytes. No LIVE / no production deploy.

### Baseline

- Initial main HEAD: `9abbd35262f1229e73474fe6bdb9d59f4098f6cd`
- Product SHA: `8fc385335a91aa753b879234999f29a2d025abfb`
- Tag created on product SHA (not docs tip); main not moved.

### Provenance

- Server CI: `34588327249` success on `8fc3853`
- Artifact: `10194603661` digest `sha256:180c12fa157e0922b0a8ce582132d01c8f9e26385bdbd589b836d67a4589e74b`
- Tag: annotated `server-v1.1.13` → `8fc3853`
- Server Release: `34614557064` success — https://github.com/Moroz1212/Nyxveil/actions/runs/34614557064
- GitHub Release id `387133550` — https://github.com/Moroz1212/Nyxveil/releases/tag/server-v1.1.13
- draft=false, prerelease=false; assets=18; SHA256SUMS verify of downloaded UPLOAD-LIST = PASS
- Frozen Core assert in release workflow = PASS

### Files changed (handoff)

- `AI_STATE.md` / `AI_CHANGELOG.md` — this release record

### Not done

- LIVE update / production deploy / force push / tag move

### Next suggested action

LIVE `1.1.9` → CP drain → `1.1.13` → terminal success → undrain (authorized disposable host only).

---

## 2026-09-11 — Control Plane 1.3.3 unknown update reconciliation (local)

### Goal

Prepare Control Plane **1.3.3** with SuperAdmin evidence-based reconciliation of unknown
`UpdateNodeLatest` outcomes that permanently blocked location disruptive operations
(LIVE symptom on `nv-test-227e939e` after expired 1.1.9→1.1.12).

### Baseline

- Initial HEAD: `5cf0117f1898fcf3b5a2fdeb5e8c165961a8ad12`
- Final HEAD: `3f9129e56afcdad49ed21a4cc8beedf557a343a9`
- Control Plane before: **1.3.2** / after: **1.3.3**
- Server: **1.1.13** unchanged / published
- Core: **1.0.0** / Protocol **NVP/1** unchanged

### Behavior changed

- Service contract: `GetUnknownUpdateReconciliationPreviewAsync`, `ReconcileUnknownUpdateAsync`
- Eligible: `UpdateNodeLatest` + Failed/Expired + `expired_outcome_unknown` | `outcome_unknown` | `rollback_failed`
- Confirm rollback → Failed / `rolled_back_healthy` when observed == PreviousVersion
- Confirm updated → Succeeded / `updated_healthy` when observed == TargetVersion
- Fresh heartbeat, Active lifecycle, Healthy runtime, sessions=0, identity, admin_state_before required
- Restore via existing `RestoreAdminStateFromPayloadAsync` (preserves prior drain/maintenance)
- Forensic `reconciliation` object in PayloadJson; audit `node.command.update.reconcile`
- UI: Operations + NodeDetails SuperAdmin modal (no generic unlock)
- Location lock clears only because unknown blocking result codes are replaced

### Version metadata

- Control Plane **1.3.2 → 1.3.3** (`VERSION`, gate/deploy scripts, Dashboard/Api contracts, RELEASE-1.3.3.md, CI package required docs)
- DB schema: **unchanged** (still 5); migration: **none**

### Files changed (primary)

- `licensing/src/.../UnknownUpdateReconciliationContracts.cs` (new)
- `licensing/src/.../INodeCommandService.cs`, `NodeCommandService.cs`
- `licensing/.../NodeDetails.razor`, `Operations.razor`, `_Imports.razor`
- `licensing/tests/.../UnknownUpdateReconciliationTests.cs` (new)
- Version/docs/scripts/CI pins for 1.3.3
- `AI_STATE.md`, `AI_CHANGELOG.md`, `PROJECT.md`

### Tests actually run

- `dotnet build` Web + Unit + Integration (Release): PASS
- `dotnet test` UnitTests: **350 passed**, 0 failed, 0 skipped
- `dotnet test` IntegrationTests (LocalDB): **125 passed**, 0 failed, 0 skipped
- `bash server/scripts/assert-frozen-core.sh`: PASS (`7b13097…`)
- `production-gate.ps1 -GateMode local`: RESULT=PARTIAL (schema/DB SKIP without InstallDir — expected)
- `pack-release.ps1` + extracted package required paths including `docs/RELEASE-1.3.3.md`: PASS

### Tests not run / SKIP

- Authoritative GitHub Control Plane CI (no push)
- Production deploy / production-gate production mode
- LIVE reconciliation / LIVE server update

### Compatibility

- Server 1.1.13, Core 1.0.0, NVP/1 unchanged
- Frozen Core paths untouched
- Schema 5 compatible with existing production DB

### Risks / blockers

- Not production-deployed; LIVE node still drained until authorized reconcile after deploy
- Concurrent SuperAdmin reconcile relies on location lock + idempotent payload marker (unit coverage for conflict/idempotent replay)

### Next suggested action

Push/PR → Control Plane CI → deploy rehearsal → backup → deploy CP 1.3.3 → UI reconcile LIVE unknown command as rollback → then separate LIVE 1.1.9→1.1.13 update.


