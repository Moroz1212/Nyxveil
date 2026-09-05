#Requires -Version 5.1
#Requires -RunAsAdministrator
<#
.SYNOPSIS
  Update Nyxveil Control Plane binaries safely (health → DB backup → binary backup → deploy → start → health).

.DESCRIPTION
  Supports Windows and Sql auth via Invoke-NyxveilSql.
  Binary rollback is performed only when a migration was NOT applied.
  If a migration was applied, schema compatibility is checked against operational.json ExpectedSchemaVersion.
#>
[CmdletBinding()]
param(
    [string]$PublishDir = '',
    [string]$InstallDir = '',
    [string]$ServiceName = '',
    [string]$BackupRoot = '',
    [string]$MigrationScript = '',
    [string]$DatabaseUser = '',
    [securestring]$DatabasePassword,
    [string]$ExpectedSchemaVersion = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force

Assert-Administrator

$op = $null
try { $op = Read-OperationalConfig } catch { }

if ([string]::IsNullOrWhiteSpace($InstallDir)) {
    $InstallDir = if ($op -and $op.InstallDir) { [string]$op.InstallDir } else { 'C:\Program Files\Nyxveil\ControlPlane' }
}
if ([string]::IsNullOrWhiteSpace($ServiceName)) {
    $ServiceName = if ($op -and $op.ServiceName) { [string]$op.ServiceName } else { 'NyxveilControlPlane' }
}
if ([string]::IsNullOrWhiteSpace($BackupRoot)) {
    $BackupRoot = Join-Path (Get-ProgramDataRoot) 'backups'
}

$port = if ($op -and $op.Port) { [int]$op.Port } else { 0 }
$publicHostname = if ($op -and $op.PublicHostname) { [string]$op.PublicHostname } else { 'localhost' }
$dbSettings = Get-NyxveilDatabaseSettings -InstallDir $InstallDir
$dbServer = [string]$dbSettings.Server
$dbName = [string]$dbSettings.Database
$dbAuth = [string]$dbSettings.Auth
$certMode = if ($op -and $op.CertificateMode) { [string]$op.CertificateMode } else { 'Store' }
$currentSchema = '1'
if ($op -and $op.PSObject.Properties.Name -contains 'ExpectedSchemaVersion' -and $op.ExpectedSchemaVersion) {
    $currentSchema = [string]$op.ExpectedSchemaVersion
}
if ([string]::IsNullOrWhiteSpace($ExpectedSchemaVersion)) {
    $ExpectedSchemaVersion = $currentSchema
}

if ($port -le 0) { throw 'operational.json Port missing. Cannot update safely.' }
Assert-ValidDatabaseName -DatabaseName $dbName

if ($dbAuth -eq 'Sql') {
    $secretsDir = if ($op -and $op.SecretsDir) { [string]$op.SecretsDir } else { Join-Path (Get-ProgramDataRoot) 'secrets' }
    if (-not $DatabasePassword) {
        $sqlPassPath = Join-Path $secretsDir 'sql-password.dpapi'
        if (Test-Path -LiteralPath $sqlPassPath) {
            $plain = Read-ProtectedSecret -Path $sqlPassPath
            $DatabasePassword = ConvertTo-SecureString $plain -AsPlainText -Force
            $plain = $null
        }
        else {
            $DatabasePassword = Read-Host 'SQL password' -AsSecureString
        }
    }
    if ([string]::IsNullOrWhiteSpace($DatabaseUser)) {
        $DatabaseUser = [string]$dbSettings.User
    }
    if ([string]::IsNullOrWhiteSpace($DatabaseUser)) {
        throw 'SQL Auth requires -DatabaseUser or DatabaseUser in operational.json / appsettings.Production.json.'
    }
}

$PublishDir = Get-PublishedBuildDir -PublishDir $PublishDir
if (-not (Test-Path $InstallDir)) { throw "InstallDir not found: $InstallDir" }

Write-Host "Pre-update health check ($publicHostname :$port)..."
$certValidationMode = ''
if ($op.PSObject.Properties.Name -contains 'CertificateValidationMode') { $certValidationMode = [string]$op.CertificateValidationMode }
if (-not (Test-HttpsHealthLocal -Port $port -PublicHostname $publicHostname -InstallDir $InstallDir `
            -CertificateMode $certMode -CertificateValidationMode $certValidationMode)) {
    Write-Warning 'Pre-update health failed. Continuing only if you still intend to update.'
}

$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$backupDir = Join-Path $BackupRoot $stamp
New-Item -ItemType Directory -Force -Path $backupDir | Out-Null

$isLocalSql = Test-IsLocalDatabaseServer -DatabaseServer $dbServer
Write-Host 'Backing up database (abort on failure; VERIFYONLY; remote skips local .bak Test-Path)...'
Assert-SqlCmdAvailable -Context 'update database backup'
$dbBak = Join-Path $backupDir ("{0}-{1}.bak" -f $dbName, $stamp)
if (-not $isLocalSql) {
    # Remote SQL: backup path must be on the SQL host. Prefer a dedicated SQL-visible path under ProgramData
    # only when local; for remote require the SQL service can write the same logical path / use UNC.
    Write-Host "Remote SQL ($dbServer): BACKUP path must be valid on the SQL Server host: $dbBak"
}
$bakArgs = @{
    SqlServer  = $dbServer
    Database   = $dbName
    BackupPath = $dbBak
}
if ($dbAuth -eq 'Sql') {
    $bakArgs['UseSqlAuth'] = $true
    $bakArgs['SqlUser'] = $DatabaseUser
    $bakArgs['SqlPassword'] = $DatabasePassword
}
& (Join-Path $PSScriptRoot 'backup-db.ps1') @bakArgs
if ($isLocalSql -and -not (Test-Path -LiteralPath $dbBak)) {
    throw 'Database backup file missing after backup-db.ps1. Update aborted.'
}
if (-not $isLocalSql) {
    Write-Host 'Remote SQL backup completed (VERIFYONLY gate inside backup-db.ps1; no local file check).'
}

Write-Host 'Backing up binaries + config...'
$binBackup = Join-Path $backupDir 'binaries'
New-Item -ItemType Directory -Force -Path $binBackup | Out-Null
Backup-DirectoryContents -SourceDir $InstallDir -DestinationDir $binBackup
Copy-Item (Join-Path $InstallDir 'appsettings*.json') -Destination $backupDir -Force -ErrorAction SilentlyContinue
$opPath = Get-OperationalConfigPath -InstallDir $InstallDir
if (Test-Path $opPath) {
    Copy-Item $opPath -Destination (Join-Path $backupDir 'operational.json') -Force
}

$deployed = $false
$migrationApplied = $false
try {
    Write-Host "Stopping service $ServiceName..."
    Stop-Service -Name $ServiceName -Force -ErrorAction Stop

    Write-Host 'Deploying new binaries (preserving appsettings.Production.json / operational config)...'
    $preserve = @('appsettings.Production.json', 'appsettings.json', 'config')
    Get-ChildItem -LiteralPath $InstallDir -Force | Where-Object {
        $preserve -notcontains $_.Name -and $_.Name -notlike 'appsettings*.json'
    } | ForEach-Object {
        Remove-Item -LiteralPath $_.FullName -Recurse -Force -ErrorAction SilentlyContinue
    }
    Copy-Item -Path (Join-Path $PublishDir '*') -Destination $InstallDir -Recurse -Force
    $prodBak = Join-Path $binBackup 'appsettings.Production.json'
    if (Test-Path $prodBak) {
        Copy-Item $prodBak -Destination (Join-Path $InstallDir 'appsettings.Production.json') -Force
    }
    $cfgBak = Join-Path $binBackup 'config'
    if (Test-Path $cfgBak) {
        Copy-Item $cfgBak -Destination (Join-Path $InstallDir 'config') -Recurse -Force
    }
    $deployed = $true

    if ($MigrationScript -and (Test-Path $MigrationScript)) {
        Write-Host "Applying migration script $MigrationScript..."
        Invoke-NyxveilSql -Server $dbServer -InputFile $MigrationScript -DatabaseName $dbName `
            -DatabaseAuth $dbAuth -DatabaseUser $DatabaseUser -DatabasePassword $DatabasePassword `
            -TrustSqlServerCertificate ([bool]$dbSettings.TrustSqlServerCertificate) `
            -Encrypt ([bool]$dbSettings.Encrypt)
        $migrationApplied = $true

        if ($op) {
            $op | Add-Member -NotePropertyName ExpectedSchemaVersion -NotePropertyValue $ExpectedSchemaVersion -Force
            Write-OperationalConfig -Config $op -InstallDir $InstallDir
        }
    }
    else {
        Write-Host 'No MigrationScript provided. Apply EF/SQL migrations manually if this release requires them.'
    }

    Write-Host 'Starting service...'
    Start-Service -Name $ServiceName
    if (-not (Wait-HttpsHealthy -Port $port -PublicHostname $publicHostname -InstallDir $InstallDir `
            -CertificateMode $certMode -CertificateValidationMode $certValidationMode -TimeoutSec 60)) {
        throw "Post-update health failed at $publicHostname :$port"
    }
    Write-Host "Update complete. Backup: $backupDir"
}
catch {
    Write-Warning "Update failed: $($_.Exception.Message)"
    if ($migrationApplied) {
        Write-Warning @"
A database migration was applied. Binary rollback alone may leave schema ahead of binaries.
operational.json ExpectedSchemaVersion=$ExpectedSchemaVersion (pre-update was $currentSchema).
Restore DB from $dbBak if you must fully roll back; binary-only rollback is skipped.
"@
        if ($currentSchema -ne $ExpectedSchemaVersion) {
            Write-Warning "Schema version mismatch metadata: was=$currentSchema expected=$ExpectedSchemaVersion"
        }
    }
    elseif ($deployed -and (Test-Path $binBackup)) {
        Write-Warning 'Restoring previous binaries and restarting service (migration was not applied)...'
        try {
            Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
            Get-ChildItem -LiteralPath $InstallDir -Force -ErrorAction SilentlyContinue |
                Remove-Item -Recurse -Force -ErrorAction SilentlyContinue
            Backup-DirectoryContents -SourceDir $binBackup -DestinationDir $InstallDir
            Start-Service -Name $ServiceName -ErrorAction SilentlyContinue
        }
        catch {
            Write-Warning "Binary restore encountered: $($_.Exception.Message)"
        }
    }
    throw
}
