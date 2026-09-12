# Server 1.1.18

Production release for ACME directory configuration and lab/test CA E2E support
on top of durable update progress from **1.1.17**.

## Changes

- Reads `acme_directory` from local node config for non-default ACME endpoints
  (Let's Encrypt staging / Pebble / other test CAs).
- Supports `NYXVEIL_ACME_INSECURE_DIRECTORY_TLS=1` for lab directories whose ACME
  directory presents a non-system-trusted TLS certificate (Pebble).
- Preserves legacy `/var/lib/nyxveil/acme` ownership migration to `nyxveil:nyxveil`
  `0700` during privileged update/startup paths.
- Retains durable update progress / lease behavior introduced for **1.1.17**.

## Version and update path

- Server `VERSION` = **1.1.18**
- Tag: `server-v1.1.18`
- Production button path from published **1.1.15**: **1.1.15 → 1.1.18**
- Companion Control Plane: **1.3.10** (or later) recommended for operator E2E
