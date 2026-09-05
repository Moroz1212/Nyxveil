#Requires -Version 5.1
#Requires -RunAsAdministrator
<#
.SYNOPSIS
  Restore Nyxveil Control Plane database from a .bak file.

.DESCRIPTION
  Requires explicit -Force confirmation. Optionally stops the Windows service,
  restores, starts service, and runs health checks. Never runs without confirmation.

.PARAMETER BackupPath
  Path to the .bak file. For remote SQL Server this must be a path visible to the
  SQL Server service (SQL host local disk or UNC), not necessarily the Control Plane box.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$BackupPath,
    [string]$SqlServer = '',
    [string]$Database = '',
    [switch]$Force,
    [switch]$StopService,
    [switch]$UseSqlAuth,
    [string]$SqlUser = '',
    [securestring]$SqlPassword
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force

Assert-Administrator

if (-not $Force) {
    throw "Refusing restore. Pass -Force and type confirmation when prompted (destructive to target database)."
}

$dbSettings = Get-NyxveilDatabaseSettings
$resolvedServer = if ($SqlServer) { $SqlServer } else { [string]$dbSettings.Server }
$isLocal = Test-IsLocalDatabaseServer -DatabaseServer $resolvedServer

if ($isLocal) {
    if (-not (Test-Path -LiteralPath $BackupPath)) {
        throw "Backup file not found: $BackupPath"
    }
}
else {
    Write-Host @"
Remote SQL restore: skipping local Test-Path for BackupPath.
Ensure the SQL Server service can read:
  $BackupPath
"@
}

if ([string]::IsNullOrWhiteSpace($SqlServer)) { $SqlServer = [string]$dbSettings.Server }
if ([string]::IsNullOrWhiteSpace($Database)) { $Database = [string]$dbSettings.Database }
if (-not $UseSqlAuth -and [string]$dbSettings.Auth -eq 'Sql') {
    $UseSqlAuth = $true
}
if ($UseSqlAuth -and [string]::IsNullOrWhiteSpace($SqlUser)) {
    $SqlUser = [string]$dbSettings.User
}

$typed = Read-Host "Type the database name '$Database' to confirm destructive restore"
if ($typed -cne $Database) {
    throw 'Confirmation text did not match database name. Restore aborted.'
}

$op = $null
try { $op = Read-OperationalConfig } catch { }
$ServiceName = if ($op -and $op.ServiceName) { [string]$op.ServiceName } else { 'NyxveilControlPlane' }
$port = if ($op -and $op.Port) { [int]$op.Port } else { 0 }
$publicHostname = if ($op -and $op.PublicHostname) { [string]$op.PublicHostname } else { 'localhost' }
$certMode = if ($op -and $op.CertificateMode) { [string]$op.CertificateMode } else { 'Store' }
$InstallDir = if ($op -and $op.InstallDir) { [string]$op.InstallDir } else { 'C:\Program Files\Nyxveil\ControlPlane' }
Assert-ValidDatabaseName -DatabaseName $Database

$trust = [bool]$dbSettings.TrustSqlServerCertificate
$encrypt = [bool]$dbSettings.Encrypt
$auth = if ($UseSqlAuth) { 'Sql' } else { 'Windows' }
if ($UseSqlAuth -and -not $SqlPassword) {
    $secretsDir = Join-Path (Get-ProgramDataRoot) 'secrets'
    if ($op -and $op.SecretsDir) { $secretsDir = [string]$op.SecretsDir }
    $sqlPassPath = Join-Path $secretsDir 'sql-password.dpapi'
    if (Test-Path -LiteralPath $sqlPassPath) {
        $plain = Read-ProtectedSecret -Path $sqlPassPath
        $SqlPassword = ConvertTo-SecureString $plain -AsPlainText -Force
        $plain = $null
    }
    else {
        $SqlPassword = Read-Host 'SQL password' -AsSecureString
    }
}
if ($UseSqlAuth -and [string]::IsNullOrWhiteSpace($SqlUser)) {
    throw 'SQL Auth requires DatabaseUser in operational.json / appsettings or -SqlUser.'
}

# Safety backup of current DB before overwrite
$safetyDir = Join-Path (Get-ProgramDataRoot) 'backups\sql'
$safetyPath = Join-Path $safetyDir ("{0}-pre-restore-{1}.bak" -f $Database, (Get-Date -Format 'yyyyMMdd-HHmmss'))
Write-Host "Creating safety backup of current database to $safetyPath ..."
try {
    & (Join-Path $PSScriptRoot 'backup-db.ps1') -SqlServer $SqlServer -Database $Database -BackupPath $safetyPath `
        -UseSqlAuth:$UseSqlAuth -SqlUser $SqlUser -SqlPassword $SqlPassword
}
catch {
    throw "Safety backup failed; restore aborted. $($_.Exception.Message)"
}

$stopped = $false
if ($StopService) {
    $svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if ($svc -and $svc.Status -ne 'Stopped') {
        Write-Host "Stopping service $ServiceName..."
        Stop-Service -Name $ServiceName -Force
        $stopped = $true
    }
}

$escaped = $BackupPath.Replace("'", "''")
$sql = @"
ALTER DATABASE [$Database] SET SINGLE_USER WITH ROLLBACK IMMEDIATE;
RESTORE DATABASE [$Database] FROM DISK = N'$escaped' WITH REPLACE, STATS = 10;
ALTER DATABASE [$Database] SET MULTI_USER;
"@

Write-Host "Restoring $Database from $BackupPath ..."
Invoke-NyxveilSql -Server $SqlServer -Query $sql -DatabaseName $Database `
    -DatabaseAuth $auth -DatabaseUser $SqlUser -DatabasePassword $SqlPassword `
    -TrustSqlServerCertificate $trust -Encrypt $encrypt

# Lightweight schema compatibility probe
$probe = @"
USE [$Database];
SELECT CASE WHEN OBJECT_ID(N'dbo.AspNetUsers', N'U') IS NULL THEN 0 ELSE 1 END;
"@
Invoke-NyxveilSql -Server $SqlServer -Query $probe -DatabaseName $Database `
    -DatabaseAuth $auth -DatabaseUser $SqlUser -DatabasePassword $SqlPassword `
    -TrustSqlServerCertificate $trust -Encrypt $encrypt

if ($stopped -or $StopService) {
    Write-Host "Starting service $ServiceName..."
    Start-Service -Name $ServiceName -ErrorAction SilentlyContinue
}

if ($port -gt 0) {
    Write-Host "Post-restore health: $publicHostname :$port"
    if (-not (Wait-HttpsHealthy -Port $port -PublicHostname $publicHostname -InstallDir $InstallDir `
            -CertificateMode $certMode -TimeoutSec 60)) {
        Write-Warning "Health checks failed after restore. Safety backup: $safetyPath"
        throw 'Post-restore health failed.'
    }
}

Write-Host "Restore completed. Safety backup: $safetyPath"
