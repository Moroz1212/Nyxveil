#Requires -Version 5.1
#Requires -RunAsAdministrator
<#
.SYNOPSIS
  Restore signing keys via published Web exe (portable encrypted backup).
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

$confirm = Read-Host "Restore signing-key recovery bundle from '$InputPath'? Type YES to confirm"
if ($confirm -ne 'YES') {
    throw 'Aborted (confirmation not YES).'
}

$keysDir = Join-Path (Get-ProgramDataRoot) 'keys'
if ((Test-Path $keysDir) -and (Get-ChildItem $keysDir -Force -ErrorAction SilentlyContinue | Measure-Object).Count -gt 0 -and -not $Force) {
    throw "Keys directory is not empty: $keysDir (pass -Force to overwrite via CLI import)."
}

if (-not $Password) {
    $Password = Read-Host 'Backup password' -AsSecureString
}

$exe = Get-NyxveilWebExePath -InstallDir $InstallDir
$attempts = @(
    @{ Args = @('backup-signing-keys', 'import', '--input', $InputPath); Label = 'backup-signing-keys import' },
    @{ Args = @('restore-recovery', '--input', $InputPath); Label = 'restore-recovery' }
)

$lastErr = ''
foreach ($attempt in $attempts) {
    Write-Host "Trying CLI: $($attempt.Label) ..."
    $result = Invoke-NyxveilWebCli -InstallDir $InstallDir -Arguments $attempt.Args -StdinSecure $Password
    if ($result.ExitCode -eq 0) {
        if ($result.StdOut) { Write-Host ($result.StdOut.TrimEnd()) }
        Write-Host 'Signing keys restored via CLI.'
        exit 0
    }
    $combined = ("{0}`n{1}" -f $result.StdOut, $result.StdErr).Trim()
    $lastErr = $combined
    if ($combined -match '(?i)unknown|usage:|unrecognized') {
        Write-Verbose "CLI not supported: $($attempt.Label)"
        continue
    }
    if ($combined) { Write-Host $combined }
}

throw "Unable to restore signing keys via CLI on $exe. Last error: $lastErr"
