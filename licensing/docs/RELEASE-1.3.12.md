# Nyxveil Control Plane 1.3.12

Emergency production-deploy repair for LIVE hosts damaged by 1.3.8→1.3.10 deploy.

## Root cause fixed

`production-deploy.ps1` previously stopped only `NyxveilControlPlane`, then cleared
`InstallDir` while `NyxveilControlPlaneUpdater` was still Running and holding
`.NET` DLLs (`System.Diagnostics.EventLog.dll` access denied). Rollback repeated
the same lock defect.

## Changes

- Stop **both** Web and Updater before any `InstallDir` mutation
- Assert updater is not Running before `Clear-DirectoryContents`
- Rollback: stop both → restore binaries → restore updater SCM → restore original Running/Stopped states → health
- Repair deploy: allow empty/partial InstallDir; do not require healthy Web at precheck
- Schema remains **5**

## LIVE recovery

From a damaged 1.3.8 install (`rollback_complete=false`, VERSION still 1.3.8):

```powershell
.\scripts\production-deploy.ps1 -PublishDir <extracted-1.3.12\publish>
```

No manual `sc stop` / file deletes required. Deploy **directly to 1.3.12** (skip 1.3.10/1.3.11).

## Version

- Tag: `control-plane-v1.3.12`
- Schema: **5**
