#Requires -Version 5.1
<#
.SYNOPSIS
  Compare create_database.sql CREATE TABLE names against EF InitialCreate / model expectations.
.DESCRIPTION
  Mirrors SchemaAlignmentTests.cs. Exit 0 on alignment; non-zero with missing tables listed.
#>
[CmdletBinding()]
param(
    [string]$LicensingRoot = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if ([string]::IsNullOrWhiteSpace($LicensingRoot)) {
    $LicensingRoot = Split-Path -Parent $PSScriptRoot
}

$sqlPath = Join-Path $LicensingRoot 'database\create_database.sql'
$migrationsDir = Join-Path $LicensingRoot 'src\Nyxveil.ControlPlane.Infrastructure\Persistence\Migrations'

if (-not (Test-Path -LiteralPath $sqlPath)) {
    throw "create_database.sql not found: $sqlPath"
}
if (-not (Test-Path -LiteralPath $migrationsDir)) {
    throw "Migrations folder not found: $migrationsDir"
}

$sql = Get-Content -LiteralPath $sqlPath -Raw -Encoding UTF8
$sqlTables = [System.Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
foreach ($m in [regex]::Matches($sql, 'CREATE\s+TABLE\s+(?:dbo\.)?\[?([A-Za-z_][A-Za-z0-9_]*)\]?', 'IgnoreCase')) {
    [void]$sqlTables.Add($m.Groups[1].Value)
}

$major = @(
    'Users', 'Plans', 'Licenses', 'Devices', 'Locations', 'Nodes',
    'NodeEndpoints', 'NodeTransports', 'NodeHealth', 'NodeMetrics',
    'NodeCredentials', 'NodeConfigs', 'BootstrapTokens', 'TicketAudits',
    'Revocations', 'CatalogVersions', 'SigningKeysMetadata', 'AuditLog',
    'SystemSettings', 'PaymentEvents', 'LicenseAllowedLocations',
    'AspNetRoles', 'AspNetUsers', 'AspNetUserRoles', '__EFMigrationsHistory'
)

$failures = New-Object System.Collections.Generic.List[string]
foreach ($t in $major) {
    if (-not $sqlTables.Contains($t)) {
        [void]$failures.Add("create_database.sql missing CREATE TABLE $t")
    }
}

$migrationFiles = @(Get-ChildItem -LiteralPath $migrationsDir -Filter '*_InitialCreate.cs' |
    Where-Object { $_.Name -notlike '*.Designer.cs' } |
    Sort-Object Name)
if ($migrationFiles.Count -lt 1) {
    throw "No *_InitialCreate.cs under $migrationsDir"
}
$migrationFile = $migrationFiles[-1]
$migrationId = [System.IO.Path]::GetFileNameWithoutExtension($migrationFile.Name)
$migrationText = Get-Content -LiteralPath $migrationFile.FullName -Raw -Encoding UTF8

foreach ($m in [regex]::Matches($migrationText, 'CreateTable\(\s*name:\s*"([^"]+)"')) {
    $name = $m.Groups[1].Value
    if (-not $sqlTables.Contains($name)) {
        [void]$failures.Add("InitialCreate table '$name' missing from create_database.sql")
    }
}

if ($sql -notmatch [regex]::Escape($migrationId)) {
    [void]$failures.Add("create_database.sql does not contain MigrationId '$migrationId'")
}

if ($failures.Count -gt 0) {
    Write-Host 'SCHEMA MISMATCH:' -ForegroundColor Red
    $failures | ForEach-Object { Write-Host "  - $_" -ForegroundColor Red }
    exit 1
}

Write-Host "OK: schema aligned (tables=$($sqlTables.Count); migration=$migrationId)" -ForegroundColor Green
exit 0
