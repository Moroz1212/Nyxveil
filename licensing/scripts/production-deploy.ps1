#Requires -Version 5.1
<#
.SYNOPSIS
  Hardened emergency production deployment for Nyxveil Control Plane 1.3.1.

.DESCRIPTION
  Backs up and verifies production, rehearses schema v3 against a disposable
  restored database, then performs the service outage and deploy. No live
  service, production database, or installed files are changed before rehearsal passes.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$PublishDir,
    [string]$InstallDir = '',
    [string]$ServiceName = 'NyxveilControlPlane',
    [string]$MigrationScript = '',
    [string]$ReleaseZip = '',
    [string]$ExpectedSchemaVersion = '4'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$licensingRoot = Split-Path -Parent $PSScriptRoot
Import-Module (Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force

$requiredServiceName = 'NyxveilControlPlane'
$validationScript = Join-Path $licensingRoot 'database\migrations\validate_schema_v3.sql'
$stage = 'precheck'
$failedGate = ''
$gateLog = ''
$backupDir = ''
$binaryBackup = ''
$configBackup = ''
$databaseBackup = ''
$packageFingerprint = ''
$releaseZipFingerprint = ''
$deployStarted = $false
$productionMigrationAttempted = $false
$databasePassword = $null
$op = $null
$originalOperationalJson = ''
$events = [Collections.Generic.List[string]]::new()

function Add-DeployEvent {
    param([Parameter(Mandatory = $true)][string]$Message)
    $line = '{0:o} {1}' -f (Get-Date).ToUniversalTime(), $Message
    $script:events.Add($line)
    Write-Host $Message
}

function ConvertTo-SanitizedText {
    param([AllowEmptyString()][string]$Text)
    if ($null -eq $Text) { return '' }
    $safe = $Text
    $safe = $safe -replace '(?i)((?:password|pwd|token|secret|api[_-]?key|private[_-]?key)\s*[=:]\s*)[^;\s]+', '$1<redacted>'
    $safe = $safe -replace '(?i)(SQLCMDPASSWORD\s*[=:]\s*)[^;\s]+', '$1<redacted>'
    $safe = $safe -replace '(?i)(Password\s*=\s*)[^;]+', '$1<redacted>'
    return $safe
}

function Copy-DirectoryExact {
    param(
        [Parameter(Mandatory = $true)][string]$Source,
        [Parameter(Mandatory = $true)][string]$Destination
    )
    if (-not (Test-Path -LiteralPath $Destination)) {
        New-Item -ItemType Directory -Path $Destination -Force | Out-Null
    }
    Get-ChildItem -LiteralPath $Source -Force | ForEach-Object {
        Copy-Item -LiteralPath $_.FullName -Destination $Destination -Recurse -Force
    }
}

function Clear-DirectoryContents {
    param([Parameter(Mandatory = $true)][string]$Path)
    Get-ChildItem -LiteralPath $Path -Force -ErrorAction Stop | ForEach-Object {
        Remove-Item -LiteralPath $_.FullName -Recurse -Force -ErrorAction Stop
    }
}

function Get-PackageFingerprint {
    param([Parameter(Mandatory = $true)][string]$Path)
    $files = @(Get-ChildItem -LiteralPath $Path -Recurse -File -Force | Sort-Object FullName)
    if ($files.Count -eq 0) { throw "PublishDir contains no files: $Path" }
    $lines = foreach ($file in $files) {
        $relative = $file.FullName.Substring($Path.TrimEnd('\').Length).TrimStart('\')
        $hash = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash
        '{0}|{1}|{2}' -f $relative, $file.Length, $hash
    }
    $bytes = [Text.Encoding]::UTF8.GetBytes(($lines -join "`n"))
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        return ([BitConverter]::ToString($sha.ComputeHash($bytes)) -replace '-', '').ToLowerInvariant()
    }
    finally {
        $sha.Dispose()
        [Array]::Clear($bytes, 0, $bytes.Length)
    }
}

function Assert-SeparateDirectoryTrees {
    param(
        [Parameter(Mandatory = $true)][string]$First,
        [Parameter(Mandatory = $true)][string]$Second
    )
    $a = $First.TrimEnd('\') + '\'
    $b = $Second.TrimEnd('\') + '\'
    if ($a.StartsWith($b, [StringComparison]::OrdinalIgnoreCase) -or
        $b.StartsWith($a, [StringComparison]::OrdinalIgnoreCase)) {
        throw "PublishDir and InstallDir must be separate directory trees. PublishDir=$First InstallDir=$Second"
    }
}

function Invoke-DeploymentSql {
    param(
        [string]$InputFile = '',
        [string]$Query = '',
        [Parameter(Mandatory = $true)][string]$DatabaseName,
        [string[]]$ExtraArgs = @()
    )
    Invoke-NyxveilSql -Server $script:dbServer -InputFile $InputFile -Query $Query `
        -DatabaseName $DatabaseName -DatabaseAuth $script:dbAuth `
        -DatabaseUser $script:dbUser -DatabasePassword $script:databasePassword `
        -ExtraArgs $ExtraArgs `
        -TrustSqlServerCertificate ([bool]$script:dbSettings.TrustSqlServerCertificate) `
        -Encrypt ([bool]$script:dbSettings.Encrypt)
}

function Remove-MigrationCheckDatabases {
    $prefix = 'NyxveilControlPlane_MigrationCheck_'
    $cleanupSql = @"
SET NOCOUNT ON;
USE [master];
DECLARE @sql nvarchar(max) = N'';
SELECT @sql += N'ALTER DATABASE ' + QUOTENAME(name) +
    N' SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE ' + QUOTENAME(name) + N';'
FROM sys.databases
WHERE name LIKE N'NyxveilControlPlane[_]MigrationCheck[_]%';
IF LEN(@sql) > 0 EXEC sys.sp_executesql @sql;
"@
    Invoke-DeploymentSql -Query $cleanupSql -DatabaseName 'master'
    Add-DeployEvent "Removed abandoned rehearsal databases matching ${prefix}%."
}

function Invoke-MigrationRehearsal {
    param(
        [Parameter(Mandatory = $true)][string]$BackupPath,
        [Parameter(Mandatory = $true)][string]$MigrationPath,
        [Parameter(Mandatory = $true)][string]$ValidatePath
    )

    Remove-MigrationCheckDatabases
    $id = [guid]::NewGuid().ToString('N')
    $tempDatabase = "NyxveilControlPlane_MigrationCheck_$($id.Substring(0, 8))"
    Assert-ValidDatabaseName -DatabaseName $tempDatabase
    $rehearsalDir = Join-Path (Join-Path (Get-ProgramDataRoot) 'migration-rehearsal') $id
    New-Item -ItemType Directory -Path $rehearsalDir -Force | Out-Null

    $created = $false
    try {
        $fileListSql = Get-NyxveilFileListOnlySql -BackupPath $BackupPath
        $fileListOutput = @(Invoke-DeploymentSql -Query $fileListSql -DatabaseName 'master' `
            -ExtraArgs @('-h', '-1', '-W', '-s', '|'))
        $databaseFiles = [Collections.Generic.List[object]]::new()
        foreach ($line in $fileListOutput) {
            $parts = ([string]$line).Split('|')
            if ($parts.Count -lt 3) { continue }
            $logicalName = $parts[0].Trim()
            $fileType = $parts[2].Trim()
            if ($logicalName -and $fileType -in @('D', 'L')) {
                $databaseFiles.Add([pscustomobject]@{
                    LogicalName = $logicalName
                    Type = $fileType
                })
            }
        }

        $dataFiles = @($databaseFiles | Where-Object Type -eq 'D')
        $logFiles = @($databaseFiles | Where-Object Type -eq 'L')
        if ($dataFiles.Count -eq 0 -or $logFiles.Count -eq 0) {
            throw 'RESTORE FILELISTONLY did not return at least one data file and one log file.'
        }

        $dataPath = Join-Path $rehearsalDir "$tempDatabase.mdf"
        $logPath = Join-Path $rehearsalDir "${tempDatabase}_log.ldf"
        $additional = [Collections.Generic.List[object]]::new()
        for ($i = 1; $i -lt $dataFiles.Count; $i++) {
            $additional.Add([pscustomobject]@{
                LogicalName = $dataFiles[$i].LogicalName
                PhysicalPath = (Join-Path $rehearsalDir ("{0}_data{1}.ndf" -f $tempDatabase, ($i + 1)))
            })
        }
        for ($i = 1; $i -lt $logFiles.Count; $i++) {
            $additional.Add([pscustomobject]@{
                LogicalName = $logFiles[$i].LogicalName
                PhysicalPath = (Join-Path $rehearsalDir ("{0}_log{1}.ldf" -f $tempDatabase, ($i + 1)))
            })
        }

        $restoreSql = Get-NyxveilRestoreMasterSql -DatabaseName $tempDatabase -BackupPath $BackupPath `
            -DataLogicalName ([string]$dataFiles[0].LogicalName) `
            -LogLogicalName ([string]$logFiles[0].LogicalName) `
            -DataPath $dataPath -LogPath $logPath -AdditionalFileMappings $additional.ToArray()
        # A failed RESTORE can still leave a database behind; always attempt cleanup.
        $created = $true
        Invoke-DeploymentSql -Query $restoreSql -DatabaseName 'master'

        Invoke-DeploymentSql -InputFile $MigrationPath -DatabaseName $tempDatabase
        Invoke-DeploymentSql -InputFile $ValidatePath -DatabaseName $tempDatabase
        Add-DeployEvent "Migration rehearsal passed in disposable database $tempDatabase."
    }
    finally {
        if ($created) {
            $escapedDb = $tempDatabase.Replace("'", "''")
            $quotedDb = $tempDatabase.Replace(']', ']]')
            $dropSql = @"
SET NOCOUNT ON;
USE [master];
IF DB_ID(N'$escapedDb') IS NOT NULL
BEGIN
    ALTER DATABASE [$quotedDb] SET SINGLE_USER WITH ROLLBACK IMMEDIATE;
    DROP DATABASE [$quotedDb];
END;
"@
            Invoke-DeploymentSql -Query $dropSql -DatabaseName 'master'
        }
        if (Test-Path -LiteralPath $rehearsalDir) {
            Remove-Item -LiteralPath $rehearsalDir -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}

function Test-RollbackHealth {
    $validationMode = ''
    if ($script:op -and $script:op.PSObject.Properties.Name -contains 'CertificateValidationMode') {
        $validationMode = [string]$script:op.CertificateValidationMode
    }
    return (Wait-HttpsHealthy -Port $script:port -PublicHostname $script:publicHostname `
        -InstallDir $script:InstallDir -CertificateMode $script:certMode `
        -CertificateValidationMode $validationMode -TimeoutSec 60)
}

function New-SanitizedDiagnosticBundle {
    param(
        [Parameter(Mandatory = $true)][string]$Failure,
        [string[]]$RollbackErrors = @()
    )
    $bundle = Join-Path ([IO.Path]::GetTempPath()) ("nyxveil-production-deploy-{0:yyyyMMdd-HHmmss}-{1}" -f (Get-Date), [guid]::NewGuid().ToString('N').Substring(0, 8))
    New-Item -ItemType Directory -Path $bundle -Force | Out-Null
    @(
        'release_version=1.3.1'
        "powershell_version=$($PSVersionTable.PSVersion)"
        "os_version=$([Environment]::OSVersion.VersionString)"
        "expected_schema_version=$script:ExpectedSchemaVersion"
        "service_name=$script:ServiceName"
        "publish_payload_sha256=$script:packageFingerprint"
        "release_zip_sha256=$script:releaseZipFingerprint"
    ) | Set-Content -LiteralPath (Join-Path $bundle 'versions.txt') -Encoding UTF8
    @(
        $script:events | ForEach-Object { ConvertTo-SanitizedText -Text $_ }
        "failure=$(ConvertTo-SanitizedText -Text $Failure)"
        $RollbackErrors | ForEach-Object { "rollback_error=$(ConvertTo-SanitizedText -Text $_)" }
    ) | Set-Content -LiteralPath (Join-Path $bundle 'deployment.log') -Encoding UTF8
    if ($script:gateLog -and (Test-Path -LiteralPath $script:gateLog)) {
        Get-Content -LiteralPath $script:gateLog | ForEach-Object {
            ConvertTo-SanitizedText -Text $_
        } | Set-Content -LiteralPath (Join-Path $bundle 'gate.log') -Encoding UTF8
    }
    else {
        'gate_not_run=true' | Set-Content -LiteralPath (Join-Path $bundle 'gate.log') -Encoding UTF8
    }
    return $bundle
}

try {
    # 1. PRECHECK: no mutation of the installed instance.
    $stage = 'precheck'
    Assert-Administrator
    if ($ServiceName -cne $requiredServiceName) {
        throw "This deploy may only operate on service '$requiredServiceName'."
    }
    if ($ExpectedSchemaVersion -cne '4') {
        throw "Control Plane 1.3.1 requires ExpectedSchemaVersion=4."
    }
    $releaseVersion = (Get-Content -LiteralPath (Join-Path $licensingRoot 'VERSION') -Raw).Trim()
    if ($releaseVersion -cne '1.3.1') {
        throw "This wrapper requires licensing VERSION 1.3.1; found '$releaseVersion'."
    }

    $PublishDir = (Resolve-Path -LiteralPath $PublishDir -ErrorAction Stop).Path
    if (-not (Test-Path -LiteralPath $PublishDir -PathType Container)) {
        throw "PublishDir is not a directory: $PublishDir"
    }
    $opPath = Get-OperationalConfigPath -InstallDir $InstallDir
    $op = Read-OperationalConfig -Path $opPath
    $originalOperationalJson = $op | ConvertTo-Json -Depth 20
    if ([string]::IsNullOrWhiteSpace($InstallDir)) {
        $InstallDir = if ($op.InstallDir) { [string]$op.InstallDir } else { 'C:\Program Files\Nyxveil\ControlPlane' }
    }
    $InstallDir = (Resolve-Path -LiteralPath $InstallDir -ErrorAction Stop).Path
    Assert-SeparateDirectoryTrees -First $PublishDir -Second $InstallDir

    $MigrationScript = if ($MigrationScript) { $MigrationScript } else {
        Join-Path $licensingRoot 'database\migrations\002_node_lifecycle_cert_metadata.sql'
    }
    $MigrationScript = (Resolve-Path -LiteralPath $MigrationScript -ErrorAction Stop).Path
    $validationScript = (Resolve-Path -LiteralPath $validationScript -ErrorAction Stop).Path

    $webExe = Join-Path $PublishDir 'Nyxveil.ControlPlane.Web.exe'
    $webDll = Join-Path $PublishDir 'Nyxveil.ControlPlane.Web.dll'
    if (-not (Test-Path -LiteralPath $webExe -PathType Leaf) -and
        -not (Test-Path -LiteralPath $webDll -PathType Leaf)) {
        throw 'PublishDir must contain Nyxveil.ControlPlane.Web.exe or Nyxveil.ControlPlane.Web.dll.'
    }
    foreach ($hostFile in @($webExe, $webDll)) {
        if ((Test-Path -LiteralPath $hostFile -PathType Leaf) -and
            (Get-Item -LiteralPath $hostFile).Length -le 0) {
            throw "Published host file is empty: $hostFile"
        }
    }

    $null = Get-Service -Name $requiredServiceName -ErrorAction Stop
    if ($op.ServiceName -and [string]$op.ServiceName -cne $requiredServiceName) {
        throw "operational.json ServiceName must be '$requiredServiceName'."
    }
    $port = if ($op.Port) { [int]$op.Port } else { 0 }
    Assert-ValidPort -Port $port
    if ($port -ne 8443) {
        throw "Emergency deploy preserves the production 8443 architecture; configured port is $port."
    }
    if (-not (Test-TcpPortAvailable -Port $port -AllowServiceName $requiredServiceName)) {
        throw "Configured port $port has a foreign listener."
    }
    $publicHostname = if ($op.PublicHostname) { [string]$op.PublicHostname } else { 'cp.nyxveil.ru' }
    $certMode = if ($op.CertificateMode) { [string]$op.CertificateMode } else { 'Store' }

    $dbSettings = Get-NyxveilDatabaseSettings -InstallDir $InstallDir
    $dbServer = [string]$dbSettings.Server
    $dbName = [string]$dbSettings.Database
    $dbAuth = [string]$dbSettings.Auth
    $dbUser = [string]$dbSettings.User
    Assert-ValidDatabaseName -DatabaseName $dbName
    Assert-SqlCmdAvailable -Context 'production backup, migration rehearsal, migration, validation, and rollback'
    if ($dbAuth -eq 'Sql') {
        $secretsDir = if ($op.SecretsDir) { [string]$op.SecretsDir } else { Join-Path (Get-ProgramDataRoot) 'secrets' }
        $sqlPasswordPath = Join-Path $secretsDir 'sql-password.dpapi'
        if (Test-Path -LiteralPath $sqlPasswordPath) {
            $plainPassword = Read-ProtectedSecret -Path $sqlPasswordPath
            try { $databasePassword = ConvertTo-SecureString $plainPassword -AsPlainText -Force }
            finally { $plainPassword = $null }
        }
        else {
            $databasePassword = Read-Host 'SQL password' -AsSecureString
        }
        if ([string]::IsNullOrWhiteSpace($dbUser)) {
            throw 'SQL Auth requires DatabaseUser.'
        }
    }

    # 2. PACKAGE VALIDATION + EXPLICIT HASHES.
    $stage = 'package_validation'
    $packageFingerprint = Get-PackageFingerprint -Path $PublishDir
    Write-Output "publish_payload_sha256=$packageFingerprint"

    if ($ReleaseZip) {
        $ReleaseZip = (Resolve-Path -LiteralPath $ReleaseZip -ErrorAction Stop).Path
    }
    else {
        $zipCandidates = @(
            (Join-Path $licensingRoot 'Nyxveil-ControlPlane-v1.3.1-release.zip'),
            (Join-Path (Split-Path -Parent $licensingRoot) 'Nyxveil-ControlPlane-v1.3.1-release.zip')
        )
        $ReleaseZip = $zipCandidates | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } | Select-Object -First 1
    }
    if ($ReleaseZip) {
        $releaseZipFingerprint = (Get-FileHash -LiteralPath $ReleaseZip -Algorithm SHA256).Hash.ToLowerInvariant()
        Write-Output "release_zip_sha256=$releaseZipFingerprint"
    }
    Add-DeployEvent "PRECHECK and package validation passed (service=$ServiceName port=$port)."

    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    $backupDir = Join-Path (Join-Path (Get-ProgramDataRoot) 'backups\production-deploy') $stamp
    New-Item -ItemType Directory -Path $backupDir -Force | Out-Null

    # 3. VERIFIED PRODUCTION DATABASE BACKUP.
    $stage = 'backup_database'
    $databaseBackup = Join-Path $backupDir ("{0}-{1}.bak" -f $dbName, $stamp)
    $backupArgs = @{ SqlServer = $dbServer; Database = $dbName; BackupPath = $databaseBackup }
    if ($dbAuth -eq 'Sql') {
        $backupArgs.UseSqlAuth = $true
        $backupArgs.SqlUser = $dbUser
        $backupArgs.SqlPassword = $databasePassword
    }
    & (Join-Path $PSScriptRoot 'backup-db.ps1') @backupArgs

    # 4. REHEARSE RESTORE + MIGRATION + VALIDATION BEFORE OUTAGE.
    $stage = 'migration_rehearsal'
    try {
        Invoke-MigrationRehearsal -BackupPath $databaseBackup -MigrationPath $MigrationScript `
            -ValidatePath $validationScript
    }
    catch {
        $failedGate = 'migration_rehearsal'
        throw
    }

    # 5. BACK UP INSTALLED BINARIES AND CONFIGURATION.
    $stage = 'backup_binaries_config'
    $binaryBackup = Join-Path $backupDir 'binaries'
    $configBackup = Join-Path $backupDir 'configuration'
    Copy-DirectoryExact -Source $InstallDir -Destination $binaryBackup
    New-Item -ItemType Directory -Path $configBackup -Force | Out-Null
    foreach ($configItem in @('appsettings.Production.json', 'config')) {
        $source = Join-Path $InstallDir $configItem
        if (Test-Path -LiteralPath $source) {
            Copy-Item -LiteralPath $source -Destination $configBackup -Recurse -Force
        }
    }
    Copy-Item -LiteralPath $opPath -Destination (Join-Path $configBackup 'operational.json') -Force

    $currentFingerprint = Get-PackageFingerprint -Path $PublishDir
    if ($currentFingerprint -cne $packageFingerprint) {
        throw "PublishDir changed after validation. expected=$packageFingerprint actual=$currentFingerprint"
    }

    $deployStarted = $true

    # 6. STOP ONLY THE CONTROL PLANE SERVICE.
    $stage = 'stop_service'
    Add-DeployEvent "Stopping only $requiredServiceName."
    Stop-Service -Name $requiredServiceName -Force -ErrorAction Stop
    (Get-Service -Name $requiredServiceName).WaitForStatus('Stopped', [TimeSpan]::FromSeconds(30))

    # 7. APPLY PRODUCTION MIGRATION.
    $stage = 'apply_production_migration'
    $productionMigrationAttempted = $true
    Invoke-DeploymentSql -InputFile $MigrationScript -DatabaseName $dbName

    # 8. VALIDATE PRODUCTION SCHEMA V2.
    $stage = 'validate_production_schema'
    Invoke-DeploymentSql -InputFile $validationScript -DatabaseName $dbName

    # 9. DEPLOY BINARIES WHILE PRESERVING PRODUCTION CONFIG.
    $stage = 'deploy_binaries'
    Clear-DirectoryContents -Path $InstallDir
    Copy-DirectoryExact -Source $PublishDir -Destination $InstallDir
    foreach ($configItem in @('appsettings.Production.json', 'config')) {
        $saved = Join-Path $configBackup $configItem
        $target = Join-Path $InstallDir $configItem
        if (Test-Path -LiteralPath $target) {
            Remove-Item -LiteralPath $target -Recurse -Force
        }
        if (Test-Path -LiteralPath $saved) {
            Copy-Item -LiteralPath $saved -Destination $InstallDir -Recurse -Force
        }
    }

    # 10. RECORD EXPECTED SCHEMA VERSION.
    $stage = 'write_operational_config'
    $op | Add-Member -NotePropertyName ExpectedSchemaVersion -NotePropertyValue '4' -Force
    Write-OperationalConfig -Config $op -InstallDir $InstallDir

    # 11. START ONLY THE CONTROL PLANE SERVICE.
    $stage = 'start_service'
    Add-DeployEvent "Starting only $requiredServiceName."
    Start-Service -Name $requiredServiceName -ErrorAction Stop
    (Get-Service -Name $requiredServiceName).WaitForStatus('Running', [TimeSpan]::FromSeconds(30))

    # 12. RUN THE PRODUCTION GATE.
    $stage = 'production_gate'
    $gateLog = Join-Path $backupDir 'production-gate.log'
    $gateErrorLog = Join-Path $backupDir 'production-gate.error.log'
    $gateArgs = @(
        '-NoProfile', '-ExecutionPolicy', 'Bypass',
        '-File', ('"{0}"' -f (Join-Path $PSScriptRoot 'production-gate.ps1')),
        '-GateMode', 'production',
        '-InstallDir', ('"{0}"' -f $InstallDir)
    )
    $gateProcess = Start-Process -FilePath 'powershell.exe' -ArgumentList $gateArgs -Wait -PassThru `
        -RedirectStandardOutput $gateLog -RedirectStandardError $gateErrorLog
    if (Test-Path -LiteralPath $gateErrorLog) {
        Get-Content -LiteralPath $gateErrorLog | Add-Content -LiteralPath $gateLog -Encoding UTF8
        Remove-Item -LiteralPath $gateErrorLog -Force
    }
    $gateOutput = if (Test-Path -LiteralPath $gateLog) { Get-Content -LiteralPath $gateLog } else { @() }
    $gateOutput | ForEach-Object { Write-Host "gate: $_" }
    $gateFailureLine = $gateOutput | Where-Object { $_ -like 'failed_gate=*' } | Select-Object -First 1
    if ($gateFailureLine) { $failedGate = $gateFailureLine.Substring('failed_gate='.Length) }
    if ($gateProcess.ExitCode -ne 0) {
        if (-not $failedGate) { $failedGate = 'production_gate' }
        throw "Production gate failed with exit code $($gateProcess.ExitCode)."
    }

    # 13. COMMIT.
    Add-DeployEvent "Deployment committed; verified backup retained at $backupDir."
    Write-Output 'RESULT=PASS'
}
catch {
    $failure = $_.Exception.Message
    $rollbackErrors = [Collections.Generic.List[string]]::new()
    Add-DeployEvent "Deployment failed at $stage."

    if ($deployStarted) {
        $rollbackBinaries = $false
        $rollbackConfig = $false
        $rollbackDatabase = (-not $productionMigrationAttempted)
        $rollbackService = $false
        $rollbackHealth = $false

        if ($productionMigrationAttempted) {
            try {
                $restoreArgs = @{
                    BackupPath = $databaseBackup
                    SqlServer = $dbServer
                    Database = $dbName
                    AutomatedRollback = $true
                    Force = $true
                    ConfirmDatabaseName = $dbName
                    StopService = $true
                }
                if ($dbAuth -eq 'Sql') {
                    $restoreArgs.UseSqlAuth = $true
                    $restoreArgs.SqlUser = $dbUser
                    $restoreArgs.SqlPassword = $databasePassword
                }
                & (Join-Path $PSScriptRoot 'restore-db.ps1') @restoreArgs
                $rollbackDatabase = $true
            }
            catch {
                $rollbackErrors.Add("database_restore: $($_.Exception.Message)")
            }
        }

        try {
            Stop-Service -Name $requiredServiceName -Force -ErrorAction SilentlyContinue
            (Get-Service -Name $requiredServiceName).WaitForStatus('Stopped', [TimeSpan]::FromSeconds(30))
            Clear-DirectoryContents -Path $InstallDir
            Copy-DirectoryExact -Source $binaryBackup -Destination $InstallDir
            $rollbackBinaries = $true
        }
        catch {
            $rollbackErrors.Add("binary_restore: $($_.Exception.Message)")
        }

        try {
            foreach ($configItem in @('appsettings.Production.json', 'config')) {
                $saved = Join-Path $configBackup $configItem
                $target = Join-Path $InstallDir $configItem
                if (Test-Path -LiteralPath $target) {
                    Remove-Item -LiteralPath $target -Recurse -Force
                }
                if (Test-Path -LiteralPath $saved) {
                    Copy-Item -LiteralPath $saved -Destination $InstallDir -Recurse -Force
                }
            }
            if ($originalOperationalJson) {
                Write-OperationalConfig -Config ($originalOperationalJson | ConvertFrom-Json) -InstallDir $InstallDir
            }
            $rollbackConfig = $true
        }
        catch {
            $rollbackErrors.Add("config_restore: $($_.Exception.Message)")
        }

        try {
            Start-Service -Name $requiredServiceName -ErrorAction Stop
            (Get-Service -Name $requiredServiceName).WaitForStatus('Running', [TimeSpan]::FromSeconds(30))
            $rollbackService = $true
        }
        catch {
            $rollbackErrors.Add("service_restart: $($_.Exception.Message)")
        }

        if ($rollbackService) {
            try {
                if (-not (Test-RollbackHealth)) { throw 'Rollback health verification failed.' }
                $rollbackHealth = $true
            }
            catch {
                $rollbackErrors.Add("rollback_health: $($_.Exception.Message)")
            }
        }

        $rollbackComplete = $rollbackBinaries -and $rollbackConfig -and $rollbackDatabase -and
            $rollbackService -and $rollbackHealth
        Write-Output 'rollback:'
        Write-Output "  binaries=$($rollbackBinaries.ToString().ToLowerInvariant())"
        Write-Output "  config=$($rollbackConfig.ToString().ToLowerInvariant())"
        Write-Output "  database=$($rollbackDatabase.ToString().ToLowerInvariant())"
        Write-Output "  service=$($rollbackService.ToString().ToLowerInvariant())"
        Write-Output "  health=$($rollbackHealth.ToString().ToLowerInvariant())"
        Write-Output "rollback_complete=$($rollbackComplete.ToString().ToLowerInvariant())"
    }

    try {
        $diagnosticBundle = New-SanitizedDiagnosticBundle -Failure $failure -RollbackErrors $rollbackErrors.ToArray()
    }
    catch {
        $diagnosticBundle = 'unavailable'
        $rollbackErrors.Add("diagnostic_bundle: $($_.Exception.Message)")
    }
    if (-not $failedGate) { $failedGate = $stage }
    Write-Output 'RESULT=FAIL'
    Write-Output "failed_gate=$failedGate"
    Write-Output "diagnostic_bundle=$diagnosticBundle"
    foreach ($rollbackError in $rollbackErrors) {
        Write-Warning (ConvertTo-SanitizedText -Text $rollbackError)
    }
    exit 1
}
