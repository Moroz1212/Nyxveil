# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-12 after Control Plane **1.3.5** product gate (local).  
> Schema remains **5**. Server/Core/NVP unchanged.  
> Live production deploy: **not executed** (self-update first install of 1.3.5 still requires existing deploy path).  
> Preserved dirty: `licensing/tests/CoreInterop/verify-signed/go.mod` (not part of this release).

## How to use this file safely

This is a snapshot, not a desired-state manifest. Never reset the repo to match it.

## Current audited component state

| Component | Source version | Audited state |
|---|---:|---|
| Protocol | `NVP/1` | Frozen |
| Core | `1.0.0` | Frozen |
| Server node | `1.1.14` | Unchanged this stage |
| Control Plane | `1.3.5` | Self-update + Fleet local gate PASS; release pending/recorded below |
| Windows client | `1.1.2` | Unchanged |
| Android client | `1.0.0` | Unchanged |

## Frozen Core

- SHA256: `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`

## Control Plane 1.3.5 (this session)

### Product

- **Self-update**: GitHub `control-plane-v*` discovery, SHA256 sidecar verify, ProgramData durable transaction, external `Nyxveil.ControlPlane.Updater` + `self-update-apply.ps1`, SuperAdmin+MFA+step-up (`CriticalOperation.ControlPlaneSelfUpdate`), no UI downgrade, fail-closed ambiguous restart reconciliation
- **Fleet**: `/admin/fleet` overview (locations/nodes/health/sessions/capacity/versions/certs/ops/filters/topology), Deleted excluded, SignalR + ~15s poll
- **MFA UX**: local QRCoder QR + regenerate secret before activation
- Schema: **5** (self-update state outside SQL)

### Tests (fresh local run)

- Unit: **447** PASS
- Integration: **130** PASS
- Build: PASS (0 errors)
- Package: `Nyxveil-ControlPlane-v1.3.5-release.zip`
- Package SHA256: `24C1BB42EB69599B9D8B8B807C27334A99AF6CB5C860578B83718E7988F64F9A`
- production-gate local: PARTIAL (DB/install skips expected)
- Browser E2E (headed Playwright): **NOT RUN / BLOCKED**
- Windows Service LIVE self-update path: **NOT RUN** on this host (no `NyxveilControlPlane` service)

### Deployment note

`1.3.4 → 1.3.5` initial install still uses `production-deploy.ps1` / `update-windows.ps1`.  
UI self-update applies only after 1.3.5 is already installed (for later versions).

## Advisory next action

1. Publish `control-plane-v1.3.5` GitHub Release with validated ZIP + `.sha256`
2. Sync branch into `main` via PR (no force)
3. On authorized CP host: deploy 1.3.5 via existing scripts, then validate self-update UX against a lab 1.3.6 candidate later

This is advisory only.
