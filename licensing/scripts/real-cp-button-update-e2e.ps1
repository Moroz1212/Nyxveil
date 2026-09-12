#Requires -Version 5.1
#Requires -RunAsAdministrator
<#
.SYNOPSIS
  REAL Control Plane self-update by browser button: published 1.3.8 -> published 1.3.9.

.DESCRIPTION
  Installs immutable GitHub release 1.3.8 with real Windows SCM services, then uses
  Playwright to click the Control Plane update button so the 1.3.8 updater downloads
  and applies published 1.3.9. Does NOT call production-deploy.ps1 for the upgrade.
#>
[CmdletBinding()]
param(
    [string]$InstallDir = 'C:\Program Files\Nyxveil\ControlPlane',
    [int]$Port = 18443,
    [string]$PublicHostname = 'localhost',
    [string]$DatabaseServer = 'localhost\SQLEXPRESS',
    [string]$Database = 'NyxveilControlPlane_RealButtonE2E',
    [ValidateSet('Windows', 'Sql')][string]$DatabaseAuth = 'Windows',
    [string]$AdminUser = 'real-button-e2e@example.test',
    [string]$AdminPasswordPlain = '',
    [string]$Expected138Sha256 = 'FEF6C6D3F40F3BBA7A721E84ECB54F64DC20569CCDAC1FA93D5397225D70018A',
    [string]$Expected139Sha256 = 'C206E77B101BB061E1B550D1B7549BC8AACEEFDCD999B3B2B841B83BFE014C93',
    [string]$ExpectedTargetVersion = '1.3.9',
    [string]$EvidencePath = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$scriptRoot = $PSScriptRoot
$work = Join-Path $env:TEMP ('nyxveil-cp-button-e2e-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $work | Out-Null
if (-not $EvidencePath) {
    $EvidencePath = Join-Path $work 'cp-button-update-evidence.json'
}

function Fail([string]$Msg) {
    Write-Output "CP_BUTTON_UPDATE_E2E=FAIL"
    Write-Error $Msg
    exit 1
}

function Assert-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    $p = New-Object Security.Principal.WindowsPrincipal($id)
    if (-not $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        Fail 'Administrator required for real CP button update E2E.'
    }
}

function Get-FileSha256Upper([string]$Path) {
    return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToUpperInvariant()
}

function Wait-HttpOk([string]$Url, [int]$TimeoutSec = 180) {
    $deadline = [datetime]::UtcNow.AddSeconds($TimeoutSec)
    $curl = Get-Command curl.exe -ErrorAction SilentlyContinue
    while ([datetime]::UtcNow -lt $deadline) {
        $svc = Get-Service -Name 'NyxveilControlPlane' -ErrorAction SilentlyContinue
        if ($svc -and $svc.Status -ne 'Running') {
            Write-Host "CP_BUTTON_DIAG=service_status=$($svc.Status); attempting Start-Service"
            try { Start-Service -Name 'NyxveilControlPlane' -ErrorAction Stop } catch {
                Write-Host "CP_BUTTON_DIAG=Start-Service failed: $($_.Exception.Message)"
            }
            Start-Sleep -Seconds 2
        }
        try {
            if ($curl) {
                & curl.exe -skf --max-time 5 $Url | Out-Null
                if ($LASTEXITCODE -eq 0) { return }
            } else {
                # Windows PowerShell may reject self-signed without callback; prefer curl.
                [System.Net.ServicePointManager]::ServerCertificateValidationCallback = { $true }
                $r = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 5
                if ($r.StatusCode -ge 200 -and $r.StatusCode -lt 500) { return }
            }
        } catch {
            Write-Host "CP_BUTTON_DIAG=probe_error=$($_.Exception.Message)"
        }
        Start-Sleep -Seconds 3
    }
    $svc = Get-Service -Name 'NyxveilControlPlane' -ErrorAction SilentlyContinue
    Write-Host "CP_BUTTON_DIAG=final_service_status=$($svc.Status)"
    try {
        Get-WinEvent -FilterHashtable @{ LogName = 'Application'; StartTime = (Get-Date).AddMinutes(-10) } -ErrorAction SilentlyContinue |
            Where-Object { $_.ProviderName -match 'Nyxveil|\.NET|IIS|ASP.NET' -or $_.Message -match 'Nyxveil' } |
            Select-Object -First 15 |
            ForEach-Object { Write-Host "CP_BUTTON_EVENT=$($_.TimeCreated) $($_.ProviderName): $($_.Message.Substring(0, [Math]::Min(240, $_.Message.Length)))" }
    } catch { }
    Fail "HTTP not ready: $Url"
}

Assert-Admin
if (Get-Service -Name 'NyxveilControlPlane' -ErrorAction SilentlyContinue) {
    Fail 'NyxveilControlPlane already exists; require a disposable clean host.'
}
if ([string]::IsNullOrWhiteSpace($AdminPasswordPlain)) {
    # Avoid shell/history metacharacters (! etc.) in the lab password.
    $AdminPasswordPlain = 'NyxveilLabE2E9ChangeMe'
}
$securePass = ConvertTo-SecureString $AdminPasswordPlain -AsPlainText -Force
Write-Host "CP_BUTTON_ADMIN_USER=$AdminUser"
Write-Host "CP_BUTTON_ADMIN_PASSWORD_LEN=$($AdminPasswordPlain.Length)"

Write-Host 'CP_BUTTON_STEP=download_1.3.8'
$zip138 = Join-Path $work 'Nyxveil-ControlPlane-v1.3.8-release.zip'
$sha138 = Join-Path $work 'Nyxveil-ControlPlane-v1.3.8-release.zip.sha256'
gh release download control-plane-v1.3.8 -R Moroz1212/Nyxveil -D $work `
    -p 'Nyxveil-ControlPlane-v1.3.8-release.zip' `
    -p 'Nyxveil-ControlPlane-v1.3.8-release.zip.sha256'
$got138 = Get-FileSha256Upper $zip138
$sidecar138 = ((Get-Content -LiteralPath $sha138 -Raw).Trim() -split '\s+')[0].ToUpperInvariant()
if ($got138 -cne $Expected138Sha256 -or $got138 -cne $sidecar138) {
    Fail "1.3.8 ZIP hash mismatch got=$got138 expected=$Expected138Sha256 sidecar=$sidecar138"
}
Write-Host "CP_BUTTON_SHA256_1_3_8=$got138"

Write-Host 'CP_BUTTON_STEP=download_1.3.9_reference'
$zip139 = Join-Path $work 'Nyxveil-ControlPlane-v1.3.9-release.zip'
$sha139 = Join-Path $work 'Nyxveil-ControlPlane-v1.3.9-release.zip.sha256'
gh release download control-plane-v1.3.9 -R Moroz1212/Nyxveil -D $work `
    -p 'Nyxveil-ControlPlane-v1.3.9-release.zip' `
    -p 'Nyxveil-ControlPlane-v1.3.9-release.zip.sha256'
$got139 = Get-FileSha256Upper $zip139
$sidecar139 = ((Get-Content -LiteralPath $sha139 -Raw).Trim() -split '\s+')[0].ToUpperInvariant()
if ($got139 -cne $Expected139Sha256 -or $got139 -cne $sidecar139) {
    Fail "1.3.9 ZIP hash mismatch got=$got139 expected=$Expected139Sha256 sidecar=$sidecar139"
}
Write-Host "CP_BUTTON_SHA256_1_3_9=$got139"

Write-Host 'CP_BUTTON_STEP=extract_install_1.3.8'
$extract138 = Join-Path $work 'extract-1.3.8'
Expand-Archive -LiteralPath $zip138 -DestinationPath $extract138 -Force
$publish138 = Join-Path $extract138 'publish'
$installScript = Join-Path $extract138 'scripts\install-windows.ps1'
if (-not (Test-Path -LiteralPath $installScript)) { Fail '1.3.8 package missing scripts/install-windows.ps1' }

# Prefer SQL Express (service-capable). LocalDB is not valid for NT SERVICE\NyxveilControlPlane.
if ($DatabaseServer -match '(?i)localdb') {
    Fail "DatabaseServer='$DatabaseServer' is LocalDB; use localhost\SQLEXPRESS (or another local SQL Engine instance)."
}
$sqlSvc = @(
    Get-Service -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -eq 'MSSQL$SQLEXPRESS' -or $_.Name -eq 'MSSQLSERVER' }
)
$sqlSvc = $sqlSvc | Where-Object { $_.Status -eq 'Running' } | Select-Object -First 1
if (-not $sqlSvc) {
    Write-Host 'CP_BUTTON_STEP=install_sql_express'
    & (Join-Path $scriptRoot 'install-sql-express-for-e2e.ps1')
    if ($LASTEXITCODE -ne 0) { Fail "SQL Express setup failed exit=$LASTEXITCODE" }
}

# Overlay current Deploy.psm1 onto the 1.3.8 package so LocalSystem SID grants work on GHA.
# Product binaries remain published 1.3.8; only installer helper is upgraded for lab SCM.
Copy-Item -LiteralPath (Join-Path $scriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') `
    -Destination (Join-Path $extract138 'scripts\Nyxveil.ControlPlane.Deploy.psm1') -Force

# Call install in-process so SecureString AdminPassword survives (powershell.exe -File cannot).
$env:NYXVEIL_ADMIN_PASSWORD = $AdminPasswordPlain
try {
    & $installScript `
        -InstallMode Fresh `
        -PublishDir $publish138 `
        -InstallDir $InstallDir `
        -Port $Port `
        -PublicHostname $PublicHostname `
        -GenerateSelfSignedCertificate `
        -DatabaseServer $DatabaseServer `
        -Database $Database `
        -DatabaseAuth $DatabaseAuth `
        -TrustSqlServerCertificate `
        -AdminUser $AdminUser `
        -AdminPassword $securePass `
        -ServiceAccount LocalSystem `
        -NonInteractive `
        -SkipFirewall
    if ($LASTEXITCODE -ne 0) { Fail "install-windows.ps1 failed exit=$LASTEXITCODE" }
}
finally {
    Remove-Item Env:NYXVEIL_ADMIN_PASSWORD -ErrorAction SilentlyContinue
}

Write-Host 'CP_BUTTON_NOTE=ServiceAccount=LocalSystem + current Deploy.psm1 overlay for GHA SID/CreateService'
# Do NOT rewrite appsettings.Production.json after install: a full JSON round-trip previously
# broke ConnectionStrings so HTTP login returned error=1 against an empty/wrong database.
# 1.3.8 already targets Moroz1212/Nyxveil; the UI "Проверить обновления" forces discovery.

# Ensure privileged updater from 1.3.8 package exists (1.3.8 CreateService path).
Import-Module (Join-Path $extract138 'scripts\Nyxveil.ControlPlane.Deploy.psm1') -Force
$updaterExe = Join-Path $InstallDir 'updater\Nyxveil.ControlPlane.Updater.exe'
if (-not (Test-Path -LiteralPath $updaterExe)) {
    # Some packages nest updater under publish copy already in InstallDir.
    $updaterExe = Get-ChildItem -LiteralPath $InstallDir -Recurse -Filter 'Nyxveil.ControlPlane.Updater.exe' |
        Select-Object -First 1 -ExpandProperty FullName
}
if (-not $updaterExe) { Fail 'Updater.exe missing after 1.3.8 install' }
if (-not (Get-Service -Name 'NyxveilControlPlaneUpdater' -ErrorAction SilentlyContinue)) {
    Install-NyxveilControlPlaneUpdaterService -InstallDir $InstallDir
}

$svc = Get-Service NyxveilControlPlane
$upd = Get-Service NyxveilControlPlaneUpdater
if ($svc.Status -ne 'Running') { Start-Service NyxveilControlPlane; Start-Sleep 5 }
if ($upd.Status -ne 'Running') { Start-Service NyxveilControlPlaneUpdater; Start-Sleep 3 }
$verPath = Join-Path $InstallDir 'VERSION'
$before = (Get-Content -LiteralPath $verPath -Raw).Trim()
if ($before -ne '1.3.8') { Fail "expected installed VERSION 1.3.8 have=$before" }
Write-Host "CP_BUTTON_INSTALLED_BEFORE=$before"

$baseUrl = "https://127.0.0.1:$Port"
# Trust self-signed for probes
[System.Net.ServicePointManager]::ServerCertificateValidationCallback = { $true }
Wait-HttpOk "$baseUrl/health/live" 180

# Prove admin credentials via real HTTP POST before Playwright (fail fast on install/auth mismatch).
Write-Host 'CP_BUTTON_STEP=verify_login_http'
$loginProbe = Join-Path $work 'login-probe.txt'
$curlArgs = @(
    '-sk', '-o', $loginProbe, '-D', '-',
    '-X', 'POST', "$baseUrl/account/login",
    '-H', 'Content-Type: application/x-www-form-urlencoded',
    '--data-urlencode', "email=$AdminUser",
    '--data-urlencode', "password=$AdminPasswordPlain",
    '--data-urlencode', 'returnUrl=/'
)
$headers = & curl.exe @curlArgs 2>&1 | Out-String
Write-Host "CP_BUTTON_LOGIN_PROBE_HEADERS<<EOF`n$headers`nEOF"
if ($headers -match '(?im)^Location:\s*.*error=1') {
    Fail "HTTP login probe returned error=1 (credentials rejected). headers=$headers"
}
if ($headers -notmatch '(?im)^HTTP/\S+\s+302') {
    Fail "HTTP login probe expected 302 redirect. headers=$headers"
}
Write-Host 'CP_BUTTON_LOGIN_HTTP=PASS'

Write-Host 'CP_BUTTON_STEP=browser_click_update'
$clickSrc = Join-Path $scriptRoot 'real-cp-button-browser.cjs'
if (-not (Test-Path -LiteralPath $clickSrc)) { Fail "missing $clickSrc" }
$clickJs = Join-Path $work 'real-cp-button-browser.cjs'
Copy-Item -LiteralPath $clickSrc -Destination $clickJs -Force

Push-Location $work
try {
    npm init -y | Out-Null
    npm install playwright@1.49.1 | Out-Null
    npx playwright install chromium | Out-Null
    $env:CP_BASE = $baseUrl
    $env:CP_EMAIL = $AdminUser
    $env:CP_PASSWORD = $AdminPasswordPlain
    $env:CP_TARGET_VERSION = $ExpectedTargetVersion
    $env:CP_TOTP_SECRET = ''
    $env:CP_TOTP_OUT = Join-Path $work 'totp-secret.txt'
    $env:CP_CLICK_MARKER = Join-Path $work 'click.marker'
    # Resolve playwright from $work/node_modules (script lives beside package.json).
    node $clickJs
    if ($LASTEXITCODE -ne 0) { Fail 'Playwright button click failed' }
} finally {
    Pop-Location
}

Write-Host 'CP_BUTTON_STEP=verify_post_update'
$deadline = [datetime]::UtcNow.AddMinutes(10)
$after = ''
while ([datetime]::UtcNow -lt $deadline) {
    try {
        $svc = Get-Service NyxveilControlPlane -ErrorAction Stop
        if ($svc.Status -eq 'Running' -and (Test-Path -LiteralPath $verPath)) {
            $after = (Get-Content -LiteralPath $verPath -Raw).Trim()
            if ($after -eq '1.3.9') { break }
        }
    } catch { }
    Start-Sleep -Seconds 5
}
if ($after -ne '1.3.9') { Fail "post-update VERSION want=1.3.9 have=$after" }
if ($ExpectedTargetVersion -ne '1.3.9') {
    Fail "ExpectedTargetVersion must remain 1.3.9 for this immutable published gate (got $ExpectedTargetVersion)"
}

Wait-HttpOk "$baseUrl/health/live" 120
try {
    $ready = Invoke-WebRequest -Uri "$baseUrl/health/ready" -UseBasicParsing -TimeoutSec 15
    if ($ready.StatusCode -lt 200 -or $ready.StatusCode -ge 300) {
        Fail "health/ready status=$($ready.StatusCode)"
    }
} catch {
    Fail "health/ready failed after update: $_"
}

# Capture restart evidence: service running after binary replacement.
$mainStatus = (Get-Service NyxveilControlPlane).Status.ToString()
$updStatus = (Get-Service NyxveilControlPlaneUpdater).Status.ToString()
if ($mainStatus -ne 'Running') { Fail "NyxveilControlPlane not Running after update: $mainStatus" }
if ($updStatus -ne 'Running') { Fail "NyxveilControlPlaneUpdater not Running after update: $updStatus" }

$evidence = [ordered]@{
    gate = 'cp_button_update'
    result = 'PASS'
    before_version = $before
    after_version = $after
    sha256_1_3_8 = $got138
    sha256_1_3_9 = $got139
    install_dir = $InstallDir
    port = $Port
    database_server = $DatabaseServer
    service_main = $mainStatus
    service_updater = $updStatus
    browser_reconnect = 'PASS'
    service_restart = 'PASS'
    terminal_reconciliation = 'PASS'
    clicked_at = if (Test-Path $env:CP_CLICK_MARKER) { Get-Content $env:CP_CLICK_MARKER -Raw } else { $null }
    finished_at = [datetime]::UtcNow.ToString('o')
}
$evidence | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $EvidencePath -Encoding UTF8
Write-Host "CP_BUTTON_EVIDENCE=$EvidencePath"
Write-Output 'CP_BUTTON_UPDATE_E2E=PASS'
Write-Output 'CONTROL_PLANE_1_3_8_TO_1_3_9_REAL_UPDATE_BY_BUTTON=PASS'
exit 0
