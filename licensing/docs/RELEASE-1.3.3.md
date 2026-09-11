# Nyxveil Control Plane 1.3.3

## Focus

Safe SuperAdmin reconciliation of remote `UpdateNodeLatest` commands that ended with an
unknown outcome (`expired_outcome_unknown`, `outcome_unknown`, `rollback_failed`) and
were permanently blocking location disruptive operations.

Companion Server: **1.1.13** (unchanged by this Control Plane release)

## Database

- **Schema version: 5** (unchanged)
- No new EF migration
- Reconciliation forensics stored in existing `NodeCommand.PayloadJson` + `AuditLog`
- Fresh install and schema **4 → 5** path identical to 1.3.2

## Unknown update reconciliation

- Application contract: `GetUnknownUpdateReconciliationPreviewAsync` / `ReconcileUnknownUpdateAsync`
- SuperAdmin only (service + UI)
- Eligible only for terminal `UpdateNodeLatest` with unknown outcome codes
- Confirm rollback → `Failed` / `rolled_back_healthy` when observed version == `PreviousVersion`
- Confirm updated → `Succeeded` / `updated_healthy` when observed version == `TargetVersion`
- Requires fresh heartbeat, Active lifecycle, Healthy runtime, sessions=0, identity present,
  and durable `admin_state_before` snapshot
- Does **not** require TLS/QUIC listeners while node remains drained
- Restores prior admin state via existing `RestoreAdminStateFromPayloadAsync`
- Preserves forensic history (original result + reconciliation metadata) in payload
- Audit action: `node.command.update.reconcile` (reason required)
- Location disruption lock clears only because the command no longer carries an unknown
  blocking result code — no generic unlock API

## Operations UI

- `/admin/operations` and node details: SuperAdmin button «Разрешить неопределённый результат»
- Modal shows evidence and only enables the confirm action that matches observed version

## Deployment

- `production-deploy.ps1` / `production-gate.ps1` pin **1.3.3**, ExpectedSchemaVersion=**5**
- Same deploy order as 1.3.2 (backup → rehearsal → stop → migrate no-op → validate → start)
