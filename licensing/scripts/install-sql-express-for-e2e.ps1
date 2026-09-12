#Requires -Version 5.1
#Requires -RunAsAdministrator
<#
.SYNOPSIS
  Installs SQL Server Express on a disposable Windows host for Control Plane SCM E2E.
  LocalDB is insufficient for NT SERVICE\NyxveilControlPlane.
#>
[CmdletBinding()]
param(
    [string]$SaPassword = '',
    [string]$InstanceName = 'SQLEXPRESS'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Fail([string]$Msg) {
    Write-Output 'SQL_EXPRESS_E2E_SETUP=FAIL'
    throw $Msg
}

if ([string]::IsNullOrWhiteSpace($SaPassword)) {
    $SaPassword = 'Nyxveil-SqlE2E-' + [guid]::NewGuid().ToString('N').Substring(0, 16) + '!'
}

$svcName = 'MSSQL$' + $InstanceName
$existing = Get-Service -ErrorAction SilentlyContinue | Where-Object { $_.Name -eq $svcName }
if ($existing) {
    if ($existing.Status -ne 'Running') {
        Start-Service -Name $existing.Name
        Start-Sleep -Seconds 5
    }
    Write-Output ("SQL_EXPRESS_E2E_SETUP=PASS instance=localhost\{0} reused=1" -f $InstanceName)
    Write-Output ("SQL_EXPRESS_CONNECTION=localhost\{0}" -f $InstanceName)
    exit 0
}

# Prefer Chocolatey on GitHub windows-latest (stable, no brittle CDN media IDs).
if (-not (Get-Command choco -ErrorAction SilentlyContinue)) {
    Fail 'chocolatey (choco) is required to install SQL Server Express on this host'
}

Write-Host 'SQL_EXPRESS_STEP=choco_install'
# Package params follow chocolatey.org/packages/sql-server-express conventions.
$params = "/CONFIGURATIONFILE= /INSTANCENAME=$InstanceName /SAPWD=$SaPassword /SECURITYMODE=SQL /TCPENABLED=1 /IACCEPTSQLSERVERLICENSETERMS /FEATURES=SQLENGINE /SQLSYSADMINACCOUNTS=BUILTIN\Administrators"
choco install sql-server-express -y --no-progress --params $params
if ($LASTEXITCODE -ne 0) {
    # Retry without custom params; default instance is often SQLEXPRESS.
    Write-Host 'SQL_EXPRESS_STEP=choco_retry_default'
    choco install sql-server-express -y --no-progress
    if ($LASTEXITCODE -ne 0) {
        Fail "choco sql-server-express failed exit=$LASTEXITCODE"
    }
}

$deadline = [datetime]::UtcNow.AddMinutes(3)
$svc = $null
while ([datetime]::UtcNow -lt $deadline) {
    $svc = Get-Service -ErrorAction SilentlyContinue | Where-Object {
        $_.Name -eq $svcName -or $_.Name -eq 'MSSQLSERVER'
    } | Select-Object -First 1
    if ($svc) { break }
    Start-Sleep -Seconds 5
}
if (-not $svc) {
    Fail "SQL Engine service missing after choco install (expected $svcName)"
}
if ($svc.Status -ne 'Running') {
    Start-Service -Name $svc.Name
    Start-Sleep -Seconds 8
}

$conn = if ($svc.Name -eq 'MSSQLSERVER') { 'localhost' } else { "localhost\$InstanceName" }
Write-Output ("SQL_EXPRESS_E2E_SETUP=PASS instance={0}" -f $conn)
Write-Output ("SQL_EXPRESS_CONNECTION={0}" -f $conn)
exit 0
