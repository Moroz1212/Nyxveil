#Requires -Version 5.1
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$EvidenceDirectory,
    [string]$OutputPath = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$componentKeys = @(
    'cp_button_update',
    'windows_scm',
    'node_button_update',
    'durable_restart',
    'acme_pebble',
    'cert_button',
    'tls_served',
    'quic_handshake',
    'rollback_recovery'
)

if (-not $OutputPath) {
    $OutputPath = Join-Path (Get-Location) 'production-gates-evidence.json'
}
$outputFullPath = [IO.Path]::GetFullPath($OutputPath)
$merged = @{}
$conflicts = [Collections.Generic.List[string]]::new()

function Merge-Result([string]$Key, [object]$Value, [string]$Source) {
    if ($componentKeys -notcontains $Key) { return }
    $text = [string]$Value
    if ($merged.ContainsKey($Key) -and $merged[$Key] -cne $text) {
        $conflicts.Add("$Key conflicts in $Source")
        $merged[$Key] = 'FAIL'
        return
    }
    $merged[$Key] = $text
}

try {
    if (-not (Test-Path -LiteralPath $EvidenceDirectory -PathType Container)) {
        throw "Evidence directory not found: $EvidenceDirectory"
    }

    $files = @(Get-ChildItem -LiteralPath $EvidenceDirectory -Filter '*.json' -File -Recurse |
        Where-Object { [IO.Path]::GetFullPath($_.FullName) -ne $outputFullPath })
    if ($files.Count -eq 0) {
        throw "No evidence JSON files found under: $EvidenceDirectory"
    }

    foreach ($file in $files) {
        $evidence = Get-Content -LiteralPath $file.FullName -Raw | ConvertFrom-Json
        if ($null -eq $evidence) { continue }

        if ($evidence.PSObject.Properties.Name -contains 'gate' -and
            $evidence.PSObject.Properties.Name -contains 'result') {
            Merge-Result -Key ([string]$evidence.gate) -Value $evidence.result -Source $file.FullName
        }
        foreach ($key in $componentKeys) {
            if ($evidence.PSObject.Properties.Name -contains $key) {
                Merge-Result -Key $key -Value $evidence.$key -Source $file.FullName
            }
        }
    }

    $aggregate = [ordered]@{}
    foreach ($key in $componentKeys) {
        $aggregate[$key] = if ($merged.ContainsKey($key)) { $merged[$key] } else { 'MISSING' }
    }
    $allComponentsPassed = @($componentKeys | Where-Object { $aggregate[$_] -cne 'PASS' }).Count -eq 0
    $aggregate['full_operator_e2e'] = if ($allComponentsPassed) { 'PASS' } else { 'FAIL' }
    $aggregate['generated_at'] = [datetime]::UtcNow.ToString('o')
    $aggregate['source_file_count'] = $files.Count
    if ($conflicts.Count -gt 0) {
        $aggregate['conflicts'] = @($conflicts)
    }

    $parent = Split-Path -Parent $outputFullPath
    if ($parent -and -not (Test-Path -LiteralPath $parent)) {
        New-Item -ItemType Directory -Path $parent -Force | Out-Null
    }
    $aggregate | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $outputFullPath -Encoding UTF8
    Write-Output "PRODUCTION_GATES_EVIDENCE=$outputFullPath"
    Write-Output ("FULL_OPERATOR_E2E={0}" -f $aggregate['full_operator_e2e'])

    & (Join-Path $PSScriptRoot 'assert-production-gates.ps1') -EvidencePath $outputFullPath
    exit $LASTEXITCODE
}
catch {
    Write-Output 'FULL_OPERATOR_E2E=FAIL'
    Write-Output ("AUTOMATED_PRODUCTION_GATES=FAIL reasons={0}" -f $_.Exception.Message)
    exit 1
}
