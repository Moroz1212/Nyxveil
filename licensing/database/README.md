# Nyxveil Control Plane database

Production target: Microsoft SQL Server on Windows. `create_database.sql` is the
fresh-install bootstrap. Its generated section exactly matches EF InitialCreate
`20260904155703_InitialCreate`, including types, lengths, nullability, defaults,
PK/FK/check constraints, unique keys and indexes. The ConfigVersion bigint is an
optimistic concurrency token; no SQL rowversion column is required.

The script creates a missing database, then runs the EF idempotent baseline and
records the matching migration marker. Re-running after success is safe. It must
not be used to stamp an earlier or partially created schema as current. No live
production database existed when this InitialCreate was finalized.

Use the installer for a custom database name. It validates and binds the chosen
name into a temporary SQL copy; an in-script `:setvar` otherwise takes precedence
over sqlcmd `-v`. For a manual install edit `:setvar DatabaseName` near the top,
then use sqlcmd (or enable SQLCMD Mode in SSMS):

```powershell
sqlcmd -S localhost -E -b -I -i .\database\create_database.sql
```

Use an appropriately privileged installation identity to create the database and
schema. Runtime SQL permissions are granted to the service identity by the
installer. Remote SQL uses certificate validation by default; store SQL passwords
through the DPAPI deployment flow, not in scripts or connection string files.
All datetime2 values represent UTC. `seed_dev.sql` is development-only.

The bootstrap explicitly sets the seven session options required by filtered
indexes after selecting the database, including QUOTED_IDENTIFIER ON. The shared
sqlcmd helper also passes uppercase `-I` for both file and query execution.
These are connection-local settings; no server or database defaults are changed.
After a failed first bootstrap, preserve the database and the DPAPI secrets.
Use `scripts/check-bootstrap-retry.sql` against the chosen database to check for
the expected empty state (at most an empty dbo.__EFMigrationsHistory table) before
retrying Fresh. If it reports a partial schema, inspect it instead of dropping
tables or adding a migration marker manually.

The new NodeRequestNonces composite PK `(NodeId, NonceHash)` prevents concurrent
replay across service processes. ExpiresAt is indexed for retention cleanup.

Local verification: `dotnet test -c Release` compares the complete bootstrap
generated section to EF's generated idempotent DDL and checks the model snapshot.
`scripts/compare-schema.ps1` additionally checks table coverage and migration ID.
Live DDL: run `scripts/test-database.ps1` on the isolated deployment VM. Local
SQLite transaction tests do not count as live SQL Server verification.
