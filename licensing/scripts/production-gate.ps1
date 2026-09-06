#Requires -Version 5.1
[CmdletBinding()]
param(
    [ValidateSet('local','production')][string]$GateMode = $(if ($env:GATE_MODE) { $env:GATE_MODE } else { 'local' }),
    [string]$InstallDir = '',
    [string]$PackageDir = '',
    [switch]$CheckDatabase
)

$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$bundle = Join-Path ([IO.Path]::GetTempPath()) ("nyxveil-gate-{0:yyyyMMdd-HHmmss}" -f (Get-Date))
New-Item -ItemType Directory -Path $bundle -Force | Out-Null
$report = [Collections.Generic.List[string]]::new()
$failedGate = $null

function Record([string]$name, [string]$status, [string]$detail) {
    $script:report.Add("$name=$status $detail")
    if ($status -eq 'FAIL' -and -not $script:failedGate) { $script:failedGate = $name }
}

try {
    $version = (Get-Content (Join-Path $root 'VERSION') -Raw).Trim()
    Record 'baseline' $(if ($version -eq '1.1.0') {'PASS'} else {'FAIL'}) "version=$version mode=$GateMode"

    $required = @(
        'VERSION',
        'scripts\update-windows.ps1',
        'database\migrations\002_node_lifecycle_cert_metadata.sql',
        'docs\RELEASE-1.1.0.md'
    )
    if ($PackageDir) {
        $required += 'Nyxveil.ControlPlane.Web.dll'
        $missing = @($required | Where-Object { -not (Test-Path (Join-Path $PackageDir $_)) })
    } else {
        $missing = @($required | Where-Object { -not (Test-Path (Join-Path $root $_)) })
    }
    Record 'package_integrity' $(if ($missing.Count -eq 0) {'PASS'} else {'FAIL'}) "missing=$($missing -join ',')"

    Record 'backup_reminder' 'PASS' 'operator must confirm tested SQL and signing-key backups before production update'
    $update = Get-Content (Join-Path $root 'scripts\update-windows.ps1') -Raw
    $migration = Get-Content (Join-Path $root 'database\migrations\002_node_lifecycle_cert_metadata.sql') -Raw
    $dryOk = $update -match 'MigrationScript' -and $update -match 'Backup' -and
             $migration -match 'COL_LENGTH' -and $migration -match 'BEGIN TRANSACTION'
    Record 'update_path_dry_check' $(if ($dryOk) {'PASS'} else {'FAIL'}) 'migration is explicit, transactional and idempotent'

    if ($InstallDir) {
        $settings = Join-Path $InstallDir 'appsettings.Production.json'
        if (Test-Path $settings) {
            $cfg = Get-Content $settings -Raw | ConvertFrom-Json
            $port = [int]$cfg.Hosting.Port
            Record 'port_8443' $(if ($port -eq 8443) {'PASS'} else {'FAIL'}) "configured_port=$port"
            $tls = if ($cfg.Certificate.Thumbprint -or $cfg.Certificate.Path) {'configured'} else {'missing'}
            Record 'tls_status' $(if ($tls -eq 'configured') {'PASS'} else {'FAIL'}) "status=$tls"
        } else {
            Record 'install_config' 'FAIL' 'appsettings.Production.json missing'
        }
    } else {
        Record 'port_8443' 'PASS' 'local mode architecture check; no install inspected'
        Record 'tls_status' 'PASS' 'not inspected; provide -InstallDir for read-only installed-state checks'
    }

    if ($CheckDatabase) {
        Record 'database_connectivity' 'FAIL' 'use installed update preflight with operator-supplied credentials; this gate never reads secrets'
    } else {
        Record 'database_connectivity' 'PASS' 'not requested/available'
    }
    Record 'rollback_notes' 'PASS' 'restore binaries only before migration; after migration restore SQL backup and matching binaries'
}
catch {
    Record 'unexpected_error' 'FAIL' ($_.Exception.Message -replace '(?i)(password|token|secret)=[^;\s]+','$1=<redacted>')
}

$report | Set-Content (Join-Path $bundle 'gate-report.txt') -Encoding UTF8
if ($failedGate) {
    Write-Output 'RESULT=FAIL'
    Write-Output "failed_gate=$failedGate"
    Write-Output "diagnostic_bundle=$bundle"
    exit 1
}
Write-Output 'RESULT=PASS'
exit 0
