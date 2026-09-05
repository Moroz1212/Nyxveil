#Requires -Version 5.1
<#
.SYNOPSIS
  Creates a TEMP database NyxveilControlPlane_Test_*, applies create_database.sql, verifies tables + __EFMigrationsHistory, then drops ONLY the test DB.

.NOTES
  Skips gracefully if sqlcmd or SQL Server is unavailable.
#>
[CmdletBinding()]
param(
    [string]$SqlServer = 'localhost',
    [ValidateSet('Windows', 'Sql')][string]$DatabaseAuth = 'Windows',
    [string]$DatabaseUser = '',
    [securestring]$DatabasePassword
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force

if (-not (Test-SqlCmdAvailable)) {
    Write-Warning 'sqlcmd not found - skipping test-database.ps1.'
    exit 0
}

$testDb = 'NyxveilControlPlane_Test_{0}' -f (Get-Date -Format 'yyyyMMddHHmmss')
Write-Host "=== test-database: $testDb on $SqlServer ==="

try {
    Invoke-SqlCmdFailClosed -Server $SqlServer -Query 'SELECT 1' -DatabaseName 'master' `
        -DatabaseAuth $DatabaseAuth -DatabaseUser $DatabaseUser -DatabasePassword $DatabasePassword
}
catch {
    Write-Warning ("SQL Server not reachable ({0}) - skipping test-database.ps1." -f $_.Exception.Message)
    exit 0
}

$repoRoot = Get-RepoRoot
$sqlSource = Join-Path $repoRoot 'database\create_database.sql'
if (-not (Test-Path $sqlSource)) {
    throw "create_database.sql not found: $sqlSource"
}

$prepared = New-CreateDatabaseScriptCopy -SourcePath $sqlSource -DatabaseName $testDb
$dropped = $false
try {
    Write-Host 'Applying create_database.sql to temp database...'
    Invoke-SqlCmdFailClosed -Server $SqlServer -InputFile $prepared -DatabaseName $testDb `
        -DatabaseAuth $DatabaseAuth -DatabaseUser $DatabaseUser -DatabasePassword $DatabasePassword

    # Literal here-string + -f avoids PowerShell parsing SQL IF/THROW as script.
    $verify = @'
USE [{0}];
SET NOCOUNT ON;
IF OBJECT_ID(N''dbo.AspNetUsers'', N''U'') IS NULL RAISERROR(''AspNetUsers missing'', 16, 1);
IF OBJECT_ID(N''dbo.Licenses'', N''U'') IS NULL RAISERROR(''Licenses missing'', 16, 1);
IF OBJECT_ID(N''dbo.__EFMigrationsHistory'', N''U'') IS NULL RAISERROR(''EFMigrationsHistory missing'', 16, 1);
IF NOT EXISTS (SELECT 1 FROM dbo.__EFMigrationsHistory) RAISERROR(''EFMigrationsHistory empty'', 16, 1);
SELECT MigrationId FROM dbo.__EFMigrationsHistory;
'@ -f $testDb

    Invoke-SqlCmdFailClosed -Server $SqlServer -Query $verify -DatabaseName $testDb `
        -DatabaseAuth $DatabaseAuth -DatabaseUser $DatabaseUser -DatabasePassword $DatabasePassword

    Write-Host 'Verification passed (tables + __EFMigrationsHistory).'
}
finally {
    Remove-Item -LiteralPath $prepared -Force -ErrorAction SilentlyContinue
    Write-Host "Dropping ONLY test database $testDb ..."
    try {
        $safeName = $testDb.Replace("'", "''")
        $drop = @'
IF DB_ID(N''{0}'') IS NOT NULL
BEGIN
    ALTER DATABASE [{1}] SET SINGLE_USER WITH ROLLBACK IMMEDIATE;
    DROP DATABASE [{1}];
END
'@ -f $safeName, $testDb

        Invoke-SqlCmdFailClosed -Server $SqlServer -Query $drop -DatabaseName 'master' `
            -DatabaseAuth $DatabaseAuth -DatabaseUser $DatabaseUser -DatabasePassword $DatabasePassword
        $dropped = $true
        Write-Host "Dropped $testDb."
    }
    catch {
        Write-Warning ("Failed to drop test DB {0}: {1}" -f $testDb, $_.Exception.Message)
    }
}

if (-not $dropped) {
    Write-Warning "Manual cleanup may be required for database $testDb"
    exit 1
}

Write-Host 'test-database completed successfully.'
exit 0
