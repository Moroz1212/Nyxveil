#Requires -Version 5.1
#Requires -RunAsAdministrator
<#
.SYNOPSIS
  Export unified portable recovery bundle (signing keys + license KEK) via Web CLI.

.DESCRIPTION
  Invokes: Nyxveil.ControlPlane.Web.exe backup-recovery --output <path>
  Password is supplied on stdin (SecureString). Does not call backup-signing-keys.ps1.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$OutputPath,
    [string]$InstallDir = '',
    [securestring]$Password,
    [switch]$Force
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force

Assert-Administrator

if (-not $InstallDir) {
    try { $InstallDir = [string](Read-OperationalConfig).InstallDir } catch { $InstallDir = 'C:\Program Files\Nyxveil\ControlPlane' }
}

$confirm = Read-Host "Export unified recovery bundle to '$OutputPath'? Type YES to confirm"
if ($confirm -ne 'YES') {
    throw 'Aborted (confirmation not YES).'
}

if ((Test-Path -LiteralPath $OutputPath) -and -not $Force) {
    throw "Output exists: $OutputPath (pass -Force to overwrite)."
}

if (-not $Password) {
    $Password = Read-Host 'Backup password' -AsSecureString
    $confirmPw = Read-Host 'Confirm password' -AsSecureString
    $a = ConvertFrom-SecureStringPlain -SecureString $Password
    $b = ConvertFrom-SecureStringPlain -SecureString $confirmPw
    if ($a -cne $b) { throw 'Passwords do not match.' }
    $a = $null; $b = $null
}

$dir = Split-Path $OutputPath -Parent
if ($dir -and -not (Test-Path $dir)) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
}

$exe = Get-NyxveilWebExePath -InstallDir $InstallDir
Write-Host "Invoking: $exe backup-recovery --output $OutputPath"
$result = Invoke-NyxveilWebCli -InstallDir $InstallDir `
    -Arguments @('backup-recovery', '--output', $OutputPath) `
    -StdinSecure $Password

if ($result.ExitCode -ne 0 -or -not (Test-Path -LiteralPath $OutputPath)) {
    $combined = ("{0}`n{1}" -f $result.StdOut, $result.StdErr).Trim()
    throw "backup-recovery failed (exit $($result.ExitCode)). $combined"
}

if ($result.StdOut) { Write-Host ($result.StdOut.TrimEnd()) }
$len = (Get-Item -LiteralPath $OutputPath).Length
if ($len -le 0) { throw "Backup file is empty: $OutputPath" }
Write-Host "Unified recovery bundle written ($len bytes): $OutputPath"
exit 0
