# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-13 after **CRITICAL LIVE DEFECT** on Control Plane production-deploy  
> (1.3.8→1.3.10 elevated deploy failed; updater file lock). Prior “CP DEVELOPMENT COMPLETE”  
> for the production-deploy path is **revoked**. Schema **5**. Frozen Core unchanged.  
> Live HEAD: see git (product work in progress toward `control-plane-v1.3.12`).  
> Preserved dirty: `licensing/tests/CoreInterop/verify-signed/go.mod`.

## Releases (immutable — do not retag)

| Component | Version | Tag | Notes |
|---|---|---|---|
| Control Plane | **1.3.12** (in progress) | `control-plane-v1.3.12` | production-deploy stops updater before InstallDir mutation; repair deploy |
| Control Plane | 1.3.11 | `control-plane-v1.3.11` | Prior published; self-update purity patch — do not retag |
| Control Plane | 1.3.10 | `control-plane-v1.3.10` | Self-update apply / locked-updater — do not retag |
| Control Plane | 1.3.9 | `control-plane-v1.3.9` | Prior; do not retag |
| Server | **1.1.18** | `server-v1.1.18` | ACME directory / cert path productized; do not retag |

- CP 1.3.10 ZIP SHA256: `099507D8E9D76F273B314A70BEFB962B5152016FD42D3C77858249D159EF4AD3`
- CP 1.3.11 ZIP SHA256: `BBB2EC621480133A435E588667E6E4DBD7ADF0A6E2555369C917BF198464A309`
- CP 1.3.12 ZIP SHA256: *(fill after publish)*
- Server 1.1.18 `nyxveil-server-linux-amd64` SHA256: `18b05cbb9b0f2ffced73a78271bcd5ec5976e0c2877bca5f8f29c1a4cfa561a9`

## LIVE defect (accepted root cause)

Elevated `production-deploy.ps1` **1.3.8 → 1.3.10** (package SHA `099507D8…`):

- Precheck / DB backup / RESTORE VERIFYONLY / schema rehearsal / schema v5: **PASS**
- Failed at **`deploy_binaries`**: `Clear-DirectoryContents` on InstallDir while **`NyxveilControlPlaneUpdater` still Running** (holds `.NET` DLLs; Access denied `System.Diagnostics.EventLog.dll`)
- Rollback: `binaries=false`, `rollback_complete=false`; VERSION still **1.3.8**; Web failed to start
- **No manual repair** performed

## Required recovery (after 1.3.12 publish)

One elevated `production-deploy.ps1` from **1.3.12** package **directly to 1.3.12** (not via 1.3.10/1.3.11). Must repair partial InstallDir without manual `sc`/file copy.

## Gate policy

- **FAST DEVELOPMENT GATES**: Control Plane CI / Server CI / root CI. Default loop.
- **FULL RELEASE E2E**: `workflow_dispatch` / `workflow_call` only — not restarted for this CP-only fix.
- LIVE acceptance: **PENDING** (user).

## DEVELOPMENT COMPLETE

**NO** for Control Plane production-deploy path until 1.3.12 tests + CI + release PASS and status lines report accordingly.

## Frozen Core

`7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`

## User LIVE steps (after 1.3.12 release)

1. Elevated `production-deploy.ps1` from extracted **1.3.12** → InstallDir (repair).  
2. Confirm VERSION=1.3.12; both services Running; health/live + health/ready PASS.  
3. Future CP updates may again use the Update button.
