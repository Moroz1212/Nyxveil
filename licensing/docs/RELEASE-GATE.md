# Control Plane release gate (1.3.4)

Release artifact: `Nyxveil-ControlPlane-v1.3.4-release.zip`

## Required pins

- Control Plane `VERSION` = **1.3.4**
- Schema version = **5** (unchanged from 1.3.3)
- Companion Server release: **server-v1.1.14** (not changed by this CP release)

## Local gate

```powershell
cd licensing
.\scripts\production-gate.ps1 -GateMode local
```

## Package contents must include

- `VERSION`
- `publish\Nyxveil.ControlPlane.Web.dll`
- `scripts\production-deploy.ps1`
- `scripts\production-gate.ps1`
- `scripts\Nyxveil.ControlPlane.Deploy.psm1`
- `database\migrations\005_certificate_operation_states.sql`
- `database\migrations\validate_schema_v5.sql`
- `docs\RELEASE-1.3.4.md`

## Schema

Schema remains at 5. No new migration for 1.3.4. Location rollout state remains in
`SystemSettings`; critical-op elevation is cookie + service gate only.

Older release notes remain under `docs/RELEASE-1.*.md` for history only.
