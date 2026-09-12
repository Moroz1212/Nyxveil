# Nyxveil Control Plane 1.3.8

Hotfix over **1.3.7** after LIVE `production-deploy` failure installing the privileged updater.

## LIVE 1.3.7 failure

- Stage: `install_updater_service`
- Diagnostic: `sc.exe create NyxveilControlPlaneUpdater failed exit=1639` (`ERROR_INVALID_COMMAND_LINE`)
- Automatic rollback restored CP **1.3.6** healthy; no manual repair.

## Root cause

PowerShell 5.1 native argument binding turned:

```text
$binPath = '"C:\Program Files\...\Nyxveil.ControlPlane.Updater.exe" --service'
& sc.exe create ... binPath= $binPath DisplayName= 'Nyxveil Control Plane Updater' ...
```

into CreateProcess command line fragment:

```text
binPath= ""C:\Program Files\...\Nyxveil.ControlPlane.Updater.exe" --service"
```

`sc.exe` re-parses `GetCommandLineW()` and rejects that malformed token (1639).

## Fix

- Build BinaryPathName via `Get-NyxveilServiceBinaryPathName`
- Create/update services with Win32 `CreateService` / `ChangeServiceConfig` (no fragile `sc.exe create` quoting)
- Verify via CIM: Name, PathName (exact, includes `--service`), StartName=LocalSystem, StartMode=Auto, Running
- production-deploy tracks updater SCM object and rolls it back on failure
- Real Windows SCM integration test: `scripts/test-windows-service-create.ps1`

## Preserve

All 1.3.6/1.3.7 fixes (UTF-8, Attention, result.json reconcile, true rollback fields, config nesting, MFA UX, privileged updater architecture).

## Version

- Control Plane `VERSION` = **1.3.8**
- Schema **5**
- Tag: `control-plane-v1.3.8`
- Direct LIVE path: **1.3.6 → 1.3.8** via elevated `production-deploy.ps1`
- `minimum_supported_version` = **1.3.6**
