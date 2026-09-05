# Deployment — Windows

## Prerequisites

- Windows Server / Windows 10+
- .NET 10 ASP.NET Core Runtime
- Microsoft SQL Server
- Administrator rights for service install
- TLS certificate for Production HTTPS (Windows Store preferred)
- `sqlcmd` on PATH

## Publish

```powershell
dotnet publish .\src\Nyxveil.ControlPlane.Web\Nyxveil.ControlPlane.Web.csproj -c Release -o .\artifacts\web
```

Static script check:

```powershell
.\scripts\test-powershell.ps1
```

## Database

```powershell
# Prefer installer (Invoke-NyxveilSql). Manual:
sqlcmd -S localhost -E -b -i .\database\create_database.sql -v DatabaseName=NyxveilControlPlane
```

Do **not** run `seed_dev.sql` in Production.

### Auth matrix

| DatabaseAuth | SQL host | Service account | Notes |
|--------------|----------|-----------------|-------|
| Windows | Local | `NT SERVICE\NyxveilControlPlane` | Default. Installer grants app DB `db_owner` after Service SID exists. |
| Windows | Remote | `NT SERVICE\*` | **Not supported** — virtual accounts are local-only. |
| Windows | Remote | gMSA (`DOMAIN\svc-nyxveil$`) | Supported if this machine can resolve the gMSA SID. No password parameter. |
| Windows | Remote | Ordinary domain user / password | **Not supported** — installer does not accept a domain password for the service identity. |
| Sql | Local or remote | any | Password in `sql-password.dpapi`. Scripts set process env `SQLCMDPASSWORD` (never `sqlcmd -P`). |

`TrustServerCertificate`: local SQL defaults **true**; remote defaults **false**. Remote + trust requires explicit `-TrustSqlServerCertificate` (warning emitted).

Database names must match `^[A-Za-z_][A-Za-z0-9_-]{0,127}$`.

### Remote SQL backups

`BACKUP DATABASE` / `RESTORE` paths are evaluated on the **SQL Server host**. For remote SQL, `-BackupPath` must be a local path on that host or a UNC share the SQL service can write. See `backup-db.ps1` (includes `RESTORE VERIFYONLY`).

## Install service

```powershell
.\scripts\install-windows.ps1 -InstallMode Fresh -PublishDir .\artifacts\web `
  -Port 8443 -PublicHostname control.example.com
```

| Mode | Behavior |
|------|----------|
| `Fresh` (default) | Fails if service exists unless `-Force` |
| `Repair` / `Upgrade` | Keeps existing `license-kek.dpapi` when present |

Service name: `NyxveilControlPlane`  
Service identity: **`NT SERVICE\NyxveilControlPlane`** (grant SQL + certificate private-key ACL **after** service create + SID)  
Uses `UseWindowsService()` in the host.

**Environment:** installer sets **service-specific** `Environment` REG_MULTI_SZ (`ASPNETCORE_ENVIRONMENT=Production`, `DOTNET_ENVIRONMENT=Production`). It does **not** set machine-wide `ASPNETCORE_ENVIRONMENT`.

### Fresh install order

1. Validate inputs / port / cert hostname / SQL auth matrix  
2. Bootstrap DB (admin connection)  
3. Copy binaries + write `appsettings.Production.json` (`Certificate:Mode=Store`, `ValidationMode=SystemTrust|SelfSignedPinned`, SQL Encrypt/Trust policy)  
4. Create Windows service **STOPPED**  
5. `Ensure-NyxveilServiceSid` (`sc.exe sidtype unrestricted` + resolve SID — fail closed)  
6. `Set-NyxveilServiceEnvironment` Production  
7. Directory ACLs (`icacls` exit-checked)  
8. Certificate private-key ACL (RSA + ECDSA — fail closed)  
9. Grant SQL login for service account  
10. Verify  
11. First admin CLI  
12. `Start-Service`  
13. Health / self-test (fail closed)  
14. Commit  

Repair/Upgrade rollback does **not** delete an existing service; Fresh rollback deletes only a service created this run.

## Port and public URL

Configure under `Hosting` (Program.cs Listen is source of truth — **do not** rely on `Kestrel:Endpoints`):

| Setting | Example | Purpose |
|---------|---------|---------|
| `BindAddress` | `0.0.0.0` | Listen address |
| `Port` | `8443` | HTTPS listen port (installer default **suggestion**; any free port is valid) |
| `PublicHostname` | `control.example.com` | Hostname for TLS / health |
| `PublicBaseUrl` | `https://control.example.com:8443` | Operator-facing base URL |

Health checks prefer `Nyxveil.ControlPlane.Web.exe self-test` (or `self-test-http` when available). Hostname-aware HTTP uses `PublicHostname` for certificate validation and may connect via `127.0.0.1` (`curl --resolve`). Scripts do **not** use `SkipCertificateCheck` / TrustAll.

Port already owned by **our** `NyxveilControlPlane` service PID is allowed for update/reconfigure/change-port. Foreign PIDs are conflicts.

```powershell
.\scripts\change-port.ps1 -Port 8444
```

## TLS certificate modes

Production runtime config always uses **Windows Certificate Store + thumbprint** after a successful PFX import or SelfSigned create+import. Do not leave production on `Mode=Pfx` with a path, or `Mode=SelfSigned` without a thumbprint.

`Certificate:ValidationMode`:
- **SystemTrust** — Store/PFX after CA import (X509Chain / SslStream system trust)
- **SelfSignedPinned** — when installer used `-GenerateSelfSignedCertificate` (thumbprint + hostname + validity; never TrustAll)

Installer fails closed if `PublicHostname` does not match the certificate DNS SAN/CN **before** creating the service.

### A. Windows Certificate Store (recommended)

```json
"Certificate": {
  "Mode": "Store",
  "Thumbprint": "YOURTHUMBPRINT",
  "StoreName": "My",
  "StoreLocation": "LocalMachine"
}
```

### B. PFX import (installer)

Installer imports the PFX into `LocalMachine\My`, then writes **Store + Thumbprint**. Runtime does not depend on the source PFX file.

### C. Self-signed (lab only)

Installer creates a cert in the store, then writes **Store + Thumbprint** with `Certificate:ValidationMode=SelfSignedPinned`. Not recommended for public Production.

CA-backed Store/PFX installs use `ValidationMode=SystemTrust` (full SslStream chain + hostname; no custom callback).

Offline installer gate:

```powershell
.\Nyxveil.ControlPlane.Web.exe certificate validate --hostname control.example.com --thumbprint YOURTHUMBPRINT
# self-signed:
.\Nyxveil.ControlPlane.Web.exe certificate validate --hostname lab.local --thumbprint YOURTHUMBPRINT --self-signed-pinned
```

`Https:RequireHttpsInProduction=true` fail-closes unless a certificate **with a private key** can be loaded.

## Data Protection keys

Production persists ASP.NET Data Protection keys under:

`C:\ProgramData\Nyxveil\ControlPlane\data-protection\`

(Installer ACLs this for the service account after the Windows Service is created so the NT SERVICE\* SID exists.)

## Protected secrets (Production)

`C:\ProgramData\Nyxveil\ControlPlane\secrets\`

| File | Maps to |
|------|---------|
| `license-kek.dpapi` | `Security:LicenseKekHex` |
| `sql-password.dpapi` | Connection string password overlay (SQL Auth) |

## First admin (CLI)

Web `/setup` is **disabled** in Production by default (`Setup:AllowWebBootstrap=false`).

```powershell
.\Nyxveil.ControlPlane.Web.exe admin create --username admin@example.com
```

## Health / self-test

- `GET /health/live` — process up  
- `GET /health/ready` — SQL + signing key  

```powershell
.\Nyxveil.ControlPlane.Web.exe self-test
.\scripts\self-test.ps1
```

## Signing key / recovery bundle

Prefer CLI portable export (decrypted then re-encrypted), not zipping an empty keys folder:

```powershell
.\scripts\backup-recovery.ps1 -OutputPath C:\Backups\nyxveil-recovery.bin
.\scripts\restore-recovery.ps1 -InputPath C:\Backups\nyxveil-recovery.bin
# or:
.\Nyxveil.ControlPlane.Web.exe backup-signing-keys export --output keys.zip
.\Nyxveil.ControlPlane.Web.exe backup-signing-keys import --input keys.zip
```

Password on stdin / SecureString. Restore requires typed `YES` confirmation.

## Update

```powershell
.\scripts\update-windows.ps1 -PublishDir .\artifacts\web
```

Backs up DB (VERIFYONLY) + binaries, stops service, deploys, optional `-MigrationScript`, starts, health-checks. Binary rollback only if migration was not applied; if migration applied, inspect `ExpectedSchemaVersion` in `operational.json` and restore DB if needed.

## Uninstall

```powershell
.\scripts\uninstall-windows.ps1              # service, firewall, binaries
.\scripts\uninstall-windows.ps1 -PurgeData   # also ProgramData (not database drop)
```

## Release pack

```powershell
.\scripts\pack-release.ps1 -PublishDir .\artifacts\web
```

ZIP entries use forward slashes only (`backslash_count=0`).

## Live checklist

See [LIVE-DEPLOYMENT-TEST.md](LIVE-DEPLOYMENT-TEST.md).

## IIS (optional)

IIS can reverse-proxy to Kestrel. Prefer the Windows Service path for the primary installer.
