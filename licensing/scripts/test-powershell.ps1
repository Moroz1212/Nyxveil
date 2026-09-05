#Requires -Version 5.1
<#
.SYNOPSIS
  Static validation for Nyxveil Control Plane PowerShell deployment scripts.
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$scriptRoot = $PSScriptRoot
$failures = New-Object System.Collections.Generic.List[string]

function Fail([string]$Msg) {
    Write-Host "[FAIL] $Msg" -ForegroundColor Red
    [void]$failures.Add($Msg)
}
function Ok([string]$Msg) { Write-Host "[PASS] $Msg" -ForegroundColor Green }

Write-Host '=== test-powershell.ps1 ==='

$files = @(Get-ChildItem -LiteralPath $scriptRoot -Include *.ps1, *.psm1 -File)
if ($files.Count -eq 0) {
    # -Include with -LiteralPath on directory can be quirky; fallback
    $files = @(Get-ChildItem -Path (Join-Path $scriptRoot '*') -Include *.ps1, *.psm1 -File)
}

Write-Host "Parsing $($files.Count) script(s)..."
foreach ($f in $files) {
    $tokens = $null
    $errors = $null
    $null = [System.Management.Automation.Language.Parser]::ParseFile($f.FullName, [ref]$tokens, [ref]$errors)
    if ($errors -and $errors.Count -gt 0) {
        foreach ($e in $errors) {
            Fail ("ParseFile {0}: {1}" -f $f.Name, $e.Message)
        }
    }
    else {
        Ok "ParseFile $($f.Name)"
    }
}

$modulePath = Join-Path $scriptRoot 'Nyxveil.ControlPlane.Deploy.psm1'
if (-not (Test-Path $modulePath)) {
    Fail "Missing module $modulePath"
}
else {
    try {
        Import-Module $modulePath -Force -ErrorAction Stop
        Ok 'Import-Module Nyxveil.ControlPlane.Deploy.psm1'
    }
    catch {
        Fail "Import-Module failed: $($_.Exception.Message)"
    }
}

# Duplicate parameter name AST check for each script
foreach ($f in $files) {
    $tokens = $null
    $errors = $null
    $ast = [System.Management.Automation.Language.Parser]::ParseFile($f.FullName, [ref]$tokens, [ref]$errors)
    if (-not $ast) { continue }

    $paramBlocks = $ast.FindAll({
            param($node)
            $node -is [System.Management.Automation.Language.ParamBlockAst]
        }, $true)

    foreach ($pb in $paramBlocks) {
        $names = @()
        foreach ($p in $pb.Parameters) {
            $n = $p.Name.VariablePath.UserPath
            if ($names -contains $n) {
                Fail "Duplicate parameter -$n in $($f.Name)"
            }
            else {
                $names += $n
            }
        }
    }
}
Ok 'Duplicate parameter AST check completed'

# Get-Help smoke for key scripts
$helpTargets = @(
    'install-windows.ps1',
    'update-windows.ps1',
    'self-test.ps1',
    'backup-db.ps1',
    'change-port.ps1',
    'uninstall-windows.ps1',
    'pack-release.ps1',
    'backup-recovery.ps1',
    'restore-recovery.ps1',
    'test-core-interop.ps1'
)
foreach ($name in $helpTargets) {
    $path = Join-Path $scriptRoot $name
    if (-not (Test-Path $path)) {
        Fail "Missing script for Get-Help: $name"
        continue
    }
    try {
        $h = Get-Help $path -ErrorAction Stop
        if (-not $h) { Fail "Get-Help returned empty for $name" }
        else { Ok "Get-Help $name" }
    }
    catch {
        Fail "Get-Help $name : $($_.Exception.Message)"
    }
}

# Spot-check critical helpers exist after import
$required = @(
    'Write-AppsettingsProduction',
    'Invoke-NyxveilSql',
    'Get-NyxveilSqlcmdArgs',
    'Test-HttpsHealthLocal',
    'Get-HealthTarget',
    'Test-RemoteWindowsAuthSupported',
    'Resolve-TrustSqlServerCertificatePolicy',
    'Get-NyxveilDatabaseSettings',
    'Invoke-NativeChecked',
    'Ensure-NyxveilServiceSid',
    'Set-NyxveilServiceEnvironment',
    'Initialize-NyxveilRestrictedDirectory',
    'Test-CertificateMatchesHostname',
    'Test-TcpPortAvailable'
)
foreach ($fn in $required) {
    if (Get-Command $fn -ErrorAction SilentlyContinue) { Ok "Command $fn" }
    else { Fail "Missing exported command $fn" }
}

# Static: installer must not set machine-wide ASPNETCORE/DOTNET env
$installPath = Join-Path $scriptRoot 'install-windows.ps1'
$deployPath = Join-Path $scriptRoot 'Nyxveil.ControlPlane.Deploy.psm1'
$installText = Get-Content -LiteralPath $installPath -Raw -Encoding UTF8
$deployText = Get-Content -LiteralPath $deployPath -Raw -Encoding UTF8
if ($installText -match "SetEnvironmentVariable\(['\`"]ASPNETCORE_ENVIRONMENT['\`"]") {
    Fail 'install-windows.ps1 must not call SetEnvironmentVariable ASPNETCORE_ENVIRONMENT'
}
elseif ($installText -match "SetEnvironmentVariable\(['\`"]DOTNET_ENVIRONMENT['\`"]") {
    Fail 'install-windows.ps1 must not call SetEnvironmentVariable DOTNET_ENVIRONMENT'
}
elseif ($installText -match "SetEnvironmentVariable\([^)]+,\s*['\`"]Machine['\`"]\s*\)") {
    Fail 'install-windows.ps1 must not SetEnvironmentVariable(..., Machine)'
}
else {
    Ok 'Installer does not set machine-wide ASPNETCORE/DOTNET environment'
}

if ($deployText -match 'function Set-NyxveilServiceEnvironment' -and $installText -match 'Set-NyxveilServiceEnvironment') {
    Ok 'Service-specific Environment helper used by installer'
}
else {
    Fail 'Set-NyxveilServiceEnvironment missing from module or installer'
}

# Static: Fresh order — service create before SID / ACLs / cert / SQL / start
$orderNames = @(
    'New-NyxveilWindowsService',
    'Ensure-NyxveilServiceSid',
    'Set-NyxveilServiceEnvironment',
    'Set-NyxveilDirectoryAcls',
    'Grant-CertificatePrivateKeyAccess',
    'Grant-SqlLoginForServiceAccount',
    'Start-Service -Name $ServiceName'
)
$lastIdx = -1
$orderOk = $true
foreach ($name in $orderNames) {
    $idx = $installText.IndexOf($name)
    if ($idx -lt 0) {
        Fail "Install order marker missing: $name"
        $orderOk = $false
        break
    }
    if ($idx -le $lastIdx) {
        Fail "Install order violated: $name appears before a prior step"
        $orderOk = $false
        break
    }
    $lastIdx = $idx
}
if ($orderOk) { Ok 'Install order: service create → SID → env → ACLs → cert → SQL → Start-Service' }

Write-Host ''
Write-Host "Failures: $($failures.Count)"
if ($failures.Count -gt 0) {
    exit 1
}
Write-Host 'All PowerShell deployment script checks passed.'
exit 0
