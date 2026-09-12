# Server 1.1.15: RenewCertificate diagnostics and ACME ownership

Patch release over immutable **1.1.14**.

## Root cause addressed

Explicit `RenewCertificate` reported `renew_failed` with
`ACME renewal failed; details are available in local logs` while discarding the
underlying ACME error and often never logging it. Legacy root-owned
`/var/lib/nyxveil/acme/` after upgrades also caused silent permission failures.

## Fixes

- `classifyRenewalFailure` returns stable ResultCodes (`renew_permission_denied`,
  `renew_acme_challenge_failed`, `renew_validation_failed`, `renew_reload_failed`,
  `renew_certbot_failed`, …) plus a sanitized useful ResultMessage
- Explicit renew path always logs the full ACME error locally
- `EnforceRuntimeTLS` also prepares `/var/lib/nyxveil/acme` ownership/mode
- Forced renew uses an 8-minute timeout (aligned with startup ACME)
- Live TLS leaf remains untouched until staged cert validates and commits

## Version pins

- Server `VERSION` = **1.1.15**
- Frozen Core / NVP unchanged
- Does **not** retag or replace `server-v1.1.14`

## Not in this release

- Protocol/Core changes
- Canary rollout
