#Requires -Version 5.1
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

& .\gradlew.bat ":app:testDebugUnitTest" ":bridge:testDebugUnitTest"
if ($LASTEXITCODE -ne 0) {
    throw "Tests failed"
}
Write-Host "Unit tests OK."
