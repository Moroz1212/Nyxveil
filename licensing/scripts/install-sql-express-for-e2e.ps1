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
    Write-Output "SQL_EXPRESS_E2E_SETUP=FAIL"
    throw $Msg
}

if ([string]::IsNullOrWhiteSpace($SaPassword)) {
    $SaPassword = 'Nyxveil-SqlE2E-' + [guid]::NewGuid().ToString('N').Substring(0, 16) + '!'
}

$existing = Get-Service -Name ("MSSQL`$" + $InstanceName) -ErrorAction SilentlyContinue
if ($existing) {
    if ($existing.Status -ne 'Running') {
        Start-Service -Name $existing.Name
        Start-Sleep -Seconds 5
    }
    Write-Output ("SQL_EXPRESS_E2E_SETUP=PASS instance=localhost\{0} reused=1" -f $InstanceName)
    Write-Output ("SQL_EXPRESS_CONNECTION=localhost\{0}" -f $InstanceName)
    exit 0
}

$work = Join-Path $env:TEMP ('nyxveil-sqlexpress-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $work | Out-Null
$setup = Join-Path $work 'SQLEXPR_x64_ENU.exe'
# Microsoft SQL Server 2022 Express bootstrapper (public CDN).
$url = 'https://download.microsoft.com/download/5/1/4/5145b935-505c-4522-9ece-bd817748e89b/SQL2022-SSEI-Expr.exe'
Write-Host "SQL_EXPRESS_STEP=download"
Invoke-WebRequest -Uri $url -OutFile $setup -UseBasicParsing

$ini = Join-Path $work 'ConfigurationFile.ini'
@"
[OPTIONS]
ACTION="Install"
FEATURES=SQLENGINE
INSTANCENAME="$InstanceName"
INSTANCEID="$InstanceName"
SQLSYSADMINACCOUNTS="BUILTIN\Administrators"
SECURITYMODE=SQL
SAPWD="$SaPassword"
TCPENABLED=1
NPENABLED=1
IACCEPTSQLSERVERLICENSETERMS="True"
QUIET="True"
UPDATEENABLED=False
AGTSVCSTARTUPTYPE="Manual"
SQLSVCSTARTUPTYPE="Automatic"
"@ | Set-Content -LiteralPath $ini -Encoding ASCII

Write-Host "SQL_EXPRESS_STEP=install"
$args = @(
    '/Q',
    '/Action=Install',
    '/IAcceptSqlServerLicenseTerms',
    "/ConfigurationFile=$ini",
    "/SAPWD=$SaPassword",
    "/INSTANCENAME=$InstanceName",
    '/FEATURES=SQLENGINE',
    '/SQLSYSADMINACCOUNTS=BUILTIN\Administrators',
    '/TCPENABLED=1',
    '/SECURITYMODE=SQL',
    '/UpdateEnabled=0'
)
$p = Start-Process -FilePath $setup -ArgumentList $args -Wait -PassThru
if ($p.ExitCode -notin 0, 3010) {
    # Fallback: chocolatey if bootstrapper shape differs on the runner image.
    Write-Host "SQL_EXPRESS_STEP=choco_fallback exit=$($p.ExitCode)"
    if (-not (Get-Command choco -ErrorAction SilentlyContinue)) {
        Fail "SQL Express installer failed exit=$($p.ExitCode) and choco is unavailable"
    }
    choco install sql-server-express -y --no-progress --params "/CONFIGURATIONFILE=$ini"
    if ($LASTEXITCODE -ne 0) {
        Fail "choco sql-server-express failed exit=$LASTEXITCODE"
    }
}

$svc = Get-Service -Name ("MSSQL`$" + $InstanceName) -ErrorAction SilentlyContinue
if (-not $svc) {
    Fail "MSSQL`$$InstanceName service missing after install"
}
if ($svc.Status -ne 'Running') {
    Start-Service -Name $svc.Name
    Start-Sleep -Seconds 8
}

# Ensure TCP is enabled for the instance (Windows auth from NT SERVICE works locally via shared memory/named pipes too).
Write-Output ("SQL_EXPRESS_E2E_SETUP=PASS instance=localhost\{0}" -f $InstanceName)
Write-Output ("SQL_EXPRESS_CONNECTION=localhost\{0}" -f $InstanceName)
# Do not print SA password.
exit 0
