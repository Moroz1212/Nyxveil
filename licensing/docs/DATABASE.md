# Database

## Database name

Default: `NyxveilControlPlane` (configurable in `create_database.sql`).

## Timestamp convention

All `datetime2(7)` values are **UTC**. Application code writes `DateTime.UtcNow` / `IClock.UtcNow`.

## Bootstrap

```powershell
sqlcmd -S localhost -E -i database\create_database.sql
```

Idempotent: safe to re-run; does not drop existing data.

## SQL TLS (`Database` options)

| Setting | Default | Notes |
|---------|---------|--------|
| `Encrypt` | `true` | Always encrypt the SQL connection. |
| `TrustSqlServerCertificate` | unset/`false` | Remote-safe default. Local lab installs may set `true`. |

`IDatabaseConnectionStringProvider` **overrides** `TrustServerCertificate` from `Database:TrustSqlServerCertificate` (null/false → do not trust). A connection-string `TrustServerCertificate=True` alone is not enough for remote; set the option explicitly.

Installer: local SQL defaults trust=true; remote defaults false (`Resolve-TrustSqlServerCertificatePolicy`). Remote + trust=true requires `-TrustSqlServerCertificate` with a warning.

## Core tables

| Table | Purpose |
|-------|---------|
| Users | End-user accounts (optional email) |
| Plans | trial / standard / premium / master, etc. |
| Licenses | Verifier-only keys, role, plan, expiry, max devices |
| LicenseAllowedLocations | Location allow-list per license |
| Devices | Device public keys bound to licenses |
| Locations | Catalog locations |
| Nodes / NodeEndpoints / NodeTransports | Node registry |
| NodeHealth / NodeMetrics | Runtime health + history |
| NodeCredentials / NodeConfigs | Auth material + policy |
| BootstrapTokens | Node enrollment |
| TicketAudits | Issue/refresh audit |
| Revocations | Ticket / license / device revocations (versioned) |
| CatalogVersions | Signed catalog versions |
| SigningKeysMetadata | Public key metadata + protected private material |
| AuditLog | Admin actions |
| SystemSettings | Operational key/value |
| PaymentEvents | Future payment idempotency |
| AspNet* | Admin Identity |

## Indexes (highlights)

- License verifier unique lookup
- License status + expiry
- Devices by LicenseId
- Nodes by LocationId / status
- Metrics by NodeId + Timestamp
- Revocations by Version / TargetId
- Audit by Timestamp
- Bootstrap verifier unique

## Dev seed

`database/seed_dev.sql` — **Development only**. Never run automatically in Production.
