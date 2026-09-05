# Backup and restore

Use SQL Server native backup — **not** raw MDF copies.

## Backup

```powershell
.\scripts\backup-db.ps1 -SqlServer localhost -Database NyxveilControlPlane -BackupPath C:\Backups\NyxveilControlPlane.bak
```

Performs `BACKUP DATABASE ... WITH INIT, COMPRESSION`.

Also back up:

- `appsettings.Production.json`
- Signing key protection directory (`Signing:KeyProtectionPath`)
- Machine-level DataProtection keys if used outside that path

## Restore

Destructive. Requires explicit confirmation:

```powershell
.\scripts\restore-db.ps1 -BackupPath C:\Backups\NyxveilControlPlane.bak -ConfirmProduction:$true
```

Without `-ConfirmProduction:$true` the script refuses to run.

Stop the Control Plane service before restore; start after verification.

## Update script integration

`update-windows.ps1` calls `backup-db.ps1` before deploying binaries.
