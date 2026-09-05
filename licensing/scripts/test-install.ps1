#Requires -Version 5.1
<#
.SYNOPSIS
  Documented harness for exercising Control Plane install/update flows on a TEST VM.

.DESCRIPTION
  Intended for non-production lab VMs only. Skips gracefully when not elevated or when
  SQL Server / sqlcmd / publish artifacts are unavailable.

  Does NOT use a production database name by default (uses NyxveilControlPlane_Lab_*).

.EXAMPLE
  # On an elevated test VM with SQL + .NET 10 ASP.NET Runtime + published build:
  .\test-install.ps1 -PublishDir C:\build\artifacts\publish -PublicHostname lab.local -GenerateSelfSignedCertificate
#>
[CmdletBinding()]
param(
    [string]$PublishDir = '',
    [string]$PublicHostname = 'localhost',
    [int]$Port = 0,
    [string]$InstallDir = 'C:\Program Files\Nyxveil\ControlPlane.Lab',
    [string]$DatabaseServer = 'localhost',
    [string]$Database = '',
    [switch]$GenerateSelfSignedCertificate,
    [string]$AdminUser = 'lab-admin@localhost',
    [securestring]$AdminPassword,
    [switch]$SkipUpdateSimulation
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force

Write-Host '=== Nyxveil Control Plane test-install harness (TEST VM only) ==='

try {
    Assert-Administrator
}
catch {
    Write-Warning $_.Exception.Message
    Write-Warning 'Not elevated — skipping test-install.ps1.'
    exit 0
}

if (-not (Test-SqlCmdAvailable)) {
    Write-Warning 'sqlcmd missing — skipping test-install.ps1.'
    exit 0
}

try {
    Assert-DotNetAspNetRuntime -Major 10
}
catch {
    Write-Warning $_.Exception.Message
    Write-Warning 'Skipping test-install.ps1.'
    exit 0
}

try {
    $PublishDir = Get-PublishedBuildDir -PublishDir $PublishDir -NonInteractive
}
catch {
    Write-Warning $_.Exception.Message
    Write-Warning 'Skipping test-install.ps1 (no publish directory).'
    exit 0
}

if ([string]::IsNullOrWhiteSpace($Database)) {
    $Database = 'NyxveilControlPlane_Lab_{0}' -f (Get-Date -Format 'yyyyMMddHHmmss')
}
Write-Host "Lab database name: $Database (not production)."

if ($Port -le 0) {
    # Find a free high port
    $Port = 19443
    while ($Port -lt 20000 -and -not (Test-TcpPortAvailable -Port $Port -BindAddress '127.0.0.1')) {
        $Port++
    }
    if ($Port -ge 20000) { throw 'Could not find a free lab port.' }
}

if (-not $AdminPassword) {
    # Deterministic lab password (SecureString); still not echoed
    $AdminPassword = ConvertTo-SecureString 'LabOnly!ChangeMe-Nyxveil1' -AsPlainText -Force
}

if (-not $GenerateSelfSignedCertificate) {
    $GenerateSelfSignedCertificate = $true
    Write-Warning 'Using self-signed certificate for lab install.'
}

Write-Host "Running install-windows.ps1 (Port=$Port, InstallDir=$InstallDir)..."
& (Join-Path $PSScriptRoot 'install-windows.ps1') `
    -Port $Port `
    -BindAddress '127.0.0.1' `
    -PublicHostname $PublicHostname `
    -GenerateSelfSignedCertificate:$GenerateSelfSignedCertificate `
    -DatabaseServer $DatabaseServer `
    -Database $Database `
    -DatabaseAuth Windows `
    -PublishDir $PublishDir `
    -InstallDir $InstallDir `
    -NonInteractive `
    -AdminUser $AdminUser `
    -AdminPassword $AdminPassword `
    -ServiceAccount 'NT SERVICE\NyxveilControlPlane'

Write-Host 'Running self-test.ps1...'
& (Join-Path $PSScriptRoot 'self-test.ps1')
if ($LASTEXITCODE -ne 0) { throw 'self-test failed after install.' }

Write-Host 'Service restart check...'
$op = Read-OperationalConfig
Restart-Service -Name $op.ServiceName -Force
$hn = if ($op.PublicHostname) { [string]$op.PublicHostname } else { 'localhost' }
$cm = if ($op.CertificateMode) { [string]$op.CertificateMode } else { 'Store' }
if (-not (Wait-HttpsHealthy -Port ([int]$op.Port) -PublicHostname $hn -InstallDir ([string]$op.InstallDir) `
        -CertificateMode $cm -TimeoutSec 60)) {
    throw 'Health failed after service restart.'
}
Write-Host '[PASS] service restart + health'

Write-Host 'Port availability / configured port check...'
if (-not (Test-TcpPortAvailable -Port ([int]$op.Port + 1) -BindAddress '127.0.0.1' -AllowServiceName ([string]$op.ServiceName))) {
    Write-Warning "Adjacent port $($op.Port + 1) occupied (informational)."
}
$listeners = Get-PortOccupancyDetails -Port ([int]$op.Port)
if (-not $listeners) { throw "Configured port $($op.Port) not listening." }
Write-Host '[PASS] configured port listening'

Write-Host 'Database access probe via test-database style verification already covered by self-test.'

if (-not $SkipUpdateSimulation) {
    Write-Host 'Update simulation (redeploy same publish)...'
    & (Join-Path $PSScriptRoot 'update-windows.ps1') -PublishDir $PublishDir -InstallDir $InstallDir
    Write-Host '[PASS] update simulation'
}

Write-Host ''
Write-Host 'test-install harness completed.'
Write-Host "Lab DB '$Database' was created by install and is left in place for inspection."
Write-Host 'Drop it manually when finished: DROP DATABASE [...];'
exit 0
