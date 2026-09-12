#Requires -Version 5.1
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [ValidateNotNullOrEmpty()]
    [string[]]$EvidencePath
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$mandatoryKeys = @(
    'cp_button_update',
    'cp_artifact_purity',
    'cp_no_overlay',
    'windows_scm',
    'node_button_update',
    'server_artifact_purity',
    'server_no_local_candidate',
    'durable_restart',
    'acme_pebble',
    'cert_button',
    'tls_served',
    'quic_handshake',
    'rollback_recovery',
    'full_operator_e2e'
)
$results = @{}

function Add-EvidenceResult([string]$Key, [object]$Value, [string]$Source) {
    if ($mandatoryKeys -notcontains $Key) { return }
    $text = [string]$Value
    if ($results.ContainsKey($Key) -and $results[$Key] -cne $text) {
        throw "Conflicting evidence for '$Key': '$($results[$Key])' and '$text' (source: $Source)."
    }
    $results[$Key] = $text
}

try {
    foreach ($pathPattern in $EvidencePath) {
        $files = @(Get-ChildItem -Path $pathPattern -File -ErrorAction Stop)
        if ($files.Count -eq 0) {
            throw "No evidence files matched: $pathPattern"
        }

        foreach ($file in $files) {
            $evidence = Get-Content -LiteralPath $file.FullName -Raw | ConvertFrom-Json
            if ($null -eq $evidence) {
                throw "Evidence JSON is empty: $($file.FullName)"
            }

            if ($evidence.PSObject.Properties.Name -contains 'gate' -and
                $evidence.PSObject.Properties.Name -contains 'result') {
                Add-EvidenceResult -Key ([string]$evidence.gate) -Value $evidence.result -Source $file.FullName
            }

            foreach ($key in $mandatoryKeys) {
                if ($evidence.PSObject.Properties.Name -contains $key) {
                    Add-EvidenceResult -Key $key -Value $evidence.$key -Source $file.FullName
                }
            }
        }
    }

    $notPassed = @(
        foreach ($key in $mandatoryKeys) {
            if (-not $results.ContainsKey($key)) {
                "$key=MISSING"
            }
            elseif ($results[$key] -cne 'PASS') {
                "$key=$($results[$key])"
            }
        }
    )

    if ($notPassed.Count -gt 0) {
        Write-Output ("AUTOMATED_PRODUCTION_GATES=FAIL reasons={0}" -f ($notPassed -join ' | '))
        exit 1
    }

    Write-Output 'AUTOMATED_PRODUCTION_GATES=PASS'
    exit 0
}
catch {
    Write-Output ("AUTOMATED_PRODUCTION_GATES=FAIL reasons={0}" -f $_.Exception.Message)
    exit 1
}
