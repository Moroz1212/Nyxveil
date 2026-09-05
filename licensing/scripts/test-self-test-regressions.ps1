#Requires -Version 5.1
[CmdletBinding()]
param([Parameter(Mandatory = $true)][string]$ScratchDir)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$module = Import-Module (Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force -PassThru
$scratchRoot = (Resolve-Path -LiteralPath $ScratchDir).Path.TrimEnd('\')
$testRoot = Join-Path $scratchRoot ('nyxveil-selftest-test-' + [Guid]::NewGuid().ToString('N'))
try {
    $null = New-Item -ItemType Directory -Path $testRoot
    Set-Content -LiteralPath (Join-Path $testRoot 'Nyxveil.ControlPlane.Web.exe') -Value 'fixture, never executed'
    $source = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'self-test.ps1') -Raw -Encoding UTF8
    $healthStart = $source.IndexOf('# Hostname-aware HTTP')
    $healthEnd = $source.IndexOf('foreach ($pair', $healthStart)
    $healthBlock = [scriptblock]::Create($source.Substring($healthStart, $healthEnd - $healthStart))
    $settingsStart = $source.IndexOf('# Setup disabled in production appsettings')
    $settingsEnd = $source.IndexOf('# DB connectivity', $settingsStart)
    $settingsBlock = [scriptblock]::Create($source.Substring($settingsStart, $settingsEnd - $settingsStart))
    $tokens = $null; $parseErrors = $null
    $ast = [Management.Automation.Language.Parser]::ParseInput($source, [ref]$tokens, [ref]$parseErrors)
    $optional = $ast.Find({ param($n) $n -is [Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -eq 'Get-OptionalProperty' }, $true)
    if ($null -eq $optional) { throw 'Missing optional-property helper.' }
    $repairSource = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'repair-self-test-metadata.ps1') -Raw -Encoding UTF8
    $repairBlock = [scriptblock]::Create($repairSource.Substring($repairSource.IndexOf('$op = Read-OperationalConfig')))
    & $module {
        param($InstallDir, $healthBlock, $settingsBlock, $optionalSource, $repairBlock)
        $savedCli = (Get-Item Function:Invoke-NyxveilSelfTestCli).ScriptBlock
        $savedHttp = (Get-Item Function:Test-HttpsHealthHttp).ScriptBlock
        try {
            function Get-NetFirewallRule { param($DisplayName) if ($script:FixtureFirewallExists) { [pscustomobject]@{DisplayName=$DisplayName} } }
            function Get-NetFirewallPortFilter { param($AssociatedNetFirewallRule) [pscustomobject]@{LocalPort=8443; Protocol='TCP'} }
            function Set-NetFirewallRule { param($DisplayName, $Enabled, $ErrorAction) }
            function New-NetFirewallRule {
                param($DisplayName,$Name,$Direction,$Action,$Protocol,$LocalPort,$Profile,$Description)
                $script:FixtureFirewallExists = $true
                [pscustomobject]@{DisplayName=$DisplayName}
            }
            $script:FixtureFirewallExists = $false
            foreach ($run in 1..3) {
                $result = New-NyxveilFirewallRule -Port 8443
                if ($result -isnot [string] -or $result -cne 'Nyxveil Control Plane HTTPS 8443') {
                    throw 'Firewall helper returned duplicate or non-string metadata.'
                }
            }
            Write-Host '[PASS] Firewall metadata stays one string across Fresh and repeated Repair.'

            function Invoke-NyxveilSelfTestCli { param($InstallDir,$PublicHostname) return $script:FixtureCliOk }
            function Test-HttpsHealthHttp { param($Port,$PublicHostname,$Path) return $false }
            function Fail([string]$Message) { $script:FixtureFailures++ }
            function Warn([string]$Message) { $script:FixtureWarnings++ }
            function Ok([string]$Message) { }
            $Port = 8443; $publicHostname = 'test.invalid'; $certMode = 'Store'
            $script:FixtureCliOk = $true
            foreach ($certValidationMode in @('SelfSignedPinned', 'SystemTrust')) {
                $script:FixtureFailures = 0
                & $healthBlock
                $expected = if ($certValidationMode -eq 'SelfSignedPinned') { 0 } else { 1 }
                if ($script:FixtureFailures -ne $expected) { throw "Incorrect health result for $certValidationMode" }
            }
            $script:FixtureCliOk = $false
            $certValidationMode = 'SelfSignedPinned'
            $script:FixtureFailures = 0
            & $healthBlock
            if ($script:FixtureFailures -ne 1) { throw 'Failed pinned TLS/CLI probe must fail the health gate.' }
            Write-Host '[PASS] Actual self-test call honors pinned policy; SystemTrust and failed CLI stay fail-closed.'

            . ([scriptblock]::Create($optionalSource))
            $settingsPath = Join-Path $InstallDir 'appsettings.Production.json'
            '{"Setup":{"AllowWebBootstrap":false},"Certificate":{"Mode":"Store","Thumbprint":"fixture","ValidationMode":"SelfSignedPinned"}}' |
                Set-Content -LiteralPath $settingsPath -Encoding UTF8
            $script:FixtureFailures = 0; $script:FixtureWarnings = 0
            & $settingsBlock
            if ($script:FixtureFailures -ne 0 -or $script:FixtureWarnings -ne 0) { throw 'Missing optional Kestrel section must not warn or fail.' }
            '{"Setup":{}}' | Set-Content -LiteralPath $settingsPath -Encoding UTF8
            $script:FixtureFailures = 0
            & $settingsBlock
            if ($script:FixtureFailures -eq 0) { throw 'Missing required Certificate section must fail.' }
            'invalid json' | Set-Content -LiteralPath $settingsPath -Encoding UTF8
            $script:FixtureFailures = 0
            & $settingsBlock
            if ($script:FixtureFailures -eq 0) { throw 'Invalid JSON must fail.' }
            Write-Host '[PASS] Optional Kestrel is accepted; missing certificate or invalid JSON is rejected.'

            # Exercise the actual repair body against temporary metadata and mocked
            # read-only firewall queries. No Windows firewall rules are modified.
            $script:FixtureMetadataPath = Join-Path $InstallDir 'operational.json'
            function Read-OperationalConfig { return $script:FixtureOp }
            function Get-OperationalConfigPath { return $script:FixtureMetadataPath }
            function Write-OperationalConfig {
                param($Config,$InstallDir)
                $script:FixtureWrites++
                $Config | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $script:FixtureMetadataPath
            }
            function Get-NetFirewallRule {
                param($Name,$ErrorAction)
                if ($script:FixtureRulePresent) {
                    [pscustomobject]@{DisplayName=$Name; Enabled='True'; Direction='Inbound'; Action='Allow'}
                }
            }
            $expectedName = 'Nyxveil Control Plane HTTPS 8443'
            foreach ($case in @('array', 'string', 'correct', 'unknown', 'missing-rule')) {
                $script:FixtureRulePresent = $case -ne 'missing-rule'
                $stored = switch ($case) {
                    'array' { @($expectedName, $expectedName) }
                    'string' { "$expectedName $expectedName" }
                    'correct' { $expectedName }
                    'unknown' { 'Unrelated rule' }
                    'missing-rule' { @($expectedName, $expectedName) }
                }
                $script:FixtureOp = [pscustomobject]@{Port=8443; FirewallRuleName=$stored; InstallDir=$InstallDir; PreserveMe='unchanged'}
                $script:FixtureOp | ConvertTo-Json | Set-Content -LiteralPath $script:FixtureMetadataPath
                $script:FixtureWrites = 0
                $failed = $false
                try { & $repairBlock } catch { $failed = $true }
                if ($failed -ne ($case -in @('unknown', 'missing-rule'))) { throw "Wrong metadata repair result: $case" }
                $expectedWrites = if ($case -in @('array','string')) { 1 } else { 0 }
                if ($script:FixtureWrites -ne $expectedWrites) { throw "Unexpected metadata write: $case" }
                if ($expectedWrites -eq 1) {
                    $repaired = Get-Content -LiteralPath $script:FixtureMetadataPath -Raw | ConvertFrom-Json
                    if ($repaired.FirewallRuleName -isnot [string] -or $repaired.FirewallRuleName -cne $expectedName -or
                        $repaired.PreserveMe -cne 'unchanged') { throw 'Repair changed unrelated metadata or failed to normalize.' }
                }
            }
            Write-Host '[PASS] Metadata repair handles known duplicates only, preserves other fields, and requires the actual rule.'
        }
        finally {
            Set-Item Function:Invoke-NyxveilSelfTestCli -Value $savedCli
            Set-Item Function:Test-HttpsHealthHttp -Value $savedHttp
            foreach ($name in @('FixtureFirewallExists','FixtureCliOk','FixtureFailures','FixtureWarnings',
                    'FixtureMetadataPath','FixtureOp','FixtureWrites','FixtureRulePresent')) {
                Remove-Variable -Name $name -Scope Script -ErrorAction SilentlyContinue
            }
        }
    } $testRoot $healthBlock $settingsBlock $optional.Extent.Text $repairBlock
}
finally {
    if (Test-Path -LiteralPath $testRoot) {
        $resolved = (Resolve-Path -LiteralPath $testRoot).Path
        if ((Split-Path -Parent $resolved) -ne $scratchRoot -or (Split-Path -Leaf $resolved) -notlike 'nyxveil-selftest-test-*') {
            throw "Refusing cleanup outside scratch: $resolved"
        }
        Remove-Item -LiteralPath $resolved -Recurse -Force
    }
}
