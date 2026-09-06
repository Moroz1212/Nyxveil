# Nyxveil Control Plane 1.1.0

Release candidate 1.1.0 adds operational node lifecycle controls and certificate/runtime visibility without changing the PoP, catalog-signing, catalog-key, or local port 8443 architecture.

## Highlights

- Soft-delete and revoke states prevent node resurrection through registration or heartbeat.
- Decommission safety blocks nodes with active sessions unless an operator explicitly forces the action.
- Catalogs include only active, enabled nodes.
- Heartbeats advertise non-secret TLS certificate metadata and runtime readiness.
- Dashboard and node administration expose stale heartbeats, certificate expiry, lifecycle, and signing-key status.
- Location deletion is blocked while any non-deleted node depends on it.

## Database

Schema version is **2**. The production deployment wrapper applies `database/migrations/002_node_lifecycle_cert_metadata.sql` when it is present, after taking and verifying a database backup.

## Production deployment

From the licensing root, operators use this single command:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\production-deploy.ps1 -PublishDir .\publish
```

The wrapper owns prechecks, verified database and binary backups, deployment, migration, service restart, the production gate, and rollback. Do not chain the legacy update script and production gate as separate operator steps.

## Rollback

Before migration, the wrapper restores the previous binaries and configuration. If migration was attempted, it restores the pre-deployment SQL backup (via `restore-db.ps1 -Force -ConfirmDatabaseName <db>`) before restoring binaries.
