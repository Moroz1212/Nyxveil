#Requires -Version 5.1
<#
.SYNOPSIS
  Privileged Control Plane self-update apply step.
  Invoked only by NyxveilControlPlaneUpdater (LocalSystem), never by the Web service identity.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$HandoffPath
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if (-not (Test-Path -LiteralPath $HandoffPath)) { throw "handoff missing: $HandoffPath" }
$handoff = Get-Content -LiteralPath $HandoffPath -Raw | ConvertFrom-Json
$staging = [string]$handoff.stagingPath
$installDir = [string]$handoff.installDir
$serviceName = if ($handoff.serviceName) { [string]$handoff.serviceName } else { 'NyxveilControlPlane' }
$backupPath = [string]$handoff.backupPath
$targetVersion = [string]$handoff.targetVersion
$currentVersion = [string]$handoff.currentVersion
$transactionId = if ($handoff.transactionId) { [string]$handoff.transactionId } else { '' }
$resultPath = Join-Path (Split-Path -Parent $HandoffPath) 'result.json'

$canonicalInstall = Join-Path ${env:ProgramFiles} 'Nyxveil\ControlPlane'
$pdRoot = Join-Path $env:ProgramData 'Nyxveil\ControlPlane'
$selfUpdateRoot = Join-Path $pdRoot 'self-update'

function Test-Under([string]$Candidate, [string]$Root) {
    $c = [IO.Path]::GetFullPath($Candidate).TrimEnd('\', '/')
    $r = [IO.Path]::GetFullPath($Root).TrimEnd('\', '/') + '\'
    return ($c + '\').StartsWith($r, [StringComparison]::OrdinalIgnoreCase)
}

if ($serviceName -cne 'NyxveilControlPlane') {
    throw "serviceName must be NyxveilControlPlane"
}
$installFull = [IO.Path]::GetFullPath($installDir).TrimEnd('\', '/')
$canonFull = [IO.Path]::GetFullPath($canonicalInstall).TrimEnd('\', '/')
if (-not $installFull.Equals($canonFull, [StringComparison]::OrdinalIgnoreCase)) {
    throw "installDir is not canonical Control Plane path"
}
if (-not (Test-Under -Candidate $staging -Root (Join-Path $selfUpdateRoot 'staging')) -and
    -not (Test-Under -Candidate $staging -Root $selfUpdateRoot)) {
    throw "stagingPath escapes self-update root"
}
if (-not (Test-Under -Candidate $backupPath -Root (Join-Path $selfUpdateRoot 'backups')) -and
    -not (Test-Under -Candidate $backupPath -Root $selfUpdateRoot)) {
    throw "backupPath escapes self-update root"
}

$module = Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1'
if (Test-Path -LiteralPath $module) {
    Import-Module $module -Force
}

$script:MutableStarted = $false
$script:InstallBackup = $null
$script:PrimaryFailure = $null

function Write-Result {
    param(
        [string]$Code,
        [string]$Version,
        [string]$Message = '',
        [string]$PrimaryFailure = '',
        [bool]$RollbackAttempted = $false,
        [object]$RollbackSucceeded = $null,
        [string]$RollbackFailure = ''
    )
    $obj = [ordered]@{
        resultCode         = $Code
        version            = $Version
        message            = $Message
        transactionId      = $transactionId
        primaryFailure     = $PrimaryFailure
        rollbackAttempted  = $RollbackAttempted
        at                 = (Get-Date).ToUniversalTime().ToString('o')
    }
    if ($null -ne $RollbackSucceeded) { $obj['rollbackSucceeded'] = [bool]$RollbackSucceeded }
    if ($RollbackFailure) { $obj['rollbackFailure'] = $RollbackFailure }
    ($obj | ConvertTo-Json -Compress) | Set-Content -LiteralPath $resultPath -Encoding UTF8
}

function Backup-Install([string]$Src, [string]$Dst) {
    if (Test-Path -LiteralPath $Dst) { Remove-Item -LiteralPath $Dst -Recurse -Force }
    New-Item -ItemType Directory -Path $Dst -Force | Out-Null
    if (Get-Command Backup-DirectoryContents -ErrorAction SilentlyContinue) {
        Backup-DirectoryContents -SourceDir $Src -DestinationDir $Dst
    }
    else {
        Copy-Item -Path (Join-Path $Src '*') -Destination $Dst -Recurse -Force
    }
}

function Copy-Staging([string]$Src, [string]$Dst) {
    $preserve = @('appsettings.Production.json')
    Get-ChildItem -LiteralPath $Src -Recurse -File | ForEach-Object {
        $rel = $_.FullName.Substring($Src.Length).TrimStart('\', '/')
        if ($rel -like 'config\*') { return }
        if ($rel -like 'appsettings.Development.json') { return }
        if ($preserve -contains $_.Name -and (Test-Path -LiteralPath (Join-Path $Dst $rel))) { return }
        $target = Join-Path $Dst $rel
        $parent = Split-Path -Parent $target
        if (-not (Test-Path -LiteralPath $parent)) { New-Item -ItemType Directory -Path $parent -Force | Out-Null }
        Copy-Item -LiteralPath $_.FullName -Destination $target -Force
    }
    $versionCandidates = @(
        (Join-Path (Split-Path -Parent $Src) 'VERSION'),
        (Join-Path $Src 'VERSION')
    )
    foreach ($v in $versionCandidates) {
        if (Test-Path -LiteralPath $v) {
            Copy-Item -LiteralPath $v -Destination (Join-Path $Dst 'VERSION') -Force
            break
        }
    }
}

function Invoke-Rollback([string]$Reason) {
    $rollbackAttempted = $true
    $rollbackSucceeded = $false
    $rollbackFailure = ''
    try {
        if ($script:InstallBackup -and (Test-Path -LiteralPath $script:InstallBackup)) {
            if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
                Stop-Service -Name $serviceName -Force -ErrorAction SilentlyContinue
                Start-Sleep -Seconds 2
            }
            Get-ChildItem -LiteralPath $installDir -Force -ErrorAction SilentlyContinue | ForEach-Object {
                Remove-Item -LiteralPath $_.FullName -Recurse -Force -ErrorAction SilentlyContinue
            }
            Copy-Item -Path (Join-Path $script:InstallBackup '*') -Destination $installDir -Recurse -Force
            if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
                Start-Service -Name $serviceName -ErrorAction Stop
                Start-Sleep -Seconds 5
            }
            $rollbackSucceeded = $true
        }
        else {
            $rollbackFailure = 'no install backup available'
        }
    }
    catch {
        $rollbackFailure = $_.Exception.Message
        $rollbackSucceeded = $false
    }

    if ($rollbackSucceeded) {
        Write-Result -Code 'rolled_back_healthy' -Version $currentVersion -Message $Reason `
            -PrimaryFailure $script:PrimaryFailure -RollbackAttempted $true -RollbackSucceeded $true
        return 11
    }

    Write-Result -Code 'rollback_failed' -Version $currentVersion -Message $Reason `
        -PrimaryFailure $script:PrimaryFailure -RollbackAttempted $true -RollbackSucceeded $false `
        -RollbackFailure $rollbackFailure
    return 12
}

try {
    if (-not (Test-Path -LiteralPath $staging)) { throw "staging missing: $staging" }
    if (-not (Test-Path -LiteralPath $installDir)) { throw "installDir missing: $installDir" }
    New-Item -ItemType Directory -Path $backupPath -Force | Out-Null
    $script:InstallBackup = Join-Path $backupPath 'install'
    Backup-Install -Src $installDir -Dst $script:InstallBackup

    $backupDb = Join-Path $PSScriptRoot 'backup-db.ps1'
    if ((Test-Path -LiteralPath $backupDb) -and (Get-Command sqlcmd -ErrorAction SilentlyContinue)) {
        try {
            & $backupDb -BackupPath (Join-Path $backupPath 'db.bak') | Out-Null
        }
        catch {
            Write-Warning "DB backup skipped/failed: $($_.Exception.Message)"
        }
    }

    $script:MutableStarted = $true
    if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
        Stop-Service -Name $serviceName -Force -ErrorAction SilentlyContinue
        Start-Sleep -Seconds 3
    }

    Copy-Staging -Src $staging -Dst $installDir

    if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
        Start-Service -Name $serviceName
        Start-Sleep -Seconds 8
    }

    $installed = ''
    $versionFile = Join-Path $installDir 'VERSION'
    if (Test-Path -LiteralPath $versionFile) { $installed = (Get-Content -LiteralPath $versionFile -Raw).Trim() }

    if ($installed -cne $targetVersion) {
        $script:PrimaryFailure = "version mismatch installed=$installed target=$targetVersion"
        exit (Invoke-Rollback -Reason $script:PrimaryFailure)
    }

    $healthy = $true
    if (Get-Command Wait-HttpsHealthy -ErrorAction SilentlyContinue) {
        try {
            $op = if (Get-Command Read-OperationalConfig -ErrorAction SilentlyContinue) { Read-OperationalConfig } else { $null }
            $port = if ($op -and $op.Port) { [int]$op.Port } else { 8443 }
            $hn = if ($op -and $op.PublicHostname) { [string]$op.PublicHostname } else { 'localhost' }
            Wait-HttpsHealthy -Hostname $hn -Port $port -TimeoutSeconds 60 | Out-Null
        }
        catch {
            $healthy = $false
            $script:PrimaryFailure = $_.Exception.Message
        }
    }

    if (-not $healthy) {
        if (-not $script:PrimaryFailure) { $script:PrimaryFailure = 'health failed' }
        exit (Invoke-Rollback -Reason $script:PrimaryFailure)
    }

    Write-Result -Code 'updated_healthy' -Version $targetVersion -Message ''
    exit 0
}
catch {
    $script:PrimaryFailure = $_.Exception.Message
    if ($script:MutableStarted) {
        exit (Invoke-Rollback -Reason $script:PrimaryFailure)
    }
    Write-Result -Code 'rollback_failed' -Version $currentVersion -Message $script:PrimaryFailure `
        -PrimaryFailure $script:PrimaryFailure -RollbackAttempted $false -RollbackSucceeded $false `
        -RollbackFailure 'mutable phase not started; nothing to roll back'
    throw
}
