# Live Deployment Test Checklist

Use this on a dedicated **lab or staging** Windows host before production cutover. Do not use a production database name until the final pass.

## Preconditions

- [ ] Elevated PowerShell 5.1+
- [ ] .NET 10 ASP.NET Core Runtime installed (`dotnet --list-runtimes`)
- [ ] SQL Server reachable; `sqlcmd` on PATH
- [ ] Published build: `dotnet publish .\src\Nyxveil.ControlPlane.Web\Nyxveil.ControlPlane.Web.csproj -c Release -o .\artifacts\web`
- [ ] Script static checks: `.\scripts\test-powershell.ps1` (exit 0)

## Auth matrix

| DatabaseAuth | DatabaseServer | ServiceAccount | Expected |
|--------------|----------------|----------------|----------|
| Windows | localhost / `.` / local instance | `NT SERVICE\NyxveilControlPlane` | Supported (grant SQL Windows login **after** Service SID) |
| Windows | remote host | `NT SERVICE\*` | **Blocked** by installer |
| Windows | remote host | gMSA (`DOMAIN\name$`) | Supported if machine resolves gMSA SID (no password param) |
| Windows | remote host | ordinary domain user | **Blocked** — no domain password path |
| Sql | local or remote | any | Supported (`sql-password.dpapi`; sqlcmd uses `SQLCMDPASSWORD`, never `-P`) |

- [ ] Local SQL: `TrustServerCertificate=True` by default; remote defaults `False` unless explicit `-TrustSqlServerCertificate`
- [ ] Service Environment registry has `ASPNETCORE_ENVIRONMENT=Production` (machine-wide must **not** be set by installer)
- [ ] `Certificate:ValidationMode` is `SystemTrust` (store/PFX) or `SelfSignedPinned` (`-GenerateSelfSignedCertificate`)

## Port and TLS hostname

- [ ] Installer default port **suggestion** is **8443** (any free 1–65535 is valid)
- [ ] `Hosting:BindAddress`, `Hosting:Port`, `Hosting:PublicHostname`, `Hosting:PublicBaseUrl` written (no `Kestrel:Endpoints`)
- [ ] After PFX or SelfSigned import/create, runtime `Certificate:Mode=Store` + `Thumbprint` (no production `Mode=Pfx` path; no SelfSigned without thumbprint)
- [ ] PublicHostname must match certificate (SAN/CN) **before** service install
- [ ] Health uses **PublicHostname** for TLS; local connect may use `127.0.0.1` via `curl --resolve` or CLI self-test
- [ ] Own service PID on the listen port is **not** treated as a foreign conflict (update/reconfigure/change-port)

## Fresh install

```powershell
.\scripts\install-windows.ps1 -InstallMode Fresh -PublishDir .\artifacts\web `
  -Port 8443 -PublicHostname control.example.com `
  -CertificateThumbprint <THUMB> `
  -DatabaseServer localhost -Database NyxveilControlPlane -DatabaseAuth Windows `
  -AdminUser admin@example.com
```

Order check (manual): service created **STOPPED** → SID → service env → ACLs → cert key ACL → SQL grant → admin → Start-Service → health.

- [ ] Fresh fails if service exists unless `-Force`
- [ ] Health gate fails the install when CLI/self-test health fails
- [ ] `.\scripts\self-test.ps1` exit 0 (includes service env + SID + cert hostname + SQL Encrypt/Trust display)
## Repair / Upgrade

- [ ] `-InstallMode Repair|Upgrade` (and Fresh when file already exists) does **not** overwrite `secrets\license-kek.dpapi`
- [ ] Fresh creates restricted secret dirs (`Initialize-NyxveilRestrictedDirectory`) **before** writing KEK
- [ ] `.\scripts\update-windows.ps1 -PublishDir .\artifacts\web` backs up DB + VERIFYONLY (remote SQL: no local `.bak` Test-Path), then binaries
- [ ] Binary rollback only if migration was **not** applied; if migration applied, check `ExpectedSchemaVersion` in `operational.json`

## Remote SQL backup path

- [ ] `backup-db.ps1`: for remote SQL, `BackupPath` is on the **SQL Server host** (or a share the SQL service can write); local `Test-Path` skipped
- [ ] `RESTORE VERIFYONLY` succeeds
- [ ] SQL Auth works without `-SqlUser` when `DatabaseUser` is in `operational.json` / appsettings
- [ ] `restore-db.ps1 -BackupPath <path>` documented for remote/local restore

## Recovery bundle

- [ ] `.\scripts\backup-recovery.ps1 -OutputPath C:\Backups\nyxveil-recovery.bin` invokes **`Nyxveil.ControlPlane.Web.exe backup-recovery --output ...`** directly (password on stdin; YES confirm) — does **not** call `backup-signing-keys.ps1`
- [ ] `.\scripts\restore-recovery.ps1 -InputPath ...` invokes **`restore-recovery`** CLI (YES confirm)
- [ ] Existing `license-kek.dpapi` is **never** overwritten on Fresh/Repair/Upgrade — use recovery restore to replace deliberately
- [ ] Restricted ACLs: secrets/keys/data-protection created SYSTEM+Administrators-only **before** writing KEK; service SID granted after service create

## Core interop (blocker)

- [ ] `.\scripts\test-core-interop.ps1` prints `TICKET_CS_TO_GO=PASS`, `CATALOG_CS_TO_GO=PASS`, `NODETOKEN_GO_TO_CS=PASS`, `AUDIENCE=nvp-node`, `CORE_SHA=verified`

## Change port / uninstall

- [ ] `.\scripts\change-port.ps1 -Port 8444` updates Hosting + firewall; rolls back on health failure
- [ ] `.\scripts\uninstall-windows.ps1` removes service/firewall/binaries; DB/secrets retained unless `-PurgeData`

## Sign-off

| Check | Pass |
|-------|------|
| test-powershell.ps1 | |
| Fresh install + self-test | |
| Auth matrix case used in prod | |
| Backup VERIFYONLY | |
| Recovery export/import lab | |
| Update + health | |
