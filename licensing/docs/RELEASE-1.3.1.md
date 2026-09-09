# Nyxveil Control Plane 1.3.1

## Hotfix

`GET /api/v1/catalog-keys` is **public** again (rate-limited).

Class-level `[LicenseAuth]` on `CatalogController` incorrectly required a client license/access ticket for catalog-keys. Server 1.1.x (including 1.1.10) calls this endpoint without NodeAuth (`sign=false`) when verifying staged SPKI catalogs after ACME — resulting in HTTP 401 and health-gate failure on fresh nodes.

### Change

- Remove class-level `[LicenseAuth]` from `CatalogController`
- Apply `[LicenseAuth]` only to `catalog`, `locations`, `nodes`
- Keep `[RateLimit]` on `catalog-keys`
- No schema, signing crypto, NodeAuth, or Server binary changes

### Deployment wrapper (same 1.3.1 package)

`production-deploy.ps1` / `production-gate.ps1` now rehearse and validate **schema v4**:

- Uses `validate_schema_v4.sql` (accepts schema >= 4)
- Does **not** default to obsolete migration `002`
- If production is already schema 4: restore disposable DB → detect → **no migration** → validate v4 → PASS
- If production is schema 3: apply `004` only, then validate v4
- Older schemas use the additive chain `002→003→004` as needed
- Production DB is never mutated before rehearsal PASS

## Companion

Server remains **1.1.10** (unchanged).
