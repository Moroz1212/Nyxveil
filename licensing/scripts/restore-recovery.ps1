#Requires -Version 5.1
#Requires -RunAsAdministrator
<#
.SYNOPSIS
  Restore unified portable recovery bundle (signing keys + license KEK) via Web CLI.

.DESCRIPTION
  Invokes: Nyxveil.ControlPlane.Web.exe restore-recovery --input <path> [--force]
  Password is supplied on stdin (SecureString). Does not call restore-signing-keys.ps1.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$InputPath,
    [string]$InstallDir = '',
    [securestring]$Password,
    [switch]$Force
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force

Assert-Administrator

if (-not (Test-Path -LiteralPath $InputPath)) {
    throw "Backup file not found: $InputPath"
}
if (-not $InstallDir) {
    try { $InstallDir = [string](Read-OperationalConfig).InstallDir } catch { $InstallDir = 'C:\Program Files\Nyxveil\ControlPlane' }
}

$confirm = Read-Host "Restore unified recovery bundle from '$InputPath'? Type YES to confirm"
if ($confirm -ne 'YES') {
    throw 'Aborted (confirmation not YES).'
}

if (-not $Password) {
    $Password = Read-Host 'Backup password' -AsSecureString
}

$cliArgs = [System.Collections.Generic.List[string]]@(
    'restore-recovery', '--input', $InputPath
)
if ($Force) { [void]$cliArgs.Add('--force') }

$exe = Get-NyxveilWebExePath -InstallDir $InstallDir
Write-Host "Invoking: $exe $($cliArgs -join ' ')"
$result = Invoke-NyxveilWebCli -InstallDir $InstallDir `
    -Arguments @($cliArgs.ToArray()) `
    -StdinSecure $Password

if ($result.ExitCode -ne 0) {
    $combined = ("{0}`n{1}" -f $result.StdOut, $result.StdErr).Trim()
    throw "restore-recovery failed (exit $($result.ExitCode)). $combined"
}

if ($result.StdOut) { Write-Host ($result.StdOut.TrimEnd()) }
Write-Host 'Unified recovery bundle restored via CLI.'
exit 0
