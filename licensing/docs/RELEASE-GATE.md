# Control Plane release gate (1.3.2)

Release artifact: `Nyxveil-ControlPlane-v1.3.2-release.zip`  
Target: Windows + Microsoft SQL Server production update/install.

## Required proofs

- Control Plane `VERSION` = **1.3.2**
- Companion Server = **1.1.11** (unchanged unless a proven CP-only fix is impossible)
- Frozen Core SHA256 unchanged:
  `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`
- Database schema **5**
  - fresh → v5
  - v4 → `005_certificate_operation_states.sql` → v5
  - v5 → no-op + `validate_schema_v5.sql`
- `production-deploy.ps1` rehearsal before any production mutation
- Unit + integration tests PASS (or SKIP with exact external reason)
- Control Plane GitHub Actions workflow PASS
- Package built, extracted to a clean temp dir, and re-validated from the extract
- No secrets in git / ZIP / diagnostic bundles
- No tracked `TestResults/`, `*.trx`, `*.tmp`

## Local commands

```powershell
dotnet restore
dotnet build .\Nyxveil.ControlPlane.sln -c Release
dotnet test .\tests\Nyxveil.ControlPlane.UnitTests -c Release --no-build
dotnet test .\tests\Nyxveil.ControlPlane.IntegrationTests -c Release --no-build
dotnet publish .\src\Nyxveil.ControlPlane.Web\Nyxveil.ControlPlane.Web.csproj -c Release -o .\artifacts\web
.\scripts\production-gate.ps1 -GateMode local
.\scripts\pack-release.ps1 -PublishDir .\artifacts\web
# Then extract ZIP to a fresh temp directory and re-run production-gate against the extract.
```

## Historical note

Earlier 1.0.0 live-deploy freeze documentation lived here. Current production gate for this tree is **1.3.2 / schema 5**. Older release notes remain under `docs/RELEASE-1.*.md` for history only.
