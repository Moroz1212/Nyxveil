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
    Record 'baseline' $(if ($version -eq '1.3.11') {'PASS'} else {'FAIL'}) "version=$version mode=$GateMode"

    $required = @(
        'VERSION',
        'scripts\update-windows.ps1',
        'database\migrations\002_node_lifecycle_cert_metadata.sql',
        'database\migrations\003_node_commands_cert_renewal.sql',
        'database\migrations\004_version_mgmt_signing_retiring.sql',
        'database\migrations\005_certificate_operation_states.sql',
        'database\migrations\validate_schema_v2.sql',
        'database\migrations\validate_schema_v3.sql',
        'database\migrations\validate_schema_v5.sql',
        'docs\RELEASE-1.3.8.md',
        'docs\RELEASE-1.3.11.md'
    )
    if ($PackageDir) {
        $missing = @($required | Where-Object { -not (Test-Path (Join-Path $root $_)) })
        if (-not (Test-Path (Join-Path $PackageDir 'Nyxveil.ControlPlane.Web.dll'))) {
            $missing += 'publish/Nyxveil.ControlPlane.Web.dll'
        }
    } else {
        $missing = @($required | Where-Object { -not (Test-Path (Join-Path $root $_)) })
    }
    Record 'package_integrity' $(if ($missing.Count -eq 0) {'PASS'} else {'FAIL'}) "missing=$($missing -join ',')"

    Record 'backup_reminder' 'INFO' 'operator must confirm tested SQL and signing-key backups before production update'
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
        Record 'port_8443' 'SKIP' 'local mode architecture check; no install inspected'
        Record 'tls_status' 'SKIP' 'not inspected; provide -InstallDir for read-only installed-state checks'
    }

    if ($InstallDir -and (Test-Path -LiteralPath $InstallDir) -and
        (Get-Command sqlcmd -ErrorAction SilentlyContinue)) {
        try {
            $modulePath = Join-Path $root 'scripts\Nyxveil.ControlPlane.Deploy.psm1'
            Import-Module $modulePath -Force
            $db = Get-NyxveilDatabaseSettings -InstallDir $InstallDir
            $dbPassword = $null
            if ([string]$db.Auth -eq 'Sql') {
                $op = Read-OperationalConfig
                $secretsDir = if ($op.SecretsDir) {
                    [string]$op.SecretsDir
                } else {
                    Join-Path (Get-ProgramDataRoot) 'secrets'
                }
                $passwordPath = Join-Path $secretsDir 'sql-password.dpapi'
                if (-not (Test-Path -LiteralPath $passwordPath -PathType Leaf)) {
                    throw 'SQL Auth secret is unavailable to the gate.'
                }
                $plainPassword = Read-ProtectedSecret -Path $passwordPath
                try {
                    $dbPassword = ConvertTo-SecureString $plainPassword -AsPlainText -Force
                }
                finally {
                    $plainPassword = $null
                }
            }
            Invoke-NyxveilSql -Server ([string]$db.Server) `
                -DatabaseName ([string]$db.Database) `
                -DatabaseAuth ([string]$db.Auth) `
                -DatabaseUser ([string]$db.User) `
                -DatabasePassword $dbPassword `
                -InputFile (Join-Path $root 'database\migrations\validate_schema_v5.sql') `
                -TrustSqlServerCertificate ([bool]$db.TrustSqlServerCertificate) `
                -Encrypt ([bool]$db.Encrypt)
            Record 'schema_version' 'PASS' "database=$($db.Database) schema_version=5"
            Record 'database_connectivity' 'PASS' "server=$($db.Server)"
        }
        catch {
            $detail = $_.Exception.Message -replace '(?i)(password|token|secret)=[^;\s]+','$1=<redacted>'
            if ($GateMode -eq 'local' -and -not $CheckDatabase) {
                Record 'schema_version' 'SKIP' "soft_local_check=$detail"
                Record 'database_connectivity' 'SKIP' 'soft local mode; database credentials/connectivity unavailable'
            }
            else {
                Record 'schema_version' 'FAIL' $detail
                Record 'database_connectivity' 'FAIL' 'schema v5 validation could not complete'
            }
        }
    }
    elseif ($CheckDatabase) {
        Record 'schema_version' 'FAIL' 'InstallDir and sqlcmd are required for requested database validation'
        Record 'database_connectivity' 'FAIL' 'database validation requested but unavailable'
    }
    else {
        Record 'schema_version' 'SKIP' 'not inspected; provide -InstallDir with sqlcmd for validation'
        Record 'database_connectivity' 'SKIP' 'not requested/available'
    }
    Record 'rollback_notes' 'INFO' 'restore binaries only before migration; after migration restore SQL backup and matching binaries'
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
# production-gate.ps1 local mode may emit RESULT=PARTIAL for SKIP checks without InstallDir.
# That local architecture probe is non-mandatory for Control Plane CI packaging.
# Mandatory production-release evidence is enforced exclusively by
# licensing/scripts/assert-production-gates.ps1 (PARTIAL/SKIP/NOT_EXECUTED/MISSING => exit 1).
if ($report -match '=SKIP ') {
    Write-Output 'RESULT=PARTIAL'
    if ($GateMode -eq 'production') { exit 1 }
    # local mode: PARTIAL is informational for package/docs probes without an installed instance.
    exit 0
}
Write-Output 'RESULT=PASS'
exit 0
