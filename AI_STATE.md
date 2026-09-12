# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-12 for Control Plane **1.3.8** hotfix (LIVE 1.3.7 sc.exe 1639).  
> Schema **5**. Frozen Core unchanged. Server **1.1.16** unchanged.  
> LIVE CP 1.3.6→1.3.8: **BLOCKED** until authorized elevated production-deploy.  
> Preserved dirty: `licensing/tests/CoreInterop/verify-signed/go.mod`.

## Session

| Fact | Value |
|---|---|
| Initial HEAD | `3f63029f90c137adbbb3deaaaefb1d9d0981035b` |
| Branch | `control-plane-1.3.8` |

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

After release: elevated `production-deploy.ps1` for **1.3.6 → 1.3.8** (skip broken 1.3.7).
