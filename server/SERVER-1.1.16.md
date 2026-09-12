# Server 1.1.16: privileged ACME state migration on update

Patch over immutable **1.1.15**.

## LIVE root cause

After 1.1.14 → 1.1.15, `/var/lib/nyxveil/acme` remained `root:root` `0700`.
`nyxveil-server` (`User=nyxveil`) logged `operation not permitted` on chmod and
could not open `acme-account.key.filemeta.tmp`. Working leaf cert was preserved.

1.1.15 diagnostics were correct; repair ran in the wrong privilege context
(runtime best-effort enforce after handoff was often skipped).

## Fix

- `filemeta.MigrateACMEState` — fail-closed, symlink-safe, pointed ownership
- Called from **privileged** `nyxveilctl update` / `update-resume` **before**
  health/restart of the non-root daemon
- Clean install creates `${STATE_DIR}/acme` as `nyxveil:nyxveil` `0700`
- Runtime uses `ValidateRuntimeACME` (writability check only; no root-only chmod)

## Version

- Server `VERSION` = **1.1.16**
- Does not retag `server-v1.1.14` or `server-v1.1.15`
- Frozen Core unchanged
