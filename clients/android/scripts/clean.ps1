#Requires -Version 5.1
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

if (Test-Path ".\gradlew.bat") {
    & .\gradlew.bat clean
}
Remove-Item -Recurse -Force -ErrorAction SilentlyContinue @(
    ".\app\build",
    ".\bridge\build",
    ".\bridge\native",
    ".\build",
    ".\.gradle",
    ".\dist"
)
Write-Host "Clean complete."
