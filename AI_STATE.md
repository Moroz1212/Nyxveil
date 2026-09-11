# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-12 after Control Plane **1.3.4** security/acceptance gate (local).  
> Schema remains **5**. Server/Core/NVP unchanged.  
> Deploy of running CP: **BLOCKED** (no `NyxveilControlPlane` Windows service on this host).  
> Preserved dirty: `licensing/tests/CoreInterop/verify-signed/go.mod` (not part of this release).

## How to use this file safely

This is a snapshot, not a desired-state manifest. Never reset the repo to match it.

## Current audited component state

| Component | Source version | Audited state |
|---|---:|---|
| Protocol | `NVP/1` | Frozen |
| Core | `1.0.0` | Frozen |
| Server node | `1.1.14` | CI+Release PASS; LIVE BLOCKED |
| Control Plane | `1.3.4` | Security gate + package PASS; deploy BLOCKED |
| Windows client | `1.1.2` | Unchanged |
| Android client | `1.0.0` | Unchanged |

## Frozen Core

- SHA256: `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`

## Control Plane 1.3.4 (this session)

### Step-up coverage (server-side)

| Operation | MFA (SuperAdmin) | Step-up | Server-side |
|---|---|---|---|
| Delete Node | yes | yes | `NodeManagementService` |
| Reboot Host | yes | yes | `NodeCommandService.Enqueue` |
| Update Node | yes | yes | `NodeCommandService.Enqueue` |
| Rolling Update start | yes | yes | `LocationRolloutService.StartAsync` |
| Reconcile unknown update | yes | yes | `NodeCommandService.Reconcile` |
| Signing key mutate | yes | yes | `Ed25519SigningKeyStore.RotateAsync` |
| Sensitive settings | yes | yes | Settings page + authorizer |
| Admin security create | yes | yes | AdminUsers + authorizer |

User-bound DataProtection cookie `nyxveil_stepup`, TTL 5 minutes. Logout clears cookie.

### Tests (fresh run)

- Unit: **403** PASS
- Integration: **130** PASS
- Build: PASS (0 errors)
- Package: `Nyxveil-ControlPlane-v1.3.4-release.zip` validated
- production-gate local: PARTIAL (DB/install skips expected)
- Operator HTTP smoke: PASS (`OperatorPanelSmokeTests` + expanded admin paths)

### Deployment

- Not executed — no installed `NyxveilControlPlane` service detected on this machine

## Advisory next action

1. Push/tag `control-plane-v1.3.4` + GitHub Release with the validated ZIP  
2. Run `update-windows.ps1` / `production-deploy.ps1` on the authorized CP host  
3. Post-deploy browser smoke (login+MFA, Dashboard, Nodes, Operations)

This is advisory only.
