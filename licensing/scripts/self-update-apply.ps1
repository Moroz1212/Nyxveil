#Requires -Version 5.1
<#
.SYNOPSIS
  External Control Plane self-update apply step. Invoked by Nyxveil.ControlPlane.Updater.
  Reuses Nyxveil.ControlPlane.Deploy.psm1 patterns: backup, stop, copy publish (preserve config), start, health.
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
$resultPath = Join-Path (Split-Path -Parent $HandoffPath) 'result.json'

$module = Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1'
if (Test-Path -LiteralPath $module) {
    Import-Module $module -Force
}

function Write-Result([string]$code, [string]$version, [string]$message = '') {
    @{
        resultCode = $code
        version    = $version
        message    = $message
        at         = (Get-Date).ToUniversalTime().ToString('o')
    } | ConvertTo-Json | Set-Content -LiteralPath $resultPath -Encoding UTF8
}

function Backup-Install([string]$src, [string]$dst) {
    if (Test-Path -LiteralPath $dst) { Remove-Item -LiteralPath $dst -Recurse -Force }
    New-Item -ItemType Directory -Path $dst -Force | Out-Null
    if (Get-Command Backup-DirectoryContents -ErrorAction SilentlyContinue) {
        Backup-DirectoryContents -Source $src -Destination $dst
    } else {
        Copy-Item -Path (Join-Path $src '*') -Destination $dst -Recurse -Force
    }
}

function Copy-Staging([string]$src, [string]$dst) {
    $preserve = @('appsettings.Production.json')
    Get-ChildItem -LiteralPath $src -Recurse -File | ForEach-Object {
        $rel = $_.FullName.Substring($src.Length).TrimStart('\', '/')
        if ($rel -like 'config\*') { return }
        if ($preserve -contains $_.Name -and (Test-Path -LiteralPath (Join-Path $dst $rel))) { return }
        $target = Join-Path $dst $rel
        $parent = Split-Path -Parent $target
        if (-not (Test-Path -LiteralPath $parent)) { New-Item -ItemType Directory -Path $parent -Force | Out-Null }
        Copy-Item -LiteralPath $_.FullName -Destination $target -Force
    }
    $versionCandidates = @(
        (Join-Path (Split-Path -Parent $src) 'VERSION'),
        (Join-Path $src 'VERSION')
    )
    foreach ($v in $versionCandidates) {
        if (Test-Path -LiteralPath $v) {
            Copy-Item -LiteralPath $v -Destination (Join-Path $dst 'VERSION') -Force
            break
        }
    }
}

try {
    if (-not (Test-Path -LiteralPath $staging)) { throw "staging missing: $staging" }
    if (-not (Test-Path -LiteralPath $installDir)) { throw "installDir missing: $installDir" }
    New-Item -ItemType Directory -Path $backupPath -Force | Out-Null
    $installBackup = Join-Path $backupPath 'install'
    Backup-Install -src $installDir -dst $installBackup

    # Optional DB backup via existing script when available.
    $backupDb = Join-Path $PSScriptRoot 'backup-db.ps1'
    if ((Test-Path -LiteralPath $backupDb) -and (Get-Command sqlcmd -ErrorAction SilentlyContinue)) {
        try {
            & $backupDb -BackupPath (Join-Path $backupPath 'db.bak') | Out-Null
        } catch {
            Write-Warning "DB backup skipped/failed: $($_.Exception.Message)"
        }
    }

    if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
        Stop-Service -Name $serviceName -Force -ErrorAction SilentlyContinue
        Start-Sleep -Seconds 3
    }

    Copy-Staging -src $staging -dst $installDir

    if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
        Start-Service -Name $serviceName
        Start-Sleep -Seconds 8
    }

    $installed = ''
    $versionFile = Join-Path $installDir 'VERSION'
    if (Test-Path -LiteralPath $versionFile) { $installed = (Get-Content -LiteralPath $versionFile -Raw).Trim() }

    if ($installed -cne $targetVersion) {
        Write-Warning "version mismatch installed=$installed target=$targetVersion; rolling back"
        if (Test-Path -LiteralPath $installBackup) {
            Copy-Item -Path (Join-Path $installBackup '*') -Destination $installDir -Recurse -Force
        }
        if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
            Start-Service -Name $serviceName -ErrorAction SilentlyContinue
        }
        Write-Result -code 'rolled_back_healthy' -version $currentVersion -message 'target version mismatch'
        exit 10
    }

    # Health: prefer Wait-HttpsHealthy when module present.
    $healthy = $true
    if (Get-Command Wait-HttpsHealthy -ErrorAction SilentlyContinue) {
        try {
            $op = if (Get-Command Read-OperationalConfig -ErrorAction SilentlyContinue) { Read-OperationalConfig } else { $null }
            $port = if ($op -and $op.Port) { [int]$op.Port } else { 8443 }
            $hn = if ($op -and $op.PublicHostname) { [string]$op.PublicHostname } else { 'localhost' }
            Wait-HttpsHealthy -Hostname $hn -Port $port -TimeoutSeconds 60 | Out-Null
        } catch {
            $healthy = $false
        }
    }

    if (-not $healthy) {
        if (Test-Path -LiteralPath $installBackup) {
            if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
                Stop-Service -Name $serviceName -Force -ErrorAction SilentlyContinue
            }
            Copy-Item -Path (Join-Path $installBackup '*') -Destination $installDir -Recurse -Force
            if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
                Start-Service -Name $serviceName -ErrorAction SilentlyContinue
            }
        }
        Write-Result -code 'rolled_back_healthy' -version $currentVersion -message 'health failed'
        exit 11
    }

    Write-Result -code 'updated_healthy' -version $targetVersion
    exit 0
}
catch {
    try {
        Write-Result -code 'rollback_failed' -version $currentVersion -message $_.Exception.Message
    } catch { }
    throw
}
