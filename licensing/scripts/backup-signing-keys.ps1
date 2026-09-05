#Requires -Version 5.1
#Requires -RunAsAdministrator
<#
.SYNOPSIS
  Export signing keys via published Web exe (portable encrypted backup).

.DESCRIPTION
  Prefers: Nyxveil.ControlPlane.Web.exe backup-signing-keys export
  Falls back to backup-recovery when present.
  Does not zip an empty keys folder as the primary strategy.
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

$confirm = Read-Host "Export signing-key recovery bundle to '$OutputPath'? Type YES to confirm"
if ($confirm -ne 'YES') {
    throw 'Aborted (confirmation not YES).'
}

if ((Test-Path $OutputPath) -and -not $Force) {
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
$keysDir = Join-Path (Get-ProgramDataRoot) 'keys'
$keyCount = 0
if (Test-Path $keysDir) {
    $keyCount = @(Get-ChildItem -LiteralPath $keysDir -Force -File -ErrorAction SilentlyContinue).Count
}
if ($keyCount -le 0) {
    Write-Warning "Keys directory empty or missing ($keysDir). Relying on CLI portable export (not an empty zip)."
}

$attempts = @(
    @{ Args = @('backup-signing-keys', 'export', '--output', $OutputPath); Label = 'backup-signing-keys export' },
    @{ Args = @('backup-recovery', '--output', $OutputPath); Label = 'backup-recovery' }
)

$lastErr = ''
foreach ($attempt in $attempts) {
    Write-Host "Trying CLI: $($attempt.Label) ..."
    $result = Invoke-NyxveilWebCli -InstallDir $InstallDir -Arguments $attempt.Args -StdinSecure $Password
    if ($result.ExitCode -eq 0 -and (Test-Path -LiteralPath $OutputPath)) {
        if ($result.StdOut) { Write-Host ($result.StdOut.TrimEnd()) }
        $len = (Get-Item -LiteralPath $OutputPath).Length
        if ($len -le 0) { throw "Backup file is empty: $OutputPath" }
        Write-Host "Signing-key recovery bundle written ($len bytes): $OutputPath"
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

throw "Unable to export signing keys via CLI on $exe. Last error: $lastErr"
