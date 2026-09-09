# Nyxveil Control Plane 1.3.2

## Focus

Production-ready Control Plane release: schema 5, remote node management safety,
Control Plane certificate wizard durable states, and hardened Windows deploy.

Companion Server: **1.1.11**

## Database

- **Schema version: 5**
- Fresh install (`create_database.sql`): EF baseline through `CertificateOperationStates` + operational `NyxveilSchemaVersion=5`
- Existing schema **4 → 5**: apply only `database/migrations/005_certificate_operation_states.sql`
- Existing schema **5**: migration is a no-op (idempotent); `validate_schema_v5.sql` PASS
- Schema **>5**: `production-deploy.ps1` refuses (fail closed / no downgrade)
- Dual markers stay aligned: `NyxveilSchemaVersion` and `__EFMigrationsHistory` entry `20260909160000_CertificateOperationStates`

## Remote node management

- `UpdateNodeLatest` pins an immutable `TargetVersion` from `ServerReleaseService` (stable `server-vX.Y.Z` only; drafts/prereleases ignored)
- GitHub source fixed to **Moroz1212/Nyxveil** (no production owner/repo override)
- Location safety: one Location may contain many Nodes — never take down the last healthy Node; block concurrent disruptive ops in the same Location
- Claim-time recheck of location safety
- Update drain waits for post-drain heartbeat; durable command history on `/admin/operations`
- Success restores prior admin state only on proven-safe outcomes; unknown / rollback_failed leave Node drained
- `RenewCertificate` / `RestartNyxveilService` / `RebootHost` require advertised capabilities (`certificate_renew`, `service_restart`, `host_reboot`)

## Control Plane certificate wizard

- Durable ACME DNS-01 flow: Start → DNS verify → Issue → Switching → HTTPS verify → Completed
- Statuses **0..9** including **Switching** and **Expired**
- Switching survives CP restart; activation failures leave the previous working TLS in place
- PFX password via stdin (never argv / logs / diagnostic bundle)

## Deployment

- `production-deploy.ps1` ExpectedSchemaVersion=**5**
- Order: backup + VERIFYONLY → disposable rehearsal DB (migrate+validate) → only then stop CP → production migrate → validate v5 → binaries → start → health/version gate
- Rehearsal SQL never targets the live production database name
- Rollback restores binaries/config and DB only when production migration was attempted; post-rollback health of the previous version is required

## Installer / registration

- Existing-node registration requires **fresh bootstrap + PoP** (atomic consume with advertisement update)

## Operations UI

- `/admin/operations` history: previous version, target version, status, progress phase, result code/message
- Node details: version, available version, Update / Renew Certificate / Restart / Reboot gated by capability + safety
