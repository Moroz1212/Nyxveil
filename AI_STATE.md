# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-12 after **control-plane-v1.3.8** release.  
> Schema **5**. Frozen Core unchanged. Server **1.1.16** unchanged.  
> LIVE CP 1.3.6→1.3.8: **BLOCKED** (no production access this session).  
> Preserved dirty: `licensing/tests/CoreInterop/verify-signed/go.mod`.

## Session / release

| Fact | Value |
|---|---|
| Initial HEAD | `3f63029f90c137adbbb3deaaaefb1d9d0981035b` |
| Product / main HEAD | `a6b776b16d5ce2ed8ffd6166f5e475a4f8a8e232` |
| Tag | `control-plane-v1.3.8` |
| Release | https://github.com/Moroz1212/Nyxveil/releases/tag/control-plane-v1.3.8 |
| Control Plane CI | https://github.com/Moroz1212/Nyxveil/actions/runs/34686627398 |
| Unit | **476 PASS** |
| Integration | **130 PASS** |
| Windows SCM test | **WINDOWS_SERVICE_CREATE_TEST=PASS** |
| ZIP SHA256 | `FEF6C6D3F40F3BBA7A721E84ECB54F64DC20569CCDAC1FA93D5397225D70018A` (download-back matched) |
| Schema | **5** |
| Frozen Core SHA256 | `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b` |

## LIVE 1.3.7 failure (authoritative)

- Deploy 1.3.6→1.3.7 failed at `install_updater_service`
- `sc.exe create NyxveilControlPlaneUpdater failed exit=1639`
- Automatic rollback restored 1.3.6 healthy
- No manual production repair

## Root cause

PS 5.1 CreateProcess cmdline for `$binPath='"...Updater.exe" --service'` became:

`binPath= ""C:\Program Files\...\Updater.exe" --service"`

sc.exe re-parses GetCommandLineW → ERROR_INVALID_COMMAND_LINE.

## Fix (1.3.8)

Win32 CreateService/ChangeServiceConfig + CIM verify + updater SCM rollback + real Windows SCM test.

## Advisory next action

Elevated `production-deploy.ps1` for **1.3.6 → 1.3.8** on LIVE (skip broken 1.3.7). No manual sc/ACL/copy.
