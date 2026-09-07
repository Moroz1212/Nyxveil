# Nyxveil Control Plane 1.1.2

Catalog signature + stale node advertisement release candidate.

## Fixes

1. **Catalog signature (Frozen Core compatible)**  
   C# `CatalogCanonicalJson` now produces byte-for-byte the same payload as Frozen Core
   `catalog.canonicalPayload` (`encoding/json`):
   - `DateTimeKind.Unspecified` treated as UTC wall time (no host-local conversion) — matches HTTP
     `UtcDateTimeJsonConverter` and fixes Windows non-UTC production hosts
   - Go float64 formatting (`g` / lowercase `e`, negative zero)
   - Go HTML unicode escapes (lowercase `\u003c` / `\u0026`)
   - Non-ASCII left as UTF-8 (not `\uXXXX`)
   - `server_name` / `spki_pin` omitempty for empty values

2. **Stale node advertisement**  
   Existing-node PoP re-registration continues to refresh mutable advertisement
   (`server_version`, `server_name`, `spki_pin`, endpoints) via
   `ApplyExistingNodeAdvertisementAsync` without mutating admin-owned fields.
   Heartbeat remains dynamic-health-only.

## Compatibility

- Schema version: **v2** (unchanged)
- Frozen Core: **1.0.0** / NVP/1 — SHA256 `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`
- HTTPS listen port remains runtime-configurable (default architecture 8443)
- Existing immutable **server-v1.1.6** catalog verifier path is compatible after this CP update

## Not included

- No Frozen Core changes
- No production key rotation
- No GitHub publish in this candidate build
