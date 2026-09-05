# Nyxveil Control Plane

Central **Licensing / Control Plane** for Nyxveil VPN (Windows, .NET 10, SQL Server, Blazor admin, HTTPS API).

- **Version:** see [`VERSION`](VERSION) (`1.0.2`)
- **Protocol:** NVP/1 (frozen Core contracts)

## Requirements

| Component | Requirement |
|-----------|-------------|
| OS | Windows (Service hosting preferred) |
| Runtime | .NET 10 |
| Database | Microsoft SQL Server |
| TLS | HTTPS mandatory in Production |

## Frozen NVP/1 rules

- Access tickets are **location-scoped**
- Automatic failover is **same-location only**
- Ticket **refresh never widens** scope
- **Master** is a role, not a backdoor

## Solution layout

```
licensing/
  src/Nyxveil.ControlPlane.*     Web, Api, Application, Domain, Infrastructure, Worker
  tests/                         Unit + integration tests
  database/                      create_database.sql, seed_dev.sql
  scripts/                       install/update/backup/restore
  docs/                          Architecture and ops guides
  appsettings.Example.json       Secret-free template
  VERSION
```

## Initial database setup

```powershell
sqlcmd -S localhost -E -i .\database\create_database.sql
```

Optional **Development** seed only:

```powershell
sqlcmd -S localhost -E -d NyxveilControlPlane -i .\database\seed_dev.sql
```

## Configuration

1. Copy [`appsettings.Example.json`](appsettings.Example.json) to your environment file
2. Set `ConnectionStrings:ControlPlane` (or DPAPI `sql-password.dpapi` overlay in Production)
3. Set `Security:LicenseKekHex` to **64 hex characters** (32 bytes), or Production DPAPI `license-kek.dpapi`
4. Set `Signing:KeyProtectionPath`
5. Set `Hosting` (`BindAddress`, `Port` default suggestion **8443**, `PublicHostname`, `PublicBaseUrl`) — Program.cs Listen is SoT (no `Kestrel:Endpoints` required)
6. Configure `Certificate` as **Store + Thumbprint** for production runtime (installer imports PFX/SelfSigned then writes Store mode)

Web project settings live under `src/Nyxveil.ControlPlane.Web/appsettings*.json`.

## Development launch

```powershell
cd src\Nyxveil.ControlPlane.Web
dotnet run
```

- Swagger: Development only  
- Admin UI: `/` (Development may allow `/setup` when `Setup:AllowWebBootstrap=true`)  
- Health: `/health/live`, `/health/ready`

## First admin

**Production (preferred):** installer / CLI — service identity `NT SERVICE\NyxveilControlPlane`

```powershell
.\Nyxveil.ControlPlane.Web.exe admin create --username admin@example.com
```

Password from stdin. Exit `2` if a SuperAdmin already exists. Web `/setup` is off by default in Production (`Setup:AllowWebBootstrap=false`).

## Install as Windows Service

```powershell
dotnet publish .\src\Nyxveil.ControlPlane.Web\Nyxveil.ControlPlane.Web.csproj -c Release -o .\artifacts\web
.\scripts\test-powershell.ps1
.\scripts\install-windows.ps1 -InstallMode Fresh -PublishDir .\artifacts\web -Port 8443 -PublicHostname control.example.com
```

Service name: **NyxveilControlPlane** (identity **`NT SERVICE\NyxveilControlPlane`**; recovery restart configured by the script).

**Auth matrix:** local Windows Auth + `NT SERVICE\*` is supported; remote SQL + `NT SERVICE\*` is blocked; remote Windows Auth requires a **gMSA** (name ending with `$`) or use SQL Auth. Ordinary domain user/password is not supported. See [docs/DEPLOYMENT-WINDOWS.md](docs/DEPLOYMENT-WINDOWS.md).

Installer sets **service-specific** Production environment (registry `Environment` multi-string), not machine-wide `ASPNETCORE_ENVIRONMENT`.
## Backup

```powershell
.\scripts\backup-db.ps1 -Database NyxveilControlPlane -BackupPath C:\Backups\NyxveilControlPlane.bak
.\scripts\backup-recovery.ps1 -OutputPath C:\Backups\nyxveil-recovery.bin
```

Remote SQL: backup path must be on the SQL Server host. Recovery bundle uses CLI portable export (not an empty keys zip).

## Restore

```powershell
.\scripts\restore-db.ps1 -BackupPath C:\Backups\NyxveilControlPlane.bak -Force
.\scripts\restore-recovery.ps1 -InputPath C:\Backups\nyxveil-recovery.bin
```

## Update

```powershell
.\scripts\update-windows.ps1 -PublishDir .\artifacts\web
```

## Uninstall

```powershell
.\scripts\uninstall-windows.ps1
```

## API overview

| Audience | Examples |
|----------|----------|
| Client | `/api/v1/license/validate`, `/device/activate`, `/ticket/issue`, `/ticket/refresh`, `/catalog` |
| Node | `/api/v1/nodes/register`, `/nodes/heartbeat`, config + revocations |
| Admin | Blazor console + Identity cookies |

Details: [docs/CLIENT-API.md](docs/CLIENT-API.md), [docs/NODE-API.md](docs/NODE-API.md), [docs/ADMIN.md](docs/ADMIN.md).

## Documentation index

- [ARCHITECTURE.md](docs/ARCHITECTURE.md)
- [DATABASE.md](docs/DATABASE.md)
- [SECURITY.md](docs/SECURITY.md)
- [TICKETS.md](docs/TICKETS.md)
- [NODE-REGISTRATION.md](docs/NODE-REGISTRATION.md)
- [KEY-ROTATION.md](docs/KEY-ROTATION.md)
- [DEPLOYMENT-WINDOWS.md](docs/DEPLOYMENT-WINDOWS.md)
- [LIVE-DEPLOYMENT-TEST.md](docs/LIVE-DEPLOYMENT-TEST.md)
- [BACKUP-RESTORE.md](docs/BACKUP-RESTORE.md)
- [PAYMENT-INTEGRATION-FUTURE.md](docs/PAYMENT-INTEGRATION-FUTURE.md)

## Build

```powershell
dotnet restore
dotnet build -c Release
dotnet test -c Release
dotnet publish .\src\Nyxveil.ControlPlane.Web\Nyxveil.ControlPlane.Web.csproj -c Release
```
