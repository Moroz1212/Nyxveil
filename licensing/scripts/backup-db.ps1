#Requires -Version 5.1
<#
.SYNOPSIS
  Backup Nyxveil Control Plane database using SQL Server BACKUP DATABASE + RESTORE VERIFYONLY.

.NOTES
  For remote SQL Server, -BackupPath must be a path visible to the SQL Server service host
  (not necessarily the Control Plane machine). Use a share or a local disk on the SQL host.
  This script does NOT Test-Path the backup file on remote SQL (VERIFYONLY is the gate).
#>
[CmdletBinding()]
param(
    [string]$SqlServer = '',
    [string]$Database = '',
    [string]$BackupPath = '',
    [string]$BackupDir = '',
    [switch]$UseSqlAuth,
    [string]$SqlUser = '',
    [securestring]$SqlPassword,
    [switch]$SkipVerify
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force

$dbSettings = Get-NyxveilDatabaseSettings

if ([string]::IsNullOrWhiteSpace($SqlServer)) { $SqlServer = [string]$dbSettings.Server }
if ([string]::IsNullOrWhiteSpace($Database)) { $Database = [string]$dbSettings.Database }
Assert-ValidDatabaseName -DatabaseName $Database

if (-not $UseSqlAuth -and [string]$dbSettings.Auth -eq 'Sql') {
    $UseSqlAuth = $true
}
if ($UseSqlAuth -and [string]::IsNullOrWhiteSpace($SqlUser)) {
    $SqlUser = [string]$dbSettings.User
}

$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
if ([string]::IsNullOrWhiteSpace($BackupPath)) {
    if ([string]::IsNullOrWhiteSpace($BackupDir)) {
        $BackupDir = Join-Path (Get-ProgramDataRoot) 'backups\sql'
    }
    $BackupPath = Join-Path $BackupDir ("{0}-{1}.bak" -f $Database, $stamp)
}

$isLocal = Test-IsLocalDatabaseServer -DatabaseServer $SqlServer
if (-not $isLocal) {
    Write-Host @"
Remote SQL Server detected ($SqlServer).
BACKUP DATABASE writes on the SQL Server host filesystem.
Ensure BackupPath is valid on that host (local disk or UNC share the SQL service can write):
  $BackupPath
Local Test-Path of the .bak is skipped; RESTORE VERIFYONLY is required.
"@
}

$dir = Split-Path $BackupPath -Parent
if ($dir -and $isLocal -and -not (Test-Path $dir)) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
}

$escaped = $BackupPath.Replace("'", "''")
$sql = "BACKUP DATABASE [$Database] TO DISK = N'$escaped' WITH INIT, COMPRESSION, STATS = 10;"

Write-Host "Backing up $Database on $SqlServer to $BackupPath ..."
$auth = if ($UseSqlAuth) { 'Sql' } else { 'Windows' }
if ($UseSqlAuth) {
    if (-not $SqlPassword) {
        $secretsDir = Join-Path (Get-ProgramDataRoot) 'secrets'
        try {
            $op = Read-OperationalConfig
            if ($op.SecretsDir) { $secretsDir = [string]$op.SecretsDir }
        } catch { }
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
    if ([string]::IsNullOrWhiteSpace($SqlUser)) {
        throw 'SQL Auth requires a configured DatabaseUser (operational.json / appsettings) or -SqlUser.'
    }
}

$trust = [bool]$dbSettings.TrustSqlServerCertificate
$encrypt = [bool]$dbSettings.Encrypt

Invoke-NyxveilSql -Server $SqlServer -Query $sql -DatabaseName $Database `
    -DatabaseAuth $auth -DatabaseUser $SqlUser -DatabasePassword $SqlPassword `
    -TrustSqlServerCertificate $trust -Encrypt $encrypt

if ($isLocal) {
    if (-not (Test-Path -LiteralPath $BackupPath)) {
        throw "Backup reported success but file missing: $BackupPath"
    }
    $len = (Get-Item -LiteralPath $BackupPath).Length
    if ($len -le 0) {
        throw "Backup file is empty: $BackupPath"
    }
    Write-Host "Backup completed ($len bytes): $BackupPath"
}
else {
    Write-Host "Backup command completed for remote SQL (no local Test-Path). Path on SQL host: $BackupPath"
}

if (-not $SkipVerify) {
    $verifySql = "RESTORE VERIFYONLY FROM DISK = N'$escaped';"
    Write-Host 'Running RESTORE VERIFYONLY...'
    Invoke-NyxveilSql -Server $SqlServer -Query $verifySql -DatabaseName $Database `
        -DatabaseAuth $auth -DatabaseUser $SqlUser -DatabasePassword $SqlPassword `
        -TrustSqlServerCertificate $trust -Encrypt $encrypt
    Write-Host 'VERIFYONLY succeeded.'
}
