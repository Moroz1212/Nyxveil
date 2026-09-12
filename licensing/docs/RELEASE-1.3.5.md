# Nyxveil Control Plane 1.3.5

Feature release: safe Control Plane self-update and Fleet Overview.

## Highlights

- **Control Plane self-update**: GitHub Releases discovery (`control-plane-vX.Y.Z`), SHA256 verification, durable ProgramData transaction, external `Nyxveil.ControlPlane.Updater` handoff, health gate, automatic rollback, SuperAdmin + MFA + step-up
- **Fleet Overview** (`/admin/fleet`): locations, nodes, health, sessions, capacity, versions, certificates, operations, filters, topology, SignalR + poll fallback; Deleted nodes excluded
- MFA enrollment: local QR code + regenerate secret before activation
- Schema remains **5** (self-update state in ProgramData JSON)

## Version pins

- Control Plane `VERSION` = **1.3.5**
- Schema = **5** (unchanged; no new EF migration)
- Server / Core / NVP = unchanged

## Package

- Artifact: `Nyxveil-ControlPlane-v1.3.5-release.zip`
- Tag convention: `control-plane-v1.3.5`
- Includes: Web publish, `updater/`, `publish/scripts/self-update-apply.ps1`, deploy scripts, schema validation, `release-manifest.json`

## First install of self-update capability

`1.3.4` cannot self-update to `1.3.5` using code that only exists in `1.3.5`.

Initial transition **1.3.4 → 1.3.5** still uses existing `production-deploy.ps1` / `update-windows.ps1`.

After **1.3.5** is installed, future versions can use the UI self-update path.

## Not in this release

- Fleet canary 25/50/100%
- Automatic live production deploy from this development host
