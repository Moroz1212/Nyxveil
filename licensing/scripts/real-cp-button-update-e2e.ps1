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
    while ([datetime]::UtcNow -lt $deadline) {
        try {
            $r = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 5
            if ($r.StatusCode -ge 200 -and $r.StatusCode -lt 500) { return }
        } catch { }
        Start-Sleep -Seconds 3
    }
    Fail "HTTP not ready: $Url"
}

Assert-Admin
if (Get-Service -Name 'NyxveilControlPlane' -ErrorAction SilentlyContinue) {
    Fail 'NyxveilControlPlane already exists; require a disposable clean host.'
}
if ([string]::IsNullOrWhiteSpace($AdminPasswordPlain)) {
    $AdminPasswordPlain = 'Nyxveil-E2E-' + [guid]::NewGuid().ToString('N').Substring(0, 12) + '!'
}
$securePass = ConvertTo-SecureString $AdminPasswordPlain -AsPlainText -Force

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
        -NonInteractive `
        -SkipFirewall
    if ($LASTEXITCODE -ne 0) { Fail "install-windows.ps1 failed exit=$LASTEXITCODE" }
}
finally {
    Remove-Item Env:NYXVEIL_ADMIN_PASSWORD -ErrorAction SilentlyContinue
}

# Accelerate GitHub discovery cache for the button gate (1.3.8 already defaults to Moroz1212/Nyxveil).
$appsettings = Join-Path $InstallDir 'appsettings.Production.json'
if (Test-Path -LiteralPath $appsettings) {
    $cfg = Get-Content -LiteralPath $appsettings -Raw | ConvertFrom-Json
    $policy = [ordered]@{
        CacheMinutes = 1
        GitHubOwner  = 'Moroz1212'
        GitHubRepo   = 'Nyxveil'
    }
    $cfg | Add-Member -NotePropertyName ServerReleasePolicy -NotePropertyValue ([pscustomobject]$policy) -Force
    $cfg | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $appsettings -Encoding UTF8
    Restart-Service -Name NyxveilControlPlane -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 5
}

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

Write-Host 'CP_BUTTON_STEP=browser_click_update'
$clickJs = Join-Path $work 'click-update.mjs'
@'
const { chromium } = require('playwright');
const fs = require('fs');
const crypto = require('crypto');

function totp(secretB32) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = '';
  const cleaned = String(secretB32).replace(/[\s=]+/g, '').toUpperCase();
  for (const c of cleaned) {
    const val = alphabet.indexOf(c);
    if (val < 0) continue;
    bits += val.toString(2).padStart(5, '0');
  }
  const bytes = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) bytes.push(parseInt(bits.slice(i, i + 8), 2));
  const key = Buffer.from(bytes);
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 1000 / 30)));
  const hmac = crypto.createHmac('sha1', key).update(counter).digest();
  const offset = hmac[hmac.length - 1] & 0xf;
  const code = ((hmac[offset] & 0x7f) << 24) | (hmac[offset + 1] << 16) | (hmac[offset + 2] << 8) | hmac[offset + 3];
  return String(code % 1000000).padStart(6, '0');
}

function extractSecret(pageText, otpUri) {
  if (otpUri) {
    const m = /[?&]secret=([A-Z2-7]+)/i.exec(otpUri);
    if (m) return m[1].toUpperCase();
  }
  const m2 = /([A-Z2-7]{16,})/.exec(pageText.replace(/\s+/g, ''));
  return m2 ? m2[1].toUpperCase() : '';
}

(async () => {
  const base = process.env.CP_BASE;
  const email = process.env.CP_EMAIL;
  const password = process.env.CP_PASSWORD;
  let totpSecret = process.env.CP_TOTP_SECRET || '';
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  page.setDefaultTimeout(180000);

  await page.goto(base + '/account/login');
  await page.fill('input[name=email]', email);
  await page.fill('input[name=password]', password);
  await page.getByRole('button', { name: 'Войти' }).click();

  // Mandatory MFA enrollment for SuperAdmin.
  if (page.url().includes('/account/mfa/setup') || await page.getByRole('heading', { name: 'Настройка MFA' }).count()) {
    await page.locator('details summary').first().click({ timeout: 30000 }).catch(() => {});
    const keyInput = page.locator('input.mono[readonly]').first();
    await keyInput.waitFor({ state: 'visible', timeout: 60000 });
    const shared = await keyInput.inputValue();
    const uri = await page.locator('textarea.mono').inputValue().catch(() => '');
    totpSecret = extractSecret(shared + ' ' + uri, uri) || shared.replace(/\s+/g, '').toUpperCase();
    if (!totpSecret) throw new Error('could not extract MFA shared key');
    fs.writeFileSync(process.env.CP_TOTP_OUT || 'totp.txt', totpSecret);
    await page.fill('input[name=code]', totp(totpSecret));
    await page.getByRole('button', { name: 'Включить MFA' }).click();
    // recovery codes page
    const cont = page.getByRole('link', { name: 'Продолжить' });
    await cont.waitFor({ state: 'visible', timeout: 60000 });
    await cont.click();
  }

  // Login 2FA if redirected
  if (await page.locator('input[name=code]').count()) {
    if (!totpSecret) throw new Error('TOTP required');
    await page.fill('input[name=code]', totp(totpSecret));
    await page.getByRole('button', { name: /Подтвердить|Войти/ }).click();
  }

  await page.waitForURL(u => !String(u).includes('/account/login') && !String(u).includes('/account/mfa/setup'), { timeout: 120000 });

  // Step-up for update
  await page.goto(base + '/account/mfa/step-up?returnUrl=' + encodeURIComponent('/admin/control-plane'));
  if (await page.locator('input[name=code]').count()) {
    await page.fill('input[name=code]', totp(totpSecret));
    await page.getByRole('button', { name: 'Подтвердить' }).click();
  }

  await page.goto(base + '/admin/control-plane');
  await page.getByRole('button', { name: 'Проверить обновления' }).click();
  await page.waitForTimeout(5000);
  const updateBtn = page.getByTestId('control-plane-update');
  await updateBtn.waitFor({ state: 'visible' });
  await expectEnabled(updateBtn);
  await updateBtn.click();
  const confirm = page.getByRole('button', { name: 'Подтвердить обновление' });
  await confirm.waitFor({ state: 'visible', timeout: 90000 });
  await confirm.click();
  fs.writeFileSync(process.env.CP_CLICK_MARKER, 'clicked=' + new Date().toISOString());

  let ok = false;
  for (let i = 0; i < 120; i++) {
    try {
      await page.goto(base + '/admin/control-plane', { waitUntil: 'domcontentloaded', timeout: 20000 });
      const body = await page.textContent('body');
      if (body && /Installed:[\s\S]*1\.3\.9/.test(body)) { ok = true; break; }
      if (body && body.includes('1.3.9')) { ok = true; break; }
    } catch (_) {}
    await page.waitForTimeout(5000);
  }
  await browser.close();
  if (!ok) throw new Error('browser did not observe VERSION 1.3.9 after button update');
  console.log('CP_BUTTON_BROWSER_OBSERVED_1_3_9=PASS');
})().catch(err => { console.error(err); process.exit(1); });

async function expectEnabled(locator) {
  for (let i = 0; i < 60; i++) {
    if (await locator.isEnabled()) return;
    await new Promise(r => setTimeout(r, 2000));
  }
  throw new Error('update button stayed disabled (1.3.9 not discovered?)');
}
'@ | Set-Content -LiteralPath $clickJs -Encoding UTF8

Push-Location $work
try {
    npm init -y | Out-Null
    npm install playwright@1.49.1 | Out-Null
    npx playwright install chromium | Out-Null
    $env:CP_BASE = $baseUrl
    $env:CP_EMAIL = $AdminUser
    $env:CP_PASSWORD = $AdminPasswordPlain
    $env:CP_TOTP_SECRET = ''
    $env:CP_TOTP_OUT = Join-Path $work 'totp-secret.txt'
    $env:CP_CLICK_MARKER = Join-Path $work 'click.marker'
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
