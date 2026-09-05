#Requires -Version 5.1
#Requires -RunAsAdministrator
<#
.SYNOPSIS
  Correct only the duplicated firewall name written by an older Repair installer.
  Does not change firewall rules, service state, certificates, database or secrets.
#>
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force
Assert-Administrator
$op = Read-OperationalConfig
$port = [int]$op.Port
Assert-ValidPort -Port $port
$expected = Get-NyxveilFirewallRuleName -Port $port
$values = @($op.FirewallRuleName)
$duplicateArray = $values.Count -eq 2 -and $values[0] -ceq $expected -and $values[1] -ceq $expected
$duplicateString = $values.Count -eq 1 -and [string]$values[0] -ceq "$expected $expected"
if ($values.Count -eq 1 -and [string]$values[0] -ceq $expected) {
    Write-Host 'Firewall metadata is already correct.'
    return
}
if (-not ($duplicateArray -or $duplicateString)) {
    throw 'Firewall metadata does not match the known duplication. No changes made.'
}
$rules = @(Get-NetFirewallRule -Name $expected -ErrorAction Stop)
if ($rules.Count -ne 1 -or [string]$rules[0].DisplayName -cne $expected) {
    throw 'Expected one matching Nyxveil firewall rule. No changes made.'
}
$rule = $rules[0]
$filters = @(Get-NetFirewallPortFilter -AssociatedNetFirewallRule $rule -ErrorAction Stop)
if ($filters.Count -ne 1 -or [string]$filters[0].LocalPort -ne [string]$port -or
    [string]$filters[0].Protocol -notin @('TCP', '6') -or [string]$rule.Enabled -ne 'True' -or
    [string]$rule.Direction -ne 'Inbound' -or [string]$rule.Action -ne 'Allow') {
    throw 'The actual firewall rule is not enabled inbound TCP Allow on the configured port. No changes made.'
}
$primary = Get-OperationalConfigPath
$copy = Join-Path ([string]$op.InstallDir) 'config\operational.json'
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss-fff'
foreach ($path in @($primary, $copy) | Select-Object -Unique) {
    if (Test-Path -LiteralPath $path) {
        Copy-Item -LiteralPath $path -Destination ($path + '.before-selftest-fix-' + $stamp) -ErrorAction Stop
    }
}
$op.FirewallRuleName = $expected
Write-OperationalConfig -Config $op -InstallDir ([string]$op.InstallDir)
Write-Host "Corrected firewall metadata: $expected. Existing rule and service were not changed."
