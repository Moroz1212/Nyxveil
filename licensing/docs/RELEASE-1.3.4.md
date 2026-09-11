# Nyxveil Control Plane 1.3.4

Patch release: security acceptance gate for the operator panel.

## Highlights

- Centralized `ICriticalOperationAuthorizer` with user-bound step-up cookie (5 minute TTL)
- Server-side step-up for: Update Node, Rolling Update start, Reboot, Delete, Reconcile, Signing Key rotate, sensitive Settings, Admin security mutations
- Location rollout continuation does not re-demand MFA per node after authorized start
- MFA reset requires fresh step-up; logout clears step-up
- Expanded unit + operator HTTP smoke coverage

## Version pins

- Control Plane `VERSION` = **1.3.4**
- Schema = **5** (unchanged; no new EF migration)
- Server / Core / NVP = unchanged

## Package

- Artifact: `Nyxveil-ControlPlane-v1.3.4-release.zip`
- Tag convention: `control-plane-v1.3.4` (aligned with `server-v*` style)

## Not in this release

- Fleet canary 25/50/100%
