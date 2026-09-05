#Requires -Version 5.1
#Requires -RunAsAdministrator
<#
.SYNOPSIS
  Uninstall Nyxveil Control Plane Windows Service, firewall rule, and binaries.

.DESCRIPTION
  By default does NOT remove the database or ProgramData secrets/keys.
  Pass -PurgeData to also remove ProgramData ControlPlane data (secrets, keys, logs, operational.json).
  Database drop is never performed by this script.
#>
[CmdletBinding()]
param(
    [string]$ServiceName = '',
    [string]$InstallDir = '',
    [switch]$PurgeData,
    [switch]$Force
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force

Assert-Administrator

$op = $null
try { $op = Read-OperationalConfig } catch { }

if ([string]::IsNullOrWhiteSpace($ServiceName)) {
    $ServiceName = if ($op -and $op.ServiceName) { [string]$op.ServiceName } else { 'NyxveilControlPlane' }
}
if ([string]::IsNullOrWhiteSpace($InstallDir)) {
    $InstallDir = if ($op -and $op.InstallDir) { [string]$op.InstallDir } else { 'C:\Program Files\Nyxveil\ControlPlane' }
}

$fw = if ($op -and $op.FirewallRuleName) { [string]$op.FirewallRuleName } else { '' }
$port = if ($op -and $op.Port) { [int]$op.Port } else { 0 }

$msg = "Uninstall service='$ServiceName' installDir='$InstallDir' PurgeData=$PurgeData. Type YES to confirm"
if (-not $Force) {
    $confirm = Read-Host $msg
    if ($confirm -ne 'YES') { throw 'Aborted (confirmation not YES).' }
}

Write-Host "Stopping/removing service $ServiceName ..."
$svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($svc) {
    Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
    & sc.exe delete $ServiceName | Out-Null
    Start-Sleep -Seconds 2
}

if ($fw) {
    try {
        Remove-NyxveilFirewallRule -RuleName $fw
        Write-Host "Removed firewall rule $fw"
    }
    catch { Write-Warning $_.Exception.Message }
}
elseif ($port -gt 0) {
    $autoName = Get-NyxveilFirewallRuleName -Port $port
    try {
        Remove-NyxveilFirewallRule -RuleName $autoName
        Write-Host "Removed firewall rule $autoName"
    }
    catch { Write-Verbose $_.Exception.Message }
}

if ($InstallDir -and (Test-Path -LiteralPath $InstallDir)) {
    Write-Host "Removing binaries under $InstallDir ..."
    Remove-Item -LiteralPath $InstallDir -Recurse -Force -ErrorAction Stop
}

if ($PurgeData) {
    $pd = Get-ProgramDataRoot
    Write-Host "PurgeData: removing $pd (secrets/keys/logs/operational). Database is NOT dropped."
    if (Test-Path -LiteralPath $pd) {
        Remove-Item -LiteralPath $pd -Recurse -Force -ErrorAction Stop
    }
}
else {
    Write-Host 'ProgramData secrets/keys/logs retained. Database retained. Use -PurgeData to remove ProgramData only.'
}

Write-Host 'Uninstall complete.'
