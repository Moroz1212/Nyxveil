# Control Plane release gate (1.3.9)

Release artifact: `Nyxveil-ControlPlane-v1.3.9-release.zip`

## Required pins

- Control Plane `VERSION` = **1.3.9**
- Schema version = **5** (unchanged from 1.3.4)
- Companion Server release: **server-v1.1.17** (command-progress reporting; 1.1.14/1.1.15/1.1.16 immutable)

## Local gate

```powershell
cd licensing
.\scripts\production-gate.ps1 -GateMode local
```

## Package contents must include

- `VERSION`
- `publish\Nyxveil.ControlPlane.Web.dll`
- `publish\updater\Nyxveil.ControlPlane.Updater.dll` (or `.exe`)
- `publish\scripts\self-update-apply.ps1`
- `scripts\production-deploy.ps1`
- `scripts\production-gate.ps1`
- `scripts\Nyxveil.ControlPlane.Deploy.psm1`
- `scripts\self-update-apply.ps1`
- `database\migrations\005_certificate_operation_states.sql`
- `database\migrations\validate_schema_v5.sql`
- `docs\RELEASE-1.3.8.md`
- `docs\RELEASE-1.3.9.md`
- `release-manifest.json`

## Schema

Schema remains at 5. No new migration for 1.3.9. Self-update durable state lives under
`%ProgramData%\Nyxveil\ControlPlane\self-update\`. Fleet is a query projection over existing inventory.

Older release notes remain under `docs/RELEASE-1.*.md` for history only.
