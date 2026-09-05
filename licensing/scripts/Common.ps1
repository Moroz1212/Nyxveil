#Requires -Version 5.1
<#
.SYNOPSIS
  Imports Nyxveil.ControlPlane.Deploy.psm1 for scripts that prefer a Common.ps1 include.
#>
$modulePath = Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1'
if (-not (Test-Path $modulePath)) {
    throw "Shared module not found: $modulePath"
}
Import-Module $modulePath -Force
