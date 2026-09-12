# Nyxveil Control Plane 1.3.10

Production hardening over **1.3.9** for artifact-pure self-update apply.

## Fixes

- Fixes privileged `self-update-apply.ps1` health probe parameter names
  (`PublicHostname` / `TimeoutSec`) so post-apply HTTPS checks match Deploy.psm1.
- Skips locked `updater\*` binaries while copying the Web payload so the running
  LocalSystem updater service does not abort apply with a sharing violation.
- Propagates the same locked-updater skip into the built-in updater apply path.
- Honors operational `CertificateMode` during post-apply health verification
  (self-signed lab/production installs no longer force SystemTrust incorrectly).

## Bootstrap note (immutable 1.3.8 / 1.3.9)

Published **1.3.8** and **1.3.9** ships still contain the broken apply script in
`InstallDir\scripts`. The updater always executes the **installed** apply script,
not the target package's copy, until after a successful apply. Therefore a
byte-exact **1.3.8 → 1.3.10** button update cannot load these fixes without
changing files already on disk.

Hosts still on **1.3.8** / **1.3.9** require a one-time elevated
`production-deploy.ps1` (or equivalent) bootstrap onto **1.3.10**. After that,
button self-update uses the fixed apply path.

## Preserved behavior

- Durable self-update handoff, result ingestion, rollback, and schema **5**.
- Frozen NVP/1 Core unchanged.

## Version and update path

- Control Plane `VERSION` = **1.3.10**
- Schema = **5** (no new migration)
- Tag: `control-plane-v1.3.10`
- Preferred production path after bootstrap: **1.3.10 → later patch by button**
