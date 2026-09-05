# Nyxveil Control Plane — Database

SQL Server schema for the Nyxveil Licensing / Control Plane (`NyxveilControlPlane`).

| File | Purpose |
|------|---------|
| `create_database.sql` | Idempotent create: database, tables, constraints, indexes, sanity checks |
| `seed_dev.sql` | **Development only** — sample plans + `fi-helsinki` location |
| `../VERSION` | Control Plane package version (`1.0.0`) |

## Prerequisites

- Microsoft SQL Server 2019+ or Azure SQL Database
- `sqlcmd` (SQL Server command-line tools) **or** SQL Server Management Studio (SSMS)
- Login with permission to `CREATE DATABASE` (first run) and to create objects in the target DB

## Connection

Typical local connection string (ADO.NET / EF Core):

```text
Server=localhost;Database=NyxveilControlPlane;Trusted_Connection=True;TrustServerCertificate=True;Encrypt=True
```

SQL authentication example:

```text
Server=localhost,1433;Database=NyxveilControlPlane;User Id=sa;Password=<secret>;TrustServerCertificate=True;Encrypt=True
```

All `datetime2(7)` columns are **UTC** (documented in `create_database.sql`).

## Run with sqlcmd

From the repository root or this folder:

```powershell
# Windows Authentication (local default instance)
sqlcmd -S localhost -E -i "d:\Nyxveil\licensing\database\create_database.sql"

# Named instance
sqlcmd -S localhost\SQLEXPRESS -E -i "d:\Nyxveil\licensing\database\create_database.sql"

# SQL login
sqlcmd -S localhost -U sa -P "<password>" -i "d:\Nyxveil\licensing\database\create_database.sql"
```

Development seed (after create; **never in production**):

```powershell
sqlcmd -S localhost -E -i "d:\Nyxveil\licensing\database\seed_dev.sql"
```

The script is idempotent: re-running `create_database.sql` does not drop existing tables.

## Run with SSMS

1. Open `create_database.sql` in SSMS.
2. Connect to the target server.
3. Optionally change the database name at the top (`@DatabaseName`) **and** the matching `USE [...]` statement.
4. Execute (F5).
5. Review Messages for sanity-check output (expected tables + row counts).
6. For local/dev only, open and execute `seed_dev.sql`.

## Database name

Default name: **`NyxveilControlPlane`**.

To rename, edit **both** places in `create_database.sql`:

1. `DECLARE @DatabaseName sysname = N'NyxveilControlPlane';`
2. `USE [NyxveilControlPlane];`

Keep `seed_dev.sql` `USE` in sync if you rename.

## Migrations note

`create_database.sql` is the **bootstrap / greenfield** schema. Day-to-day schema evolution should go through **EF Core migrations** in `Nyxveil.ControlPlane.Infrastructure` so the application model and database stay aligned.

Recommended workflow:

1. Apply `create_database.sql` once on a new environment (or use EF to create the DB from migrations).
2. Prefer a single source of truth going forward: **EF Core migrations** for additive changes (new columns, indexes).
3. Do not hand-edit production tables outside migrations except for emergency hotfix with a follow-up migration.
4. If both bootstrap SQL and EF migrations exist, generate an initial migration that matches this schema (`dotnet ef migrations add InitialCreate`) and baseline existing databases so EF history (`__EFMigrationsHistory`) stays consistent.

Identity tables (`AspNetUsers`, `AspNetRoles`, …) match the standard ASP.NET Core Identity EF model.

## Tables (domain)

Users, Plans, Licenses, LicenseAllowedLocations, Devices, Locations, Nodes, NodeEndpoints, NodeTransports, NodeHealth, NodeMetrics, NodeCredentials, NodeConfigs, BootstrapTokens, TicketAudits, Revocations, CatalogVersions, SigningKeysMetadata, AuditLog, SystemSettings, PaymentEvents — plus ASP.NET Identity tables.
