# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-12 after Control Plane **1.3.6** + Server **1.1.15** hotfix preparation.  
> Schema remains **5**. Frozen Core unchanged.  
> LIVE UI/cert validation: **BLOCKED** (no production access on this host).  
> Preserved dirty: `licensing/tests/CoreInterop/verify-signed/go.mod`.

## How to use this file safely

This is a snapshot, not a desired-state manifest. Never reset the repo to match it.

## Current audited component state

| Component | Source version | Audited state |
|---|---:|---|
| Protocol | `NVP/1` | Frozen |
| Core | `1.0.0` | Frozen (hash verified) |
| Server node | `1.1.15` | RenewCertificate diagnostics + ACME ownership; release pending CI |
| Control Plane | `1.3.6` | Encoding + Attention policy; package built locally |
| Windows client | `1.1.2` | Unchanged |
| Android client | `1.0.0` | Unchanged |

## Frozen Core

- SHA256: `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`
- `assert-frozen-core.sh`: OK

## Hotfix root causes

### Encoding

Release compile on CP1251 Windows hosts could mis-decode UTF-8-without-BOM C# sources into Infrastructure.dll string literals (mojibake). Fixed via `licensing/Directory.Build.props` `CodePage=65001` + Unicode-escape `AttentionCopy`.

### Certificate renewal

Server `safeRenewalError` discarded useful ACME detail; explicit RenewCertificate did not log the error; ACME dir ownership not enforced after upgrades. Fixed in Server **1.1.15**.

## Local gates (this session)

- CP Unit: **466** PASS
- CP Integration: **130** PASS
- Server packages tested: runtime/filemeta/releasecontract PASS
- Package: `Nyxveil-ControlPlane-v1.3.6-release.zip`
- SHA256: `37228DB06A571989C4D98F06B6D783E82EA569071C5B111B67B7E50B9DF5AD69`
- LIVE: BLOCKED

## Advisory next action

1. CI green on `control-plane-1.3.6`
2. Publish `control-plane-v1.3.6` + `server-v1.1.15`
3. Authorized host: deploy CP 1.3.6 then Server 1.1.15 and renew LIVE
