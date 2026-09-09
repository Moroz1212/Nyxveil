# Nyxveil Control Plane 1.3.1

## Hotfix

`GET /api/v1/catalog-keys` is **public** again (rate-limited).

Class-level `[LicenseAuth]` on `CatalogController` incorrectly required a client license/access ticket for catalog-keys. Server 1.1.x (including 1.1.10) calls this endpoint without NodeAuth (`sign=false`) when verifying staged SPKI catalogs after ACME — resulting in HTTP 401 and health-gate failure on fresh nodes.

### Change

- Remove class-level `[LicenseAuth]` from `CatalogController`
- Apply `[LicenseAuth]` only to `catalog`, `locations`, `nodes`
- Keep `[RateLimit]` on `catalog-keys`
- No schema, signing crypto, NodeAuth, or Server binary changes

## Companion

Server remains **1.1.10** (unchanged).
