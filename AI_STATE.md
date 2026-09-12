# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-12 during **real-operator-e2e-gates** (honest semantics; gates in progress).  
> Schema **5**. Frozen Core unchanged.  
> Preserved dirty: `licensing/tests/CoreInterop/verify-signed/go.mod`.

## Releases (immutable — do not retag)

| Component | Version | Tag | Product SHA |
|---|---|---|---|
| Control Plane | 1.3.9 | `control-plane-v1.3.9` | `3f3e750d9eeab575e5edaaa40c7e54cffa51b1a4` |
| Server | 1.1.17 | `server-v1.1.17` | `3f3e750d9eeab575e5edaaa40c7e54cffa51b1a4` |

- CP ZIP SHA256: `C206E77B101BB061E1B550D1B7549BC8AACEEFDCD999B3B2B841B83BFE014C93`
- Server candidate source VERSION now **1.1.18** (ACME `acme_directory` + lab delay/TLS opts) — **not released yet**.

## Gate semantics (corrected)

- Contract lab harnesses emit `CONTRACT_OPERATOR_GATES` / `SERVER_CONTRACT_GATES` only.
- `FULL_OPERATOR_E2E` and `AUTOMATED_PRODUCTION_GATES` are owned exclusively by
  `licensing/scripts/aggregate-production-gates.ps1` + `assert-production-gates.ps1`.
- PARTIAL / SKIPPED / NOT_EXECUTED / MISSING / BLOCKED ≠ PASS.

## Current work branch

`real-operator-e2e-gates` — real CP button E2E, Ubuntu systemd node E2E, Pebble/TLS/QUIC pending green CI evidence.

## LIVE operator acceptance

**PENDING** — not offered until `AUTOMATED_PRODUCTION_GATES=PASS`.

## Frozen Core

`7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`
