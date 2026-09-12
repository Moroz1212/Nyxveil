# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-13 after **control-plane-v1.3.12** publish (production-deploy updater lock fix).  
> Schema **5**. Frozen Core unchanged.  
> Product HEAD: `468ab73e3363499decb32739524189ce20e1bd6c` on `real-operator-e2e-gates`.  
> Preserved dirty: `licensing/tests/CoreInterop/verify-signed/go.mod`.

## Releases (immutable — do not retag)

| Component | Version | Tag | Notes |
|---|---|---|---|
| Control Plane | **1.3.12** | `control-plane-v1.3.12` | production-deploy stops updater before InstallDir mutation; repair deploy |
| Control Plane | 1.3.11 | `control-plane-v1.3.11` | Prior; do not retag |
| Control Plane | 1.3.10 | `control-plane-v1.3.10` | Prior; do not retag |
| Server | **1.1.18** | `server-v1.1.18` | Do not retag |

- CP 1.3.10 ZIP SHA256: `099507D8E9D76F273B314A70BEFB962B5152016FD42D3C77858249D159EF4AD3`
- CP 1.3.11 ZIP SHA256: `BBB2EC621480133A435E588667E6E4DBD7ADF0A6E2555369C917BF198464A309`
- CP 1.3.12 ZIP SHA256: `F0B6BC01BFB011640605CF9B537542968E86FB1A64AD7392E6E7ED4A3D0D9AD5`
- Release: https://github.com/Moroz1212/Nyxveil/releases/tag/control-plane-v1.3.12
- CI: https://github.com/Moroz1212/Nyxveil/actions/runs/34714894628 (PASS)

## LIVE root cause (fixed in 1.3.12)

`production-deploy` stopped only Web, then cleared InstallDir while **NyxveilControlPlaneUpdater** was Running and held `.NET` DLLs. Rollback cleared/restored without stopping updater → `rollback_complete=false`.

## LIVE recovery (user)

Elevated `production-deploy.ps1` from **1.3.12** package **directly to 1.3.12**. No manual `sc`/file repair. Skip 1.3.10/1.3.11.

## DEVELOPMENT COMPLETE

**YES** for Control Plane production-deploy 1.3.12 fix (tests + CI + release).  
**LIVE USER ACCEPTANCE = PENDING**.

## Frozen Core

`7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`
