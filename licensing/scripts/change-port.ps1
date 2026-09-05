#Requires -Version 5.1
#Requires -RunAsAdministrator
<#
.SYNOPSIS
  Change the Nyxveil Control Plane HTTPS listen port (firewall + Hosting + operational config) with rollback.

.DESCRIPTION
  Updates Hosting:Port and PublicBaseUrl only (Program.cs Listen / Hosting is SoT).
  Does not write Kestrel:Endpoints. Own-service port occupancy is allowed.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][int]$Port,
    [string]$BindAddress = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force

Assert-Administrator
Assert-ValidPort -Port $Port

$op = Read-OperationalConfig
$InstallDir = [string]$op.InstallDir
$ServiceName = [string]$op.ServiceName
$oldPort = [int]$op.Port
$oldBind = [string]$op.BindAddress
$oldFirewall = [string]$op.FirewallRuleName
$publicHostname = [string]$op.PublicHostname
$certMode = [string]$op.CertificateMode
if ([string]::IsNullOrWhiteSpace($BindAddress)) { $BindAddress = $oldBind }

if ($Port -eq $oldPort -and $BindAddress -eq $oldBind) {
    Write-Host "Port already set to $Port / $BindAddress."
    exit 0
}

if (-not (Test-TcpPortAvailable -Port $Port -BindAddress $BindAddress -AllowServiceName $ServiceName)) {
    throw "Port $Port is occupied by a foreign process. Foreign services will not be stopped."
}

$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$bakDir = Join-Path (Get-ProgramDataRoot) "backups\change-port-$stamp"
New-Item -ItemType Directory -Force -Path $bakDir | Out-Null

$appsettingsPath = Join-Path $InstallDir 'appsettings.Production.json'
$opPrimary = Get-OperationalConfigPath
Copy-Item $appsettingsPath -Destination (Join-Path $bakDir 'appsettings.Production.json') -Force
Copy-Item $opPrimary -Destination (Join-Path $bakDir 'operational.json') -Force
$opInstall = Join-Path $InstallDir 'config\operational.json'
if (Test-Path $opInstall) {
    Copy-Item $opInstall -Destination (Join-Path $bakDir 'operational.install.json') -Force
}

$newFirewall = ''
try {
    Write-Host "Changing port $oldPort -> $Port ..."
    Stop-Service -Name $ServiceName -Force

    $settings = Get-Content -LiteralPath $appsettingsPath -Raw -Encoding UTF8 | ConvertFrom-Json
    if (-not $settings.Hosting) {
        $settings | Add-Member -NotePropertyName Hosting -NotePropertyValue ([pscustomobject]@{}) -Force
    }
    $settings.Hosting.Port = $Port
    $settings.Hosting.BindAddress = $BindAddress
    $settings.Hosting.PublicBaseUrl = (Build-PublicBaseUrl -PublicHostname $publicHostname -Port $Port)
    # Remove legacy Kestrel:Endpoints if present (Hosting is SoT).
    if ($settings.PSObject.Properties.Name -contains 'Kestrel') {
        $settings.PSObject.Properties.Remove('Kestrel')
    }
    ($settings | ConvertTo-Json -Depth 10) | Set-Content -LiteralPath $appsettingsPath -Encoding UTF8

    $op.Port = $Port
    $op.BindAddress = $BindAddress
    $op.PublicBaseUrl = Build-PublicBaseUrl -PublicHostname $publicHostname -Port $Port
    $newFirewall = Update-NyxveilFirewallRule -Port $Port -PreviousRuleName $oldFirewall
    $op.FirewallRuleName = $newFirewall
    Write-OperationalConfig -Config $op -InstallDir $InstallDir

    Start-Service -Name $ServiceName
    if (-not (Wait-HttpsHealthy -Port $Port -PublicHostname $publicHostname -InstallDir $InstallDir `
            -CertificateMode $certMode -TimeoutSec 60)) {
        throw "Health failed after port change at $publicHostname :$Port"
    }
    Write-Host "Port change complete. PublicBaseUrl=$($op.PublicBaseUrl)"
}
catch {
    Write-Warning "Port change failed: $($_.Exception.Message). Rolling back..."
    try {
        Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
        Copy-Item (Join-Path $bakDir 'appsettings.Production.json') -Destination $appsettingsPath -Force
        Copy-Item (Join-Path $bakDir 'operational.json') -Destination $opPrimary -Force
        if (Test-Path (Join-Path $bakDir 'operational.install.json')) {
            Copy-Item (Join-Path $bakDir 'operational.install.json') -Destination $opInstall -Force
        }
        if ($newFirewall -and $newFirewall -ne $oldFirewall) {
            Remove-NyxveilFirewallRule -RuleName $newFirewall -ErrorAction SilentlyContinue
        }
        if ($oldFirewall) {
            New-NyxveilFirewallRule -Port $oldPort -RuleName $oldFirewall | Out-Null
        }
        Start-Service -Name $ServiceName -ErrorAction SilentlyContinue
    }
    catch {
        Write-Warning "Rollback encountered: $($_.Exception.Message)"
    }
    throw
}
