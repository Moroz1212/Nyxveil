#Requires -Version 5.1
# Legacy entrypoint — redirects to package-final.ps1
Write-Host "package-client.ps1 is deprecated; running package-final.ps1"
& (Join-Path $PSScriptRoot "package-final.ps1")
