# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-12 — production hardening **CP 1.3.9** + **Server 1.1.17** in progress on branch `production-hardening-1.3.9-1.1.17`.  
> Schema **5**. Frozen Core unchanged.  
> Preserved dirty: `licensing/tests/CoreInterop/verify-signed/go.mod`.

## Repository

| Fact | Value |
|---|---|
| Initial HEAD | `773f91275472cfe17d8cd64714f84ad853065e1b` |
| Branch | `production-hardening-1.3.9-1.1.17` |
| Production CP base | **1.3.8** (LIVE 1.3.6→1.3.8 confirmed by user) |
| Target CP | **1.3.9** |
| Target Server | **1.1.17** (cumulative from 1.1.15/1.1.16) |

## Root causes (this hardening)

1. **TTL / lease:** Update drain wait left command `Pending` under `DeliveryTtl` (15m). LIVE expired at ~15.5m with `expired` / `command TTL exceeded` while drain/update was in flight. Fix: execution deadline + progress lease refresh on ClaimNext drain-wait and `/progress`.
2. **Late terminal result:** `CompleteAsync` only reconciled `expired_outcome_unknown` / `outcome_unknown`. Generic `expired` rejected late `updated_healthy`. Fix: broaden resolvable codes + late_result note.
3. **Server progress:** Node now reports phase progress to refresh CP lease across restart/update.

## Automated status (session)

- CP Unit: **480 PASS** (includes NodeCommandLeaseTests)
- Browser E2E: **1 PASS** (login+MFA+three operator buttons; cert enqueue proven)
- Server packages controlplane/runtime/filemeta/nyxveilctl: **PASS** locally
- FULL_OPERATOR release-mode (1.3.8→1.3.9 real SCM): requires elevated disposable Windows host
- LIVE three production clicks: **PENDING**

## Frozen Core

`7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`
