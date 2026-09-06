#Requires -Version 5.1
#Requires -RunAsAdministrator
<#
.SYNOPSIS
  Restore Nyxveil Control Plane database from a .bak file.

.DESCRIPTION
  Administrative RESTORE always connects to [master] (never the target DB session)
  to avoid SQL Server Msg 3102 ("database is in use by this session").
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
    [securestring]$SqlPassword,
    [string]$ConfirmDatabaseName = '',
    # When set, skip safety backup / health restart (used by production-deploy rollback).
    [switch]$AutomatedRollback
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force

Assert-Administrator

if (-not $Force) {
    throw "Refusing restore. Pass -Force and confirm the database name."
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

$typed = $ConfirmDatabaseName
if ([string]::IsNullOrWhiteSpace($typed)) {
    $typed = Read-Host "Type the database name '$Database' to confirm destructive restore"
}
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

$stopped = $false
$enteredSingleUser = $false
$restoreSucceeded = $false
try {
    if ($StopService) {
        $svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
        if ($svc -and $svc.Status -ne 'Stopped') {
            Write-Host "Stopping service $ServiceName..."
            Stop-Service -Name $ServiceName -Force
            (Get-Service -Name $ServiceName).WaitForStatus('Stopped', [TimeSpan]::FromSeconds(60))
            $stopped = $true
        }
    }

    if (-not $AutomatedRollback) {
        $safetyDir = Join-Path (Get-ProgramDataRoot) 'backups\sql'
        $safetyPath = Join-Path $safetyDir ("{0}-pre-restore-{1}.bak" -f $Database, (Get-Date -Format 'yyyyMMdd-HHmmss'))
        Write-Host "Creating safety backup of current database to $safetyPath ..."
        & (Join-Path $PSScriptRoot 'backup-db.ps1') -SqlServer $SqlServer -Database $Database -BackupPath $safetyPath `
            -UseSqlAuth:$UseSqlAuth -SqlUser $SqlUser -SqlPassword $SqlPassword
    }

    $escaped = $BackupPath.Replace("'", "''")
    $dbEscaped = $Database.Replace(']', ']]')

    # MUST connect to master — never DatabaseName=$Database (Msg 3102).
    $sql = @"
SET NOCOUNT ON;
SET XACT_ABORT ON;
USE [master];

IF DB_ID(N'$($Database.Replace("'","''"))') IS NULL
    THROW 51001, N'Target database does not exist.', 1;

ALTER DATABASE [$dbEscaped] SET SINGLE_USER WITH ROLLBACK IMMEDIATE;
RESTORE DATABASE [$dbEscaped] FROM DISK = N'$escaped' WITH REPLACE, RECOVERY, STATS = 10;
ALTER DATABASE [$dbEscaped] SET MULTI_USER;
"@

    Write-Host "Restoring $Database from $BackupPath via master session..."
    $enteredSingleUser = $true
    Invoke-NyxveilSql -Server $SqlServer -Query $sql -DatabaseName 'master' `
        -DatabaseAuth $auth -DatabaseUser $SqlUser -DatabasePassword $SqlPassword `
        -TrustSqlServerCertificate $trust -Encrypt $encrypt
    $restoreSucceeded = $true
    $enteredSingleUser = $false

    $probe = @"
SET NOCOUNT ON;
USE [master];
SELECT
  name,
  state_desc,
  user_access_desc
FROM sys.databases
WHERE name = N'$($Database.Replace("'","''"))';
"@
    Invoke-NyxveilSql -Server $SqlServer -Query $probe -DatabaseName 'master' `
        -DatabaseAuth $auth -DatabaseUser $SqlUser -DatabasePassword $SqlPassword `
        -TrustSqlServerCertificate $trust -Encrypt $encrypt

    if ($stopped -or ($StopService -and -not $AutomatedRollback)) {
        Write-Host "Starting service $ServiceName..."
        Start-Service -Name $ServiceName -ErrorAction SilentlyContinue
    }

    if (-not $AutomatedRollback -and $port -gt 0) {
        Write-Host "Post-restore health: $publicHostname :$port"
        if (-not (Wait-HttpsHealthy -Port $port -PublicHostname $publicHostname -InstallDir $InstallDir `
                -CertificateMode $certMode -TimeoutSec 60)) {
            throw 'Post-restore health failed.'
        }
    }

    Write-Host "Restore completed for $Database."
}
catch {
    if ($enteredSingleUser -and -not $restoreSucceeded) {
        try {
            $recover = @"
USE [master];
IF DB_ID(N'$($Database.Replace("'","''"))') IS NOT NULL
    ALTER DATABASE [$dbEscaped] SET MULTI_USER WITH ROLLBACK IMMEDIATE;
"@
            Invoke-NyxveilSql -Server $SqlServer -Query $recover -DatabaseName 'master' `
                -DatabaseAuth $auth -DatabaseUser $SqlUser -DatabasePassword $SqlPassword `
                -TrustSqlServerCertificate $trust -Encrypt $encrypt
            Write-Warning "Attempted MULTI_USER recovery after failed restore for $Database."
        }
        catch {
            Write-Warning "MULTI_USER recovery also failed: $($_.Exception.Message)"
        }
    }
    throw
}
