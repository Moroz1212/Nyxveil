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

Schema version is **2**. Apply `database/migrations/002_node_lifecycle_cert_metadata.sql`, or the EF migration `NodeLifecycleAndCertMetadata`, after taking a verified database backup.

Example update argument:

```powershell
.\scripts\update-windows.ps1 -MigrationScript .\database\migrations\002_node_lifecycle_cert_metadata.sql -ExpectedSchemaVersion 2
```

## Production gate

Run locally without production dependencies:

```powershell
.\scripts\production-gate.ps1
```

For read-only checks of an installed instance:

```powershell
$env:GATE_MODE='production'
.\scripts\production-gate.ps1 -InstallDir 'C:\Program Files\Nyxveil\ControlPlane'
```

The gate never rotates certificates and does not mutate an installed instance.

## Rollback

Before migration, binaries may be rolled back normally. After schema migration, restore the pre-update SQL backup together with the matching 1.0.x binaries. Preserve signing-key and license-KEK backups.
