# Nyxveil Windows Client 1.0.0

**Setup (one EXE for users):** `dist/Nyxveil-Setup-v1.0.0.exe`

**Source artifact:** `dist/Nyxveil-Windows-Client-v1.0.0-FINAL.zip`

- Authenticode: **NOT SIGNED** (SmartScreen warning expected)
- Live Windows→Ubuntu Internet: **NOT VERIFIED** until infrastructure E2E

## Build

```powershell
cd D:\Nyxveil\clients\windows
powershell -ExecutionPolicy Bypass -File .\scripts\package-final.ps1
```

## Elevated OS gate (Administrator)

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\final-windows-gate.ps1
```

## Layout

- Install: `C:\Program Files\Nyxveil\Client\`
- ProgramData: `C:\ProgramData\Nyxveil\Client\`
- Service: `NyxveilClientService` (LocalSystem)
- Pipe: `\\.\pipe\NyxveilClient` (SYSTEM + Admins + provisioned user SID)

## Frozen Core

SHA256 `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b` — vendored under `third_party/nvp`.
