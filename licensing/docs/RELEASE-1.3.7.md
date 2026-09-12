# Nyxveil Control Plane 1.3.7

Patch over **1.3.6** (includes all 1.3.6 fixes: UTF-8/Roslyn codepage, AttentionCopy, Server 1.1.15 diagnostics compatibility).

## Root cause (LIVE 1.3.5 → 1.3.6 self-update)

Web service identity (`NT SERVICE\NyxveilControlPlane`) has **RX** on Program Files.
Self-update launched `Process.Start` under that identity → Access denied on install files.
`self-update-apply.ps1` wrote `rollback_failed` without restoring; `transaction.json` stayed InProgress while the service remained Running (startup reconcile never ran).

## Fixes

- Privileged Windows service `NyxveilControlPlaneUpdater` (LocalSystem) polls `request.json` / applies canonical handoff
- Web only writes handoff+request under ProgramData — never Process.Start of the updater
- True rollback with `primaryFailure` / `rollbackAttempted` / `rollbackSucceeded`
- `GetStatus` / startup reconcile ingest `result.json` (unstick ReadyForHandoff)
- `update-windows.ps1` config restore no longer nests `config\config`
- MFA step-up stays inside Update preflight modal
- Production package omits `appsettings.Development.json` from publish when present

## Bootstrap

LIVE 1.3.5 cannot install the privileged updater via broken self-update.
First transition **1.3.5 → 1.3.7** uses elevated `production-deploy.ps1` from the signed 1.3.7 package.

## Version

- Control Plane `VERSION` = **1.3.7**
- Schema remains **5**
- Tag: `control-plane-v1.3.7`
