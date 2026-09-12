# Server 1.1.17: durable update progress reporting

Cumulative patch over immutable **1.1.16**.

## Changes

- Inherits the 1.1.16 privileged `filemeta.MigrateACMEState` repair, including
  cumulative 1.1.15 → 1.1.17 updates.
- Preserves the durable update marker and transaction journal so a final update
  result remains recoverable and retryable across process restart or Control
  Plane connectivity loss.
- Reports `downloading`, `verifying`, `installing`, `restarting`, and
  `post_check` progress to the Control Plane. Phase changes are reported by
  `nyxveilctl`; the daemon periodically refreshes the lease from durable state.
- Progress reporting is best-effort and cannot fail or roll back an otherwise
  valid update.

## Version

- Server `VERSION` = **1.1.17**
- Frozen Core unchanged
