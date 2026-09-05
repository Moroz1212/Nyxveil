#Requires -Version 5.1
<#
.SYNOPSIS
  Self-test Nyxveil Control Plane Windows deployment. Exit non-zero on failure.

.DESCRIPTION
  Prefers published exe self-test (full host checks). Expands with PowerShell checks:
  service, identity, port ownership by our PID, firewall, cert store thumbprint+private key,
  DB auth mode, SuperAdmin exists, setup disabled, KEK file, signing key via CLI.
  Sql auth uses Invoke-NyxveilSql (not always -E).
#>
[CmdletBinding()]
param(
    [string]$InstallDir = ''
)

$ErrorActionPreference = 'Continue'
Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force

$failures = New-Object System.Collections.Generic.List[string]
$warnings = New-Object System.Collections.Generic.List[string]

function Ok([string]$Msg) { Write-Host "[PASS] $Msg" -ForegroundColor Green }
function Fail([string]$Msg) { Write-Host "[FAIL] $Msg" -ForegroundColor Red; [void]$failures.Add($Msg) }
function Warn([string]$Msg) { Write-Host "[WARN] $Msg" -ForegroundColor Yellow; [void]$warnings.Add($Msg) }

function Get-OptionalProperty($Object, [string]$Name) {
    if ($null -eq $Object) { return $null }
    $property = $Object.PSObject.Properties[$Name]
    if ($null -ne $property) { return $property.Value }
    return $null
}

Write-Host '=== Nyxveil Control Plane self-test ==='

$op = $null
try {
    $op = Read-OperationalConfig
    Ok "operational.json loaded (Port=$($op.Port))"
}
catch {
    Fail "operational.json: $($_.Exception.Message)"
    Write-Host "Failures: $($failures.Count)"
    exit 1
}

$ServiceName = [string]$op.ServiceName
if ([string]::IsNullOrWhiteSpace($InstallDir)) { $InstallDir = [string]$op.InstallDir }
$Port = [int]$op.Port
$BindAddress = [string]$op.BindAddress
$SecretsDir = [string]$op.SecretsDir
$DataDir = [string]$op.DataDir
$LogsDir = [string]$op.LogsDir
$thumb = [string]$op.CertificateThumbprint
$fw = [string]$op.FirewallRuleName
$dbServer = [string]$op.DatabaseServer
$dbName = [string]$op.DatabaseName
$dbAuth = [string]$op.DatabaseAuth
$certMode = [string]$op.CertificateMode
$publicHostname = [string]$op.PublicHostname
$serviceAccount = [string]$op.ServiceAccount
if ([string]::IsNullOrWhiteSpace($publicHostname)) { $publicHostname = 'localhost' }

# Prefer full exe self-test
$exe = Join-Path $InstallDir 'Nyxveil.ControlPlane.Web.exe'
if (Test-Path -LiteralPath $exe) {
    if (Invoke-NyxveilSelfTestCli -InstallDir $InstallDir -PublicHostname $publicHostname) {
        Ok 'Published exe self-test passed'
    }
    else {
        Fail 'Published exe self-test failed'
    }
}
else {
    Warn "Web exe not found at $exe - continuing with PowerShell checks only"
}

# Service installed / running / identity
$svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if (-not $svc) { Fail "Service '$ServiceName' is not installed." }
else {
    Ok "Service installed: $ServiceName"
    if ($svc.Status -eq 'Running') { Ok 'Service running' }
    else { Fail "Service status is $($svc.Status) (expected Running)" }

    try {
        $cim = Get-CimInstance Win32_Service -Filter "Name='$ServiceName'" -ErrorAction Stop
        $startName = [string]$cim.StartName
        if ($serviceAccount -and $startName -and ($startName -ieq $serviceAccount -or $startName -ieq ($serviceAccount -replace '^NT SERVICE\\', '.\'))) {
            Ok "Service identity: $startName"
        }
        elseif ($startName) {
            Warn "Service StartName=$startName (operational ServiceAccount=$serviceAccount)"
        }
    }
    catch {
        Warn "Could not read service identity: $($_.Exception.Message)"
    }

    # Service SID must resolve (virtual account / gMSA)
    try {
        $acctForSid = if ($serviceAccount) { $serviceAccount } else { "NT SERVICE\$ServiceName" }
        $nt = New-Object System.Security.Principal.NTAccount($acctForSid)
        $sid = $nt.Translate([System.Security.Principal.SecurityIdentifier])
        Ok "Service SID resolves: $acctForSid -> $($sid.Value)"
    }
    catch {
        Fail "Service SID does not resolve for '$serviceAccount': $($_.Exception.Message)"
    }

    # Service-specific Environment REG_MULTI_SZ (not machine-wide)
    $envKey = "HKLM:\SYSTEM\CurrentControlSet\Services\$ServiceName"
    try {
        $envProp = Get-ItemProperty -LiteralPath $envKey -Name Environment -ErrorAction Stop
        $envLines = @($envProp.Environment)
        $hasAsp = $false
        $hasDotnet = $false
        foreach ($line in $envLines) {
            if ($line -match '(?i)^ASPNETCORE_ENVIRONMENT=Production$') { $hasAsp = $true }
            if ($line -match '(?i)^DOTNET_ENVIRONMENT=Production$') { $hasDotnet = $true }
        }
        if ($hasAsp -and $hasDotnet) {
            Ok 'Service Environment REG_MULTI_SZ has ASPNETCORE_ENVIRONMENT=Production and DOTNET_ENVIRONMENT=Production'
        }
        else {
            Fail "Service Environment missing Production entries. Found: $($envLines -join ' | ')"
        }
    }
    catch {
        Fail "Service Environment registry value missing under $envKey : $($_.Exception.Message)"
    }
}

# Machine-wide ASPNETCORE_ENVIRONMENT: WARN if present (installer must not set it)
$machineAspNet = [Environment]::GetEnvironmentVariable('ASPNETCORE_ENVIRONMENT', 'Machine')
if (-not [string]::IsNullOrWhiteSpace($machineAspNet)) {
    Warn "Machine-wide ASPNETCORE_ENVIRONMENT=$machineAspNet is set. Installer must not set this; prefer service-specific Environment registry."
}
else {
    Ok 'Machine-wide ASPNETCORE_ENVIRONMENT is not set'
}

try {
    Assert-ValidPort -Port $Port
    Ok "Configured port valid: $Port"
}
catch { Fail $_.Exception.Message }

$listeners = @(Get-PortOccupancyDetails -Port $Port)
if ($listeners.Count -eq 0) {
    Fail "Configured port $Port is not listening."
}
else {
    if (Test-PortOwnedByService -Port $Port -ServiceName $ServiceName -BindAddress $BindAddress) {
        Ok "Port $Port owned by service $ServiceName"
    }
    else {
        $ours = $false
        foreach ($l in $listeners) {
            Write-Host ("  Listener PID={0} Process={1} Service={2}" -f $l.Pid, $l.ProcessName, $l.OwningService)
            if ($l.OwningService -eq $ServiceName -or $l.ProcessName -match 'Nyxveil') { $ours = $true }
        }
        if ($ours) { Warn "Port $Port has Nyxveil-related listener but ownership map is ambiguous." }
        else { Fail "Port $Port is not owned by $ServiceName (foreign listener)." }
    }
}

# Certificate store + private key + hostname / trust policy
$certValidationMode = ''
try { $certValidationMode = [string]$op.CertificateValidationMode } catch { }
if ([string]::IsNullOrWhiteSpace($certValidationMode)) { $certValidationMode = 'SystemTrust' }

if ($thumb) {
    $cert = Get-CertificateFromStore -Thumbprint $thumb
    if (-not $cert) {
        Fail "Certificate thumbprint $thumb not found in LocalMachine\My."
    }
    else {
        try {
            Assert-ValidServerCertificate -Certificate $cert -PublicHostname $publicHostname `
                -CertificateValidationMode $certValidationMode -InstallDir $InstallDir
            Ok "Certificate thumbprint=$($cert.Thumbprint) private key + validity + hostname OK ($certValidationMode)"
        }
        catch {
            Fail $_.Exception.Message
        }
        $sum = Get-CertificateSummary -Thumbprint $thumb
        if ($sum) {
            $exp = $sum.NotAfter.ToString('u')
            $daysLeft = [int]$sum.DaysLeft
            Ok ('Certificate Subject={0} expires {1}; daysLeft={2}' -f $sum.Subject, $exp, $daysLeft)
            if ($daysLeft -lt 30) { Warn ('Certificate expires in {0} days.' -f $daysLeft) }
        }
        Ok ("Certificate trust policy: CertificateValidationMode={0}" -f $certValidationMode)
        if ($certMode -ne 'Store') {
            Warn ('operational CertificateMode={0} - production runtime should be Store+thumbprint.' -f $certMode)
        }
    }
}
else {
    Fail 'CertificateThumbprint missing from operational.json'
}

# Hostname-aware HTTP (no TrustAll). SelfSigned may skip HTTP if CLI already passed.
if (-not (Test-HttpsHealthLocal -Port $Port -PublicHostname $publicHostname -InstallDir $InstallDir `
            -CertificateMode $certMode -CertificateValidationMode $certValidationMode)) {
    Fail "Local health gate failed for $publicHostname :$Port"
}
else {
    Ok ("Local health gate OK for {0} port {1}" -f $publicHostname, $Port)
}

foreach ($pair in @(@{N = 'DataDir'; P = $DataDir }, @{N = 'LogsDir'; P = $LogsDir })) {
    if (-not $pair.P -or -not (Test-Path $pair.P)) {
        Fail "$($pair.N) missing: $($pair.P)"
        continue
    }
    $probe = Join-Path $pair.P ("selftest-{0}.tmp" -f [guid]::NewGuid().ToString('N'))
    try {
        'ok' | Set-Content -LiteralPath $probe -Encoding ASCII
        Remove-Item -LiteralPath $probe -Force
        Ok "$($pair.N) writable: $($pair.P)"
    }
    catch {
        Fail "$($pair.N) not writable: $($pair.P)"
    }
}

if ($fw) {
    $rule = Get-NetFirewallRule -DisplayName $fw -ErrorAction SilentlyContinue
    if ($rule) {
        $pf = Get-NetFirewallPortFilter -AssociatedNetFirewallRule $rule
        if ($pf -and [string]$pf.LocalPort -eq "$Port") {
            Ok "Firewall rule '$fw' allows TCP $Port"
        }
        else {
            Fail "Firewall rule '$fw' LocalPort does not match configured port $Port"
        }
    }
    else {
        Fail "Firewall rule not found: $fw"
    }
}
else {
    Warn 'FirewallRuleName empty in operational.json (install may have used -SkipFirewall).'
}

$kekPath = Join-Path $SecretsDir 'license-kek.dpapi'
if (Test-Path $kekPath) {
    try {
        $kek = Read-ProtectedSecret -Path $kekPath
        if ($kek -and $kek.Length -eq 64) { Ok 'License verifier secret (KEK) present and DPAPI-readable' }
        else { Fail 'License KEK present but unexpected length after unprotect.' }
        $kek = $null
    }
    catch {
        Fail "License KEK DPAPI unprotect failed: $($_.Exception.Message)"
    }
}
else {
    Fail "Missing $kekPath"
}

# Setup disabled in production appsettings
$appsettingsPath = Join-Path $InstallDir 'appsettings.Production.json'
if (Test-Path $appsettingsPath) {
    try {
        $settings = Get-Content -LiteralPath $appsettingsPath -Raw -Encoding UTF8 | ConvertFrom-Json
        $setupSettings = Get-OptionalProperty $settings 'Setup'
        if ((Get-OptionalProperty $setupSettings 'AllowWebBootstrap') -eq $true) {
            Fail 'Setup:AllowWebBootstrap is true in Production (must be false).'
        }
        else {
            Ok 'Setup web bootstrap disabled (or absent) in appsettings.Production.json'
        }
        $kestrelSettings = Get-OptionalProperty $settings 'Kestrel'
        if (Get-OptionalProperty $kestrelSettings 'Endpoints') {
            Warn 'Legacy Kestrel:Endpoints present — Hosting section is SoT; consider removing Endpoints.'
        }
        $certificateSettings = Get-OptionalProperty $settings 'Certificate'
        $configuredMode = [string](Get-OptionalProperty $certificateSettings 'Mode')
        if ($configuredMode -ne 'Store') {
            Fail "appsettings Certificate:Mode=$configuredMode (expected Store with thumbprint)."
        }
        elseif ([string]::IsNullOrWhiteSpace([string](Get-OptionalProperty $certificateSettings 'Thumbprint'))) {
            Fail 'appsettings Certificate:Thumbprint is empty.'
        }
        else {
            Ok 'Certificate Mode=Store with thumbprint in appsettings.Production.json'
        }
        $configuredValidation = Get-OptionalProperty $certificateSettings 'ValidationMode'
        if ($configuredValidation) {
            Ok ("Certificate ValidationMode={0}" -f $configuredValidation)
        }
        $cs = ''
        try { $cs = [string]$settings.ConnectionStrings.ControlPlane } catch { }
        if ($cs) {
            $encrypt = if ($cs -match '(?i)Encrypt=(True|False)') { $Matches[1] } else { '?' }
            $trust = if ($cs -match '(?i)TrustServerCertificate=(True|False)') { $Matches[1] } else { '?' }
            # Display policy only — never print full connection string (may contain User ID).
            Ok ("SQL connection policy: Encrypt={0}; TrustServerCertificate={1}" -f $encrypt, $trust)
        }
    }
    catch {
        Fail "Could not validate appsettings.Production.json: $($_.Exception.Message)"
    }
}
else {
    Fail "Missing $appsettingsPath"
}

# DB connectivity with correct auth mode
$sqlUser = ''
$sqlPassword = $null
if ($dbAuth -eq 'Sql') {
    $sqlPassPath = Join-Path $SecretsDir 'sql-password.dpapi'
    if (-not (Test-Path $sqlPassPath)) {
        Fail 'SQL Auth configured but sql-password.dpapi missing.'
    }
    else {
        try {
            $plain = Read-ProtectedSecret -Path $sqlPassPath
            $sqlPassword = ConvertTo-SecureString $plain -AsPlainText -Force
            $plain = $null
            if (Test-Path $appsettingsPath) {
                $settings = Get-Content -LiteralPath $appsettingsPath -Raw -Encoding UTF8 | ConvertFrom-Json
                $cs = [string]$settings.ConnectionStrings.ControlPlane
                if ($cs -match '(?i)User ID=([^;]+)') { $sqlUser = $Matches[1] }
            }
            if ([string]::IsNullOrWhiteSpace($sqlUser)) {
                Fail 'SQL Auth: could not resolve User ID from appsettings.'
            }
            else {
                Ok "SQL Auth mode will use User ID=$sqlUser (password via DPAPI / SQLCMDPASSWORD)"
            }
        }
        catch {
            Fail "SQL Auth secret load failed: $($_.Exception.Message)"
        }
    }
}
else {
    Ok 'Database auth mode: Windows'
}

if (Test-SqlCmdAvailable) {
    try {
        Assert-ValidDatabaseName -DatabaseName $dbName
        $q = "SET NOCOUNT ON; SELECT DB_ID(N'$($dbName.Replace("'","''"))') AS DbId;"
        $tmpOut = Join-Path $env:TEMP ("nyxveil-selftest-db-{0}.txt" -f [guid]::NewGuid().ToString('N'))
        Invoke-NyxveilSql -Server $dbServer -Query $q -DatabaseName $dbName `
            -DatabaseAuth $dbAuth -DatabaseUser $sqlUser -DatabasePassword $sqlPassword `
            -ExtraArgs @('-h', '-1', '-W', '-o', $tmpOut)
        $dbIdLine = (Get-Content $tmpOut -ErrorAction SilentlyContinue | Where-Object { $_ -match '\d+' } | Select-Object -First 1)
        Remove-Item $tmpOut -Force -ErrorAction SilentlyContinue
        if ($dbIdLine -match '\d+' -and [int]$Matches[0] -gt 0) {
            Ok "Database reachable: $dbName ($dbAuth auth)"
        }
        else {
            Fail "Database not found or unreachable: $dbName on $dbServer"
        }

        $adminQ = @"
USE [$dbName];
SET NOCOUNT ON;
SELECT COUNT(*) FROM dbo.AspNetUserRoles ur
INNER JOIN dbo.AspNetRoles r ON r.Id = ur.RoleId
WHERE r.Name = N'SuperAdmin';
"@
        $adminOut = Join-Path $env:TEMP ("nyxveil-selftest-admin-{0}.txt" -f [guid]::NewGuid().ToString('N'))
        Invoke-NyxveilSql -Server $dbServer -Query $adminQ -DatabaseName $dbName `
            -DatabaseAuth $dbAuth -DatabaseUser $sqlUser -DatabasePassword $sqlPassword `
            -ExtraArgs @('-h', '-1', '-W', '-o', $adminOut)
        $countLine = (Get-Content $adminOut -ErrorAction SilentlyContinue | Where-Object { $_ -match '^\s*\d+\s*$' } | Select-Object -First 1)
        Remove-Item $adminOut -Force -ErrorAction SilentlyContinue
        $count = 0
        if ($countLine) { [void][int]::TryParse($countLine.Trim(), [ref]$count) }
        if ($count -gt 0) { Ok "SuperAdmin exists (count=$count)" }
        else { Fail 'No SuperAdmin role assignment found.' }
    }
    catch {
        Fail "DB self-test: $($_.Exception.Message)"
    }
}
else {
    Fail 'sqlcmd not available; cannot verify DB connectivity.'
}

Write-Host ''
Write-Host "Warnings: $($warnings.Count)"
Write-Host "Failures: $($failures.Count)"
if ($failures.Count -gt 0) {
    exit 1
}
exit 0
