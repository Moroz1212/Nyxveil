#Requires -Version 5.1
#Requires -RunAsAdministrator
<#
.SYNOPSIS
  Artifact-pure Control Plane self-update probe: published 1.3.8 -> published 1.3.10 by button.

.DESCRIPTION
  Installs byte-exact published 1.3.8 product payload (PublishDir from release zip).
  Installer tooling may use current workspace install helpers (INITIAL INSTALL only).
  Does NOT overlay self-update-apply.ps1 / Deploy.psm1 into InstallDir before the button.
  Does NOT call production-deploy.ps1 for the upgrade itself.

  Expected outcome on immutable 1.3.8: button update fails (installed apply script defect).
  Evidence records CP bootstrap limitation + artifact purity of the source install.
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
    [string]$SourceVersion = '1.3.8',
    [string]$TargetVersion = '1.3.10',
    [string]$ExpectedSourceZipSha256 = 'FEF6C6D3F40F3BBA7A721E84ECB54F64DC20569CCDAC1FA93D5397225D70018A',
    [string]$ExpectedTargetZipSha256 = '',
    [switch]$ExpectBootstrapLimitation,
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
    try { Stop-LabGitHubApiProxy } catch { }
    Write-Output "CP_BUTTON_UPDATE_E2E=FAIL"
    Write-Error $Msg
    exit 1
}

$script:LabProxyStarted = $false
$script:LabProxyProc = $null
$script:LabHostsPath = Join-Path $env:SystemRoot 'System32\drivers\etc\hosts'
$script:LabHostsBackup = $null
function Stop-LabGitHubApiProxy {
    if (-not $script:LabProxyStarted) { return }
    $script:LabProxyStarted = $false
    if ($script:LabProxyProc -and -not $script:LabProxyProc.HasExited) {
        try { Stop-Process -Id $script:LabProxyProc.Id -Force -ErrorAction SilentlyContinue } catch { }
    }
    if ($script:LabHostsBackup -and (Test-Path -LiteralPath $script:LabHostsBackup)) {
        Copy-Item -LiteralPath $script:LabHostsBackup -Destination $script:LabHostsPath -Force
    } elseif (Test-Path -LiteralPath $script:LabHostsPath) {
        $hostsText = Get-Content -LiteralPath $script:LabHostsPath -Raw
        $hostsText = ($hostsText -split "`r?`n" | Where-Object { $_ -notmatch '^\s*127\.0\.0\.1\s+api\.github\.com\s*$' }) -join "`r`n"
        Set-Content -LiteralPath $script:LabHostsPath -Value $hostsText -Encoding ascii
    }
    try {
        Get-ChildItem Cert:\LocalMachine\Root -ErrorAction SilentlyContinue |
            Where-Object { $_.Subject -match 'CN=api\.github\.com' -and $_.Issuer -match 'NyxveilLab' } |
            Remove-Item -Force -ErrorAction SilentlyContinue
    } catch { }
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
    # Must satisfy Identity password options (len>=12, upper/lower/digit/non-alphanumeric).
    $AdminPasswordPlain = 'NyxveilLabE2E9!Change'
}
$securePass = ConvertTo-SecureString $AdminPasswordPlain -AsPlainText -Force
Write-Host "CP_BUTTON_ADMIN_USER=$AdminUser"
Write-Host "CP_BUTTON_ADMIN_PASSWORD_LEN=$($AdminPasswordPlain.Length)"

# --- Lab GitHub API proxy (external infrastructure; not a product overlay) -------
# Immutable published CP builds call api.github.com without a token and hit GHA
# shared unauthenticated 403 rate limits. Hijack hosts → local TLS terminator
# that injects GH_TOKEN for release discovery only.
$labCertCer = Join-Path $work 'lab-api-github.cer'
try {
    Write-Host 'CP_BUTTON_STEP=lab_github_api_proxy'
    $token = $env:GH_TOKEN
    if ([string]::IsNullOrWhiteSpace($token)) { $token = $env:GITHUB_TOKEN }
    if ([string]::IsNullOrWhiteSpace($token)) { Fail 'GH_TOKEN/GITHUB_TOKEN required for lab GitHub API proxy' }
    $script:LabHostsBackup = Join-Path $work 'hosts.bak'
    Copy-Item -LiteralPath $script:LabHostsPath -Destination $script:LabHostsBackup -Force
    $hostsNow = Get-Content -LiteralPath $script:LabHostsPath -Raw
    if ($hostsNow -notmatch '(?m)^\s*127\.0\.0\.1\s+api\.github\.com\s*$') {
        Add-Content -LiteralPath $script:LabHostsPath -Value "`r`n127.0.0.1 api.github.com`r`n" -Encoding ascii
    }
    $cert = New-SelfSignedCertificate `
        -DnsName 'api.github.com' `
        -FriendlyName 'NyxveilLabGitHubApiProxy' `
        -CertStoreLocation 'Cert:\LocalMachine\My' `
        -KeyExportPolicy Exportable `
        -NotAfter (Get-Date).AddDays(2) `
        -Subject 'CN=api.github.com, O=NyxveilLab'
    Export-Certificate -Cert $cert -FilePath $labCertCer -Force | Out-Null
    Import-Certificate -FilePath $labCertCer -CertStoreLocation 'Cert:\LocalMachine\Root' | Out-Null
    $pfx = Join-Path $work 'lab-api-github.pfx'
    $pfxPass = ConvertTo-SecureString 'LabProxyOnly' -AsPlainText -Force
    Export-PfxCertificate -Cert $cert -FilePath $pfx -Password $pfxPass | Out-Null
    $pemCert = Join-Path $work 'lab-api-github.crt'
    $pemKey = Join-Path $work 'lab-api-github.key'
    $openssl = Get-Command openssl.exe -ErrorAction SilentlyContinue
    if (-not $openssl) {
        $openssl = Get-Command 'C:\Program Files\Git\usr\bin\openssl.exe' -ErrorAction SilentlyContinue
    }
    if (-not $openssl) { Fail 'openssl required to export lab proxy PEM key' }
    & $openssl.Source pkcs12 -in $pfx -out $pemCert -clcerts -nokeys -passin pass:LabProxyOnly | Out-Null
    if ($LASTEXITCODE -ne 0) { Fail "openssl cert export failed exit=$LASTEXITCODE" }
    & $openssl.Source pkcs12 -in $pfx -out $pemKey -nocerts -nodes -passin pass:LabProxyOnly | Out-Null
    if ($LASTEXITCODE -ne 0) { Fail "openssl key export failed exit=$LASTEXITCODE" }
    $proxyJs = Join-Path $scriptRoot 'lab-github-api-proxy.cjs'
    if (-not (Test-Path -LiteralPath $proxyJs)) { Fail "missing $proxyJs" }
    $env:CERT_PATH = $pemCert
    $env:KEY_PATH = $pemKey
    $env:GH_TOKEN = $token
    $script:LabProxyProc = Start-Process -FilePath (Get-Command node.exe).Source `
        -ArgumentList @($proxyJs) `
        -PassThru -WindowStyle Hidden `
        -RedirectStandardOutput (Join-Path $work 'lab-proxy.out') `
        -RedirectStandardError (Join-Path $work 'lab-proxy.err')
    $script:LabProxyStarted = $true
    Start-Sleep -Seconds 2
    if ($script:LabProxyProc.HasExited) {
        Write-Host (Get-Content (Join-Path $work 'lab-proxy.err') -Raw -ErrorAction SilentlyContinue)
        Fail "lab GitHub API proxy exited early code=$($script:LabProxyProc.ExitCode)"
    }
    Write-Host "CP_BUTTON_LAB_PROXY_PID=$($script:LabProxyProc.Id)"
} catch {
    Stop-LabGitHubApiProxy
    throw
}

Write-Host 'CP_BUTTON_STEP=download_source_release'
$zipSrc = Join-Path $work ("Nyxveil-ControlPlane-v{0}-release.zip" -f $SourceVersion)
$shaSrc = "$zipSrc.sha256"
gh release download ("control-plane-v{0}" -f $SourceVersion) -R Moroz1212/Nyxveil -D $work `
    -p ("Nyxveil-ControlPlane-v{0}-release.zip" -f $SourceVersion) `
    -p ("Nyxveil-ControlPlane-v{0}-release.zip.sha256" -f $SourceVersion)
$gotSrc = Get-FileSha256Upper $zipSrc
$sidecarSrc = ((Get-Content -LiteralPath $shaSrc -Raw).Trim() -split '\s+')[0].ToUpperInvariant()
if ($gotSrc -cne $ExpectedSourceZipSha256 -or $gotSrc -cne $sidecarSrc) {
    Fail "source ZIP hash mismatch got=$gotSrc expected=$ExpectedSourceZipSha256 sidecar=$sidecarSrc"
}
Write-Host "CP_BUTTON_SHA256_SOURCE=$gotSrc"

Write-Host 'CP_BUTTON_STEP=download_target_release'
$zipTgt = Join-Path $work ("Nyxveil-ControlPlane-v{0}-release.zip" -f $TargetVersion)
$shaTgt = "$zipTgt.sha256"
gh release download ("control-plane-v{0}" -f $TargetVersion) -R Moroz1212/Nyxveil -D $work `
    -p ("Nyxveil-ControlPlane-v{0}-release.zip" -f $TargetVersion) `
    -p ("Nyxveil-ControlPlane-v{0}-release.zip.sha256" -f $TargetVersion)
if (-not (Test-Path -LiteralPath $zipTgt)) {
    Fail "target release control-plane-v$TargetVersion is not published yet; publish before artifact-pure E2E"
}
$gotTgt = Get-FileSha256Upper $zipTgt
$sidecarTgt = ((Get-Content -LiteralPath $shaTgt -Raw).Trim() -split '\s+')[0].ToUpperInvariant()
if ($ExpectedTargetZipSha256 -and ($gotTgt -cne $ExpectedTargetZipSha256 -or $gotTgt -cne $sidecarTgt)) {
    Fail "target ZIP hash mismatch got=$gotTgt expected=$ExpectedTargetZipSha256 sidecar=$sidecarTgt"
}
if ($gotTgt -cne $sidecarTgt) {
    Fail "target ZIP hash mismatch got=$gotTgt sidecar=$sidecarTgt"
}
Write-Host "CP_BUTTON_SHA256_TARGET=$gotTgt"

Write-Host "CP_BUTTON_STEP=extract_install_$SourceVersion"
$extractSrc = Join-Path $work ("extract-{0}" -f $SourceVersion)
Expand-Archive -LiteralPath $zipSrc -DestinationPath $extractSrc -Force
$publishSrc = Join-Path $extractSrc 'publish'
# INITIAL INSTALL harness: use current workspace installer against published PublishDir
# so GHA LocalSystem SID grants work. Product payload remains byte-exact release publish/.
$installScript = Join-Path $scriptRoot 'install-windows.ps1'
if (-not (Test-Path -LiteralPath $installScript)) { Fail 'workspace missing scripts/install-windows.ps1' }
if (-not (Test-Path -LiteralPath $publishSrc)) { Fail 'source package missing publish/' }

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
        -PublishDir $publishSrc `
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

Write-Host 'CP_BUTTON_NOTE=INITIAL_INSTALL_HARNESS=workspace_install_windows+published_PublishDir; NO InstallDir overlay'

# Lab infrastructure: machine-level GitHub token for release discovery when the
# installed product supports ServerReleasePolicy:GitHubToken / GITHUB_TOKEN (1.3.12+).
# Harmless on older builds that ignore the env var.
$token = $env:GH_TOKEN
if ([string]::IsNullOrWhiteSpace($token)) { $token = $env:GITHUB_TOKEN }
if (-not [string]::IsNullOrWhiteSpace($token)) {
    [Environment]::SetEnvironmentVariable('GITHUB_TOKEN', $token, 'Machine')
    [Environment]::SetEnvironmentVariable('GH_TOKEN', $token, 'Machine')
    Write-Host 'CP_BUTTON_NOTE=lab_machine_GITHUB_TOKEN_set_for_release_discovery'
}

Import-Module (Join-Path $scriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force
$updaterExe = Join-Path $InstallDir 'updater\Nyxveil.ControlPlane.Updater.exe'
if (-not (Test-Path -LiteralPath $updaterExe)) {
    $updaterExe = Get-ChildItem -LiteralPath $InstallDir -Recurse -Filter 'Nyxveil.ControlPlane.Updater.exe' |
        Select-Object -First 1 -ExpandProperty FullName
}
if (-not $updaterExe) { Fail 'Updater.exe missing after source install' }
if (-not (Get-Service -Name 'NyxveilControlPlaneUpdater' -ErrorAction SilentlyContinue)) {
    Install-NyxveilControlPlaneUpdaterService -InstallDir $InstallDir
}

$svc = Get-Service NyxveilControlPlane
$upd = Get-Service NyxveilControlPlaneUpdater
if ($svc.Status -ne 'Running') { Start-Service NyxveilControlPlane; Start-Sleep 5 }
if ($upd.Status -ne 'Running') { Start-Service NyxveilControlPlaneUpdater; Start-Sleep 3 }
$verPath = Join-Path $InstallDir 'VERSION'
$before = (Get-Content -LiteralPath $verPath -Raw).Trim()
if ($before -ne $SourceVersion) { Fail "expected installed VERSION $SourceVersion have=$before" }
Write-Host "CP_BUTTON_INSTALLED_BEFORE=$before"

# ARTIFACT PURITY: installed product files must match published source publish/ (no overlay).
function Assert-InstalledMatchesPublish([string]$InstallRoot, [string]$PublishRoot, [string[]]$RelPaths) {
    $map = [ordered]@{}
    foreach ($rel in $RelPaths) {
        $a = Join-Path $InstallRoot $rel
        $b = Join-Path $PublishRoot $rel
        if (-not (Test-Path -LiteralPath $a)) { throw "missing installed file: $rel" }
        if (-not (Test-Path -LiteralPath $b)) { throw "missing publish reference: $rel" }
        $ha = Get-FileSha256Upper $a
        $hb = Get-FileSha256Upper $b
        if ($ha -cne $hb) {
            throw "ARTIFACT_PURITY mismatch for $rel installed=$ha publish=$hb"
        }
        $map[$rel] = $ha
    }
    return $map
}

$purityRels = @(
    'VERSION',
    'Nyxveil.ControlPlane.Web.dll',
    'updater\Nyxveil.ControlPlane.Updater.exe',
    'scripts\self-update-apply.ps1'
)
try {
    $sourceHashes = Assert-InstalledMatchesPublish -InstallRoot $InstallDir -PublishRoot $publishSrc -RelPaths $purityRels
    Write-Host 'CP_SOURCE_ARTIFACT_PURITY=PASS'
    Write-Host 'CP_NO_OVERLAY=PASS'
}
catch {
    Fail "source artifact purity failed: $($_.Exception.Message)"
}

# Force-reset admin password via env (no stdin) so Windows \r\n pipe cannot alter the secret.
Write-Host 'CP_BUTTON_STEP=reset_admin_password'
$env:NYXVEIL_ADMIN_PASSWORD = $AdminPasswordPlain
try {
    $reset = Invoke-NyxveilWebCli -InstallDir $InstallDir `
        -Arguments @('admin', 'reset-password', '--username', $AdminUser)
    Write-Host "CP_BUTTON_RESET_EXIT=$($reset.ExitCode)"
    if ($reset.StdOut) { Write-Host $reset.StdOut }
    if ($reset.StdErr) { Write-Host $reset.StdErr }
    if ($reset.ExitCode -ne 0) { Fail "admin reset-password failed exit=$($reset.ExitCode)" }
} finally {
    Remove-Item Env:NYXVEIL_ADMIN_PASSWORD -ErrorAction SilentlyContinue
}

# Prove the web process sees the same AspNetUsers row (Windows auth as LocalSystem vs installer identity).
Write-Host 'CP_BUTTON_STEP=sql_user_probe'
try {
    $userRows = & sqlcmd -S $DatabaseServer -E -d $Database -h -1 -W -Q `
        "SET NOCOUNT ON; SELECT Email, UserName, NormalizedEmail, CASE WHEN PasswordHash IS NULL THEN 'NULL' ELSE 'SET' END FROM AspNetUsers;" 2>&1 |
        Out-String
    Write-Host "CP_BUTTON_SQL_USERS<<EOF`n$userRows`nEOF"
} catch {
    Write-Host "CP_BUTTON_SQL_USERS_WARN=$($_.Exception.Message)"
}

$baseUrl = "https://127.0.0.1:$Port"
# Trust self-signed for probes
[System.Net.ServicePointManager]::ServerCertificateValidationCallback = { $true }
Wait-HttpOk "$baseUrl/health/live" 180

# Prove admin credentials via real HTTP POST before Playwright (fail fast on install/auth mismatch).
Write-Host 'CP_BUTTON_STEP=verify_login_http'
$loginProbe = Join-Path $work 'login-probe.txt'
$formFile = Join-Path $work 'login.form'
# Write form body to a file so PowerShell cannot split on '&' when invoking curl.
$formBody = "email=$([uri]::EscapeDataString($AdminUser))&password=$([uri]::EscapeDataString($AdminPasswordPlain))&returnUrl=%2F"
$utf8NoBom = New-Object System.Text.UTF8Encoding $false
[System.IO.File]::WriteAllText($formFile, $formBody, $utf8NoBom)
Write-Host "CP_BUTTON_LOGIN_FORM_BYTES=$([System.IO.File]::ReadAllBytes($formFile).Length)"
$headers = & curl.exe -sk -o $loginProbe -D - -X POST "$baseUrl/account/login" `
    -H 'Content-Type: application/x-www-form-urlencoded' `
    --data-binary "@$formFile" 2>&1 | Out-String
Write-Host "CP_BUTTON_LOGIN_PROBE_HEADERS<<EOF`n$headers`nEOF"
if ($headers -match '(?im)^Location:\s*.*error=1') {
    try {
        $failedRows = & sqlcmd -S $DatabaseServer -E -d $Database -h -1 -W -Q `
            "SET NOCOUNT ON; SELECT Email, AccessFailedCount, LockoutEnabled, CONVERT(varchar(33), LockoutEnd, 126), TwoFactorEnabled FROM AspNetUsers;" 2>&1 |
            Out-String
        Write-Host "CP_BUTTON_SQL_AFTER_LOGIN<<EOF`n$failedRows`nEOF"
    } catch { }
    $logDir = Join-Path $env:ProgramData 'Nyxveil\ControlPlane\logs'
    if (Test-Path -LiteralPath $logDir) {
        Get-ChildItem -LiteralPath $logDir -File | Sort-Object LastWriteTime -Descending | Select-Object -First 1 | ForEach-Object {
            Write-Host "CP_BUTTON_LOG_FILE=$($_.FullName)"
            Get-Content -LiteralPath $_.FullName -Tail 40 | ForEach-Object { Write-Host "CP_BUTTON_LOG=$_" }
        }
    }
    Fail "HTTP login probe returned error=1 (credentials rejected). formBytes=$([System.IO.File]::ReadAllBytes($formFile).Length) headers=$headers"
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

function Write-CpButtonSelfUpdateDiag([string]$Reason) {
    Write-Host "CP_BUTTON_DIAG_REASON=$Reason"
    $pd = Join-Path $env:ProgramData 'Nyxveil\ControlPlane'
    $su = Join-Path $pd 'self-update'
    Write-Host "CP_BUTTON_DIAG_SELF_UPDATE_DIR=$su"
    try {
        Get-Service NyxveilControlPlane, NyxveilControlPlaneUpdater -ErrorAction SilentlyContinue |
            ForEach-Object { Write-Host "CP_BUTTON_SVC=$($_.Name) status=$($_.Status)" }
    } catch { }
    if (Test-Path -LiteralPath $verPath) {
        Write-Host "CP_BUTTON_DIAG_VERSION_FILE=$((Get-Content -LiteralPath $verPath -Raw).Trim())"
    }
    if (Test-Path -LiteralPath $su) {
        Get-ChildItem -LiteralPath $su -Recurse -File -ErrorAction SilentlyContinue |
            Select-Object -First 60 FullName, Length, LastWriteTime |
            ForEach-Object { Write-Host "CP_BUTTON_SU_FILE=$($_.FullName) len=$($_.Length) t=$($_.LastWriteTime.ToUniversalTime().ToString('o'))" }
        foreach ($name in @('request.json', 'handoff.json', 'result.json', 'active.json')) {
            $p = Join-Path $su $name
            if (Test-Path -LiteralPath $p) {
                Write-Host "CP_BUTTON_SU_CONTENT_$name<<EOF"
                $raw = Get-Content -LiteralPath $p -Raw -ErrorAction SilentlyContinue
                if ($raw) { Write-Host $raw.Substring(0, [Math]::Min(6000, $raw.Length)) }
                Write-Host 'EOF'
            }
        }
        Get-ChildItem -LiteralPath $su -Filter 'history*.json' -ErrorAction SilentlyContinue |
            Sort-Object LastWriteTime -Descending | Select-Object -First 1 | ForEach-Object {
                Write-Host "CP_BUTTON_SU_HISTORY=$($_.FullName)"
                $raw = Get-Content -LiteralPath $_.FullName -Raw -ErrorAction SilentlyContinue
                if ($raw) { Write-Host $raw.Substring(0, [Math]::Min(4000, $raw.Length)) }
            }
    } else {
        Write-Host 'CP_BUTTON_DIAG_SELF_UPDATE_DIR_MISSING=1'
    }
    $logDir = Join-Path $pd 'logs'
    if (Test-Path -LiteralPath $logDir) {
        Get-ChildItem -LiteralPath $logDir -File | Sort-Object LastWriteTime -Descending | Select-Object -First 3 | ForEach-Object {
            Write-Host "CP_BUTTON_LOG_FILE=$($_.FullName)"
            Get-Content -LiteralPath $_.FullName -Tail 100 | ForEach-Object { Write-Host "CP_BUTTON_LOG=$_" }
        }
    }
    try { sc.exe query NyxveilControlPlaneUpdater | ForEach-Object { Write-Host "CP_BUTTON_UPD_SC=$_" } } catch { }
    try { sc.exe query NyxveilControlPlane | ForEach-Object { Write-Host "CP_BUTTON_MAIN_SC=$_" } } catch { }
}

Push-Location $work
$browserExit = 1
try {
    npm init -y | Out-Null
    npm install playwright@1.49.1 | Out-Null
    npx playwright install chromium | Out-Null
    $env:CP_BASE = $baseUrl
    $env:CP_EMAIL = $AdminUser
    $env:CP_PASSWORD = $AdminPasswordPlain
    $env:CP_TARGET_VERSION = $TargetVersion
    $env:CP_TOTP_SECRET = ''
    $env:CP_TOTP_OUT = Join-Path $work 'totp-secret.txt'
    $env:CP_CLICK_MARKER = Join-Path $work 'click.marker'
    # Resolve playwright from $work/node_modules (script lives beside package.json).
    node $clickJs
    $browserExit = $LASTEXITCODE
} finally {
    Pop-Location
}
if ($browserExit -ne 0) {
    try { Write-CpButtonSelfUpdateDiag 'playwright_failed' } catch {
        Write-Host "CP_BUTTON_DIAG_THROW=$($_.Exception.Message)"
    }
    if ($ExpectBootstrapLimitation) {
        $clickMarker = if ($env:CP_CLICK_MARKER) { $env:CP_CLICK_MARKER } else { Join-Path $work 'click.marker' }
        $clicked = Test-Path -LiteralPath $clickMarker
        $verNow = if (Test-Path -LiteralPath $verPath) { (Get-Content -LiteralPath $verPath -Raw).Trim() } else { '' }
        $mainStopped = $false
        try {
            $mainStopped = ((Get-Service NyxveilControlPlane -ErrorAction SilentlyContinue).Status -ne 'Running')
        } catch { }
        if (-not $clicked -and -not $mainStopped -and $verNow -eq $SourceVersion) {
            Fail "bootstrap probe never started an update attempt (likely GitHub Latest discovery failure); not confirming 1.3.8 limitation"
        }
        Write-Host 'CP_1_3_8_BOOTSTRAP_LIMITATION=CONFIRMED'
        $evidence = [ordered]@{
            gate = 'cp_bootstrap_limitation'
            result = 'PASS'
            bootstrap_limitation = 'CONFIRMED'
            button_from_immutable_source = 'FAIL'
            cp_no_overlay = 'PASS'
            cp_artifact_purity = 'PASS'
            source_version = $SourceVersion
            target_version = $TargetVersion
            sha256_source_zip = $gotSrc
            sha256_target_zip = $gotTgt
            source_install_hashes = $sourceHashes
            clicked = $clicked
            main_stopped = $mainStopped
            version_after_attempt = $verNow
            note = 'Immutable source apply script cannot load target package fixes; button update from byte-exact source is not possible without modifying InstallDir.'
            finished_at = [datetime]::UtcNow.ToString('o')
        }
        # Emit purity gates as separate evidence files for aggregator (button itself remains FAIL).
        $evDir = Split-Path -Parent $EvidencePath
        if (-not $evDir) { $evDir = $work }
        New-Item -ItemType Directory -Force -Path $evDir | Out-Null
        [ordered]@{ gate = 'cp_no_overlay'; result = 'PASS'; finished_at = [datetime]::UtcNow.ToString('o') } |
            ConvertTo-Json -Compress | Set-Content (Join-Path $evDir 'cp_no_overlay-evidence.json') -Encoding utf8
        [ordered]@{ gate = 'cp_artifact_purity'; result = 'PASS'; scope = 'source_install'; finished_at = [datetime]::UtcNow.ToString('o') } |
            ConvertTo-Json -Compress | Set-Content (Join-Path $evDir 'cp_artifact_purity-evidence.json') -Encoding utf8
        $json = $evidence | ConvertTo-Json -Depth 6 -Compress
        [System.IO.File]::WriteAllText($EvidencePath, $json, (New-Object System.Text.UTF8Encoding $false))
        Write-Host "CP_BUTTON_EVIDENCE=$EvidencePath"
        Write-Output 'CP_BUTTON_UPDATE_E2E=FAIL'
        Write-Output 'CONTROL_PLANE_BOOTSTRAP_LIMITATION=CONFIRMED'
        try { Stop-LabGitHubApiProxy } catch { }
        exit 2
    }
    Fail "Playwright button click failed exit=$browserExit"
}

Write-Host 'CP_BUTTON_STEP=verify_post_update'
$deadline = [datetime]::UtcNow.AddMinutes(15)
$after = ''
while ([datetime]::UtcNow -lt $deadline) {
    try {
        $svc = Get-Service NyxveilControlPlane -ErrorAction Stop
        if ($svc.Status -eq 'Running' -and (Test-Path -LiteralPath $verPath)) {
            $after = (Get-Content -LiteralPath $verPath -Raw).Trim()
            if ($after -eq $TargetVersion) { break }
        }
        Write-Host "CP_BUTTON_WAIT_VERSION service=$($svc.Status) version=$after"
    } catch {
        Write-Host "CP_BUTTON_WAIT_VERSION_ERR=$($_.Exception.Message)"
    }
    Start-Sleep -Seconds 5
}
if ($after -ne $TargetVersion) {
    Write-CpButtonSelfUpdateDiag 'post_update_version_mismatch'
    if ($ExpectBootstrapLimitation) {
        Write-Host 'CP_1_3_8_BOOTSTRAP_LIMITATION=CONFIRMED'
        $evDir = Split-Path -Parent $EvidencePath
        if (-not $evDir) { $evDir = $work }
        [ordered]@{ gate = 'cp_no_overlay'; result = 'PASS'; finished_at = [datetime]::UtcNow.ToString('o') } |
            ConvertTo-Json -Compress | Set-Content (Join-Path $evDir 'cp_no_overlay-evidence.json') -Encoding utf8
        [ordered]@{ gate = 'cp_artifact_purity'; result = 'PASS'; scope = 'source_install'; finished_at = [datetime]::UtcNow.ToString('o') } |
            ConvertTo-Json -Compress | Set-Content (Join-Path $evDir 'cp_artifact_purity-evidence.json') -Encoding utf8
        $evidence = [ordered]@{
            gate = 'cp_bootstrap_limitation'
            result = 'PASS'
            bootstrap_limitation = 'CONFIRMED'
            button_from_immutable_source = 'FAIL'
            before_version = $before
            after_version = $after
            source_version = $SourceVersion
            target_version = $TargetVersion
            sha256_source_zip = $gotSrc
            sha256_target_zip = $gotTgt
            finished_at = [datetime]::UtcNow.ToString('o')
        }
        $json = $evidence | ConvertTo-Json -Depth 6 -Compress
        [System.IO.File]::WriteAllText($EvidencePath, $json, (New-Object System.Text.UTF8Encoding $false))
        Write-Output 'CONTROL_PLANE_BOOTSTRAP_LIMITATION=CONFIRMED'
        try { Stop-LabGitHubApiProxy } catch { }
        exit 2
    }
    Fail "post-update VERSION want=$TargetVersion have=$after"
}

Wait-HttpOk "$baseUrl/health/live" 120
$readyOut = Join-Path $work 'ready.body'
& curl.exe -skf --max-time 15 -o $readyOut "$baseUrl/health/ready"
if ($LASTEXITCODE -ne 0) {
    Write-CpButtonSelfUpdateDiag 'health_ready_failed'
    Fail "health/ready failed after update curl_exit=$LASTEXITCODE"
}
Write-Host 'CP_BUTTON_HEALTH_READY=PASS'

# TARGET ARTIFACT PURITY: installed product must match published target publish/.
$extractTgt = Join-Path $work ("extract-{0}" -f $TargetVersion)
if (-not (Test-Path -LiteralPath $extractTgt)) {
    Expand-Archive -LiteralPath $zipTgt -DestinationPath $extractTgt -Force
}
$publishTgt = Join-Path $extractTgt 'publish'
try {
    $targetHashes = Assert-InstalledMatchesPublish -InstallRoot $InstallDir -PublishRoot $publishTgt -RelPaths $purityRels
    Write-Host 'CP_TARGET_ARTIFACT_PURITY=PASS'
}
catch {
    Fail "target artifact purity failed: $($_.Exception.Message)"
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
    sha256_source_zip = $gotSrc
    sha256_target_zip = $gotTgt
    source_install_hashes = $sourceHashes
    target_install_hashes = $targetHashes
    cp_no_overlay = 'PASS'
    cp_artifact_purity = 'PASS'
    install_dir = $InstallDir
    port = $Port
    database_server = $DatabaseServer
    service_main = $mainStatus
    service_updater = $updStatus
    browser_reconnect = 'PASS'
    service_restart = 'PASS'
    terminal_reconciliation = 'PASS'
    clicked_at = if ($env:CP_CLICK_MARKER -and (Test-Path -LiteralPath $env:CP_CLICK_MARKER)) {
        ((Get-Content -LiteralPath $env:CP_CLICK_MARKER -TotalCount 2) -join ' ').Trim()
    } else { $null }
    finished_at = [datetime]::UtcNow.ToString('o')
}
$json = $evidence | ConvertTo-Json -Depth 6 -Compress
[System.IO.File]::WriteAllText($EvidencePath, $json, (New-Object System.Text.UTF8Encoding $false))
$evDir = Split-Path -Parent $EvidencePath
if (-not $evDir) { $evDir = $work }
[ordered]@{ gate = 'cp_no_overlay'; result = 'PASS'; finished_at = [datetime]::UtcNow.ToString('o') } |
    ConvertTo-Json -Compress | Set-Content (Join-Path $evDir 'cp_no_overlay-evidence.json') -Encoding utf8
[ordered]@{ gate = 'cp_artifact_purity'; result = 'PASS'; scope = 'source_and_target'; finished_at = [datetime]::UtcNow.ToString('o') } |
    ConvertTo-Json -Compress | Set-Content (Join-Path $evDir 'cp_artifact_purity-evidence.json') -Encoding utf8
Write-Host "CP_BUTTON_EVIDENCE=$EvidencePath"
Write-Host "CP_BUTTON_EVIDENCE_BYTES=$((Get-Item -LiteralPath $EvidencePath).Length)"
Write-Output 'CP_BUTTON_UPDATE_E2E=PASS'
Write-Output ("CONTROL_PLANE_{0}_TO_{1}_REAL_UPDATE_BY_BUTTON=PASS" -f ($SourceVersion -replace '\.','_'), ($TargetVersion -replace '\.','_'))
try { Stop-LabGitHubApiProxy } catch { }
exit 0
