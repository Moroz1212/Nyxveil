#Requires -Version 5.1
#Requires -RunAsAdministrator
[CmdletBinding()]
param(
    [string]$InstanceName = "SQLEXPRESS"
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

function Fail([string]$Msg) {
    Write-Output "SQL_EXPRESS_E2E_SETUP=FAIL"
    throw $Msg
}

$svcName = "MSSQL$" + $InstanceName
$existing = Get-Service -ErrorAction SilentlyContinue | Where-Object { $_.Name -eq $svcName -or $_.Name -eq "MSSQLSERVER" } | Select-Object -First 1
if ($existing -and $existing.Status -eq "Running") {
    $conn = if ($existing.Name -eq "MSSQLSERVER") { "localhost" } else { "localhost\$InstanceName" }
    Write-Output ("SQL_EXPRESS_E2E_SETUP=PASS instance={0} reused=1" -f $conn)
    Write-Output ("SQL_EXPRESS_CONNECTION={0}" -f $conn)
    exit 0
}

if (-not (Get-Command choco -ErrorAction SilentlyContinue)) {
    Fail "chocolatey (choco) is required to install SQL Server Express on this host"
}

Write-Host "SQL_EXPRESS_STEP=choco_install"
# No custom ConfigurationFile — chocolatey package downloads Microsoft media itself.
choco install sql-server-express -y --no-progress --execution-timeout 2700
if ($LASTEXITCODE -ne 0) {
    Fail "choco sql-server-express failed exit=$LASTEXITCODE"
}

$deadline = [datetime]::UtcNow.AddMinutes(5)
$svc = $null
while ([datetime]::UtcNow -lt $deadline) {
    $svc = Get-Service -ErrorAction SilentlyContinue | Where-Object {
        $_.Name -eq $svcName -or $_.Name -eq "MSSQLSERVER"
    } | Select-Object -First 1
    if ($svc) { break }
    Start-Sleep -Seconds 5
}
if (-not $svc) { Fail "SQL Engine service missing after choco install" }
if ($svc.Status -ne "Running") {
    Start-Service -Name $svc.Name
    Start-Sleep -Seconds 8
}

$conn = if ($svc.Name -eq "MSSQLSERVER") { "localhost" } else { "localhost\$InstanceName" }
Write-Output ("SQL_EXPRESS_E2E_SETUP=PASS instance={0}" -f $conn)
Write-Output ("SQL_EXPRESS_CONNECTION={0}" -f $conn)
exit 0
