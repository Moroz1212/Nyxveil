# Nyxveil Control Plane 1.3.9

Production hardening over **1.3.8** for durable node-command execution and
self-update reconciliation.

## Fixes

- Refreshes node-command leases from signed progress reports so long-running
  updates do not expire while a node is actively downloading, verifying,
  installing, restarting, or performing its post-update check.
- Reconciles authenticated late command results instead of losing a valid
  terminal result after the original command lease elapsed.
- Adds the signed node-command progress API used by companion Server
  **1.1.17**.
- Supersedes stale failed or unknown update items in **Attention** after a newer
  update for the same node succeeds; operation history remains available.

## Preserved behavior

- Retains the **1.3.8** Win32 `CreateService` / `ChangeServiceConfig` fix for
  installing and maintaining the privileged updater service.
- Durable self-update handoff, result ingestion, health verification, and
  rollback behavior remain in place.
- Frozen NVP/1 Core and protocol behavior are unchanged.

## Version and update path

- Control Plane `VERSION` = **1.3.9**
- Schema = **5** (no new migration)
- Tag: `control-plane-v1.3.9`
- `minimum_supported_version` = **1.3.8**
- Production button self-update base: **1.3.8**, which already contains the
  privileged updater service bootstrap.
- Direct production path: **1.3.8 → 1.3.9**
