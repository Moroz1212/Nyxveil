#Requires -Version 5.1
<#
.SYNOPSIS
  Frozen Core / Control Plane production interop harness.

.DESCRIPTION
  Uses production TicketService + CatalogService against exact Frozen Core SHA.
  Prints:
    CORE_SHA=PASS
    TICKET_PRODUCTION_CS_TO_GO=PASS
    TICKET_LOCATION_SCOPE=PASS
    CATALOG_PRODUCTION_CS_TO_GO=PASS
    CATALOG_NODE_MAPPING=PASS
    NODETOKEN_GO_TO_CS=PASS
    TESTONLY_ROLE_SEMANTICS=PASS
#>
[CmdletBinding()]
param(
    [string]$RepoRoot = '',
    [string]$FrozenZip = '',
    [string]$ExpectedSha256 = '7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Fail([string]$msg) {
    Write-Host "FAIL: $msg" -ForegroundColor Red
    exit 1
}

function Ok([string]$msg) { Write-Host "OK: $msg" -ForegroundColor Green }

if (-not $RepoRoot) {
    $RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
}
$licensingRoot = Join-Path $RepoRoot 'licensing'
if (-not (Test-Path (Join-Path $licensingRoot 'Nyxveil.ControlPlane.slnx'))) {
    if (Test-Path (Join-Path $PSScriptRoot '..\Nyxveil.ControlPlane.slnx')) {
        $licensingRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
        $RepoRoot = (Resolve-Path (Join-Path $licensingRoot '..')).Path
    }
}

if (-not $FrozenZip) {
    $candidates = @(
        (Join-Path $RepoRoot 'Nyxveil-Protocol-Core-v1.0.0-FROZEN.zip'),
        (Join-Path $licensingRoot '..\Nyxveil-Protocol-Core-v1.0.0-FROZEN.zip')
    )
    foreach ($c in $candidates) {
        $resolved = [System.IO.Path]::GetFullPath($c)
        if (Test-Path -LiteralPath $resolved) { $FrozenZip = $resolved; break }
    }
}
if (-not $FrozenZip -or -not (Test-Path -LiteralPath $FrozenZip)) {
    Fail 'Frozen zip not found. Expected Nyxveil-Protocol-Core-v1.0.0-FROZEN.zip under repo root.'
}

Write-Host '=== Core interop harness (production services) ==='
Write-Host "FrozenZip=$FrozenZip"

$hash = (Get-FileHash -LiteralPath $FrozenZip -Algorithm SHA256).Hash.ToLowerInvariant()
$expected = $ExpectedSha256.ToLowerInvariant()
if ($hash -ne $expected) {
    Fail "CORE_SHA mismatch. actual=$hash expected=$expected"
}
Write-Host 'CORE_SHA=PASS'
Ok 'Frozen Core SHA256 matched'

$work = Join-Path ([System.IO.Path]::GetTempPath()) ('nyxveil-core-interop-' + [guid]::NewGuid().ToString('N'))
$extract = Join-Path $work 'frozen'
$artifacts = Join-Path $work 'artifacts'
New-Item -ItemType Directory -Force -Path $extract, $artifacts | Out-Null

$verifyDir = Join-Path $licensingRoot 'tests\CoreInterop\verify'
$emitProj = Join-Path $licensingRoot 'tests\CoreInterop\CsEmit\CsEmit.csproj'
$modPath = Join-Path $verifyDir 'go.mod'
$modBackup = $null

try {
    Write-Host "Extracting to $extract ..."
    tar -xf $FrozenZip -C $extract
    if ($LASTEXITCODE -ne 0) { Fail 'Frozen archive extraction failed' }
    $goMod = Join-Path $extract 'go.mod'
    if (-not (Test-Path -LiteralPath $goMod)) {
        Fail "Extracted archive missing go.mod at $goMod"
    }

    if (-not (Test-Path $verifyDir)) { Fail "Missing Go verify helper: $verifyDir" }
    if (-not (Test-Path $emitProj)) { Fail "Missing CsEmit project: $emitProj" }

    $modText = [System.IO.File]::ReadAllText($modPath)
    if ($modText.Length -gt 0 -and [int][char]$modText[0] -eq 0xFEFF) {
        $modText = $modText.Substring(1)
    }
    $modBackup = $modText
    $replaceLine = "replace github.com/nyxveil/nvp => $($extract.Replace('\', '/'))"
    if ($modText -match '(?m)^replace\s+github\.com/nyxveil/nvp\s+=>') {
        $modText = [regex]::Replace($modText, '(?m)^replace\s+github\.com/nyxveil/nvp\s+=>.*$', $replaceLine)
    }
    else {
        $modText = $modText.TrimEnd() + "`r`n`r`n$replaceLine`r`n"
    }
    $utf8NoBom = New-Object System.Text.UTF8Encoding $false
    [System.IO.File]::WriteAllText($modPath, $modText, $utf8NoBom)
    $env:FROZEN_CORE_ROOT = $extract

    Push-Location $verifyDir
    try {
        Write-Host 'go mod tidy (verify helper)...'
        & go mod tidy
        if ($LASTEXITCODE -ne 0) { Fail "go mod tidy failed exit=$LASTEXITCODE" }
    }
    finally { Pop-Location }

    Write-Host 'AUDIENCE=nvp-node'

    Write-Host 'Emitting production CS ticket...'
    & dotnet run --project $emitProj -c Release --no-launch-profile -- issue-ticket --out-dir $artifacts
    if ($LASTEXITCODE -ne 0) { Fail 'CsEmit issue-ticket failed' }
    $meta = Get-Content (Join-Path $artifacts 'ticket.meta.json') -Raw | ConvertFrom-Json
    Push-Location $verifyDir
    try {
        & go run . verify-ticket `
            --token-file (Join-Path $artifacts 'ticket.jwt') `
            --pubkey-hex $meta.pubkey_hex `
            --kid $meta.kid `
            --issuer $meta.issuer `
            --audience 'nvp-node' `
            --expected-location-id $meta.expected_location_id `
            --device-id $meta.device_id
        if ($LASTEXITCODE -ne 0) { Fail 'TICKET_PRODUCTION_CS_TO_GO failed' }
    }
    finally { Pop-Location }
    Write-Host 'TICKET_PRODUCTION_CS_TO_GO=PASS'
    Write-Host 'TICKET_LOCATION_SCOPE=PASS'
    Ok 'Production TicketService -> Frozen VerifyAt'

    Write-Host 'Emitting production CS catalog...'
    & dotnet run --project $emitProj -c Release --no-launch-profile -- sign-catalog --out-dir $artifacts
    $catalogEmitExit = $LASTEXITCODE
    if ($catalogEmitExit -ne 0) { Fail "CsEmit sign-catalog failed exit=$catalogEmitExit" }
    if (-not (Test-Path -LiteralPath (Join-Path $artifacts 'catalog.json'))) { Fail 'catalog.json missing after CsEmit' }
    if (-not (Test-Path -LiteralPath (Join-Path $artifacts 'testonly.meta.json'))) { Fail 'testonly.meta.json missing after CsEmit' }
    $cmeta = Get-Content (Join-Path $artifacts 'catalog.meta.json') -Raw | ConvertFrom-Json
    Push-Location $verifyDir
    try {
        & go run . verify-catalog `
            --catalog-file (Join-Path $artifacts 'catalog.json') `
            --pubkey-hex $cmeta.pubkey_hex `
            --kid $cmeta.kid `
            --expected-node-id $cmeta.expected_node_id `
            --expected-location-id $cmeta.expected_location_id
        $catalogGoExit = $LASTEXITCODE
        if ($catalogGoExit -ne 0) { Fail "CATALOG_PRODUCTION_CS_TO_GO failed exit=$catalogGoExit" }
    }
    finally { Pop-Location }
    Write-Host 'CATALOG_PRODUCTION_CS_TO_GO=PASS'
    Write-Host 'CATALOG_NODE_MAPPING=PASS'
    Ok 'Production CatalogService -> Frozen catalog.Verify'

    Push-Location $verifyDir
    try {
        foreach ($state in @('maintenance', 'draining', 'disabled')) {
            & go run . verify-catalog --catalog-file (Join-Path $artifacts ($state + '.json')) `
                --pubkey-hex $cmeta.pubkey_hex --kid $cmeta.kid --expected-location-id $cmeta.expected_location_id --expect-no-candidates
            if ($LASTEXITCODE -ne 0) { Fail "Frozen selector accepted $state node" }
            Write-Host "FROZEN_SELECTOR_$($state.ToUpperInvariant())=PASS"
        }
    }
    finally { Pop-Location }

    $tometa = Get-Content (Join-Path $artifacts 'testonly.meta.json') -Raw | ConvertFrom-Json
    if ($tometa.user_sees_testonly -eq $true) { Fail 'TESTONLY_ROLE_SEMANTICS: user saw test_only nodes' }
    if ($tometa.master_sees_testonly -ne $true) { Fail 'TESTONLY_ROLE_SEMANTICS: master did not see test_only nodes' }
    Write-Host 'TESTONLY_ROLE_SEMANTICS=PASS'
    Ok 'TestOnly master-only'

    $nodeId = 'node-interop-1'
    $seed = [byte[]]::new(32)
    [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($seed)
    $seedHex = ([System.BitConverter]::ToString($seed) -replace '-', '').ToLowerInvariant()
    $tokenPath = Join-Path $artifacts 'node.token'
    Push-Location $verifyDir
    try {
        $prevErr = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        $goOut = & go run . sign-node-token --node-id $nodeId --privkey-hex $seedHex --out $tokenPath 2>&1
        $ErrorActionPreference = $prevErr
        if ($LASTEXITCODE -ne 0) {
            Fail ('sign-node-token failed: ' + ($goOut | Out-String))
        }
        $metaLine = ($goOut | Where-Object { $_ -match '"pub_hex"' } | Select-Object -Last 1)
        if (-not $metaLine) { Fail 'sign-node-token did not emit pub_hex metadata' }
        $nmeta = $metaLine | ConvertFrom-Json
    }
    finally { Pop-Location }

    & dotnet run --project $emitProj -c Release --no-launch-profile -- verify-node-token `
        --token-file $tokenPath `
        --node-id $nodeId `
        --pubkey-hex $nmeta.pub_hex
    if ($LASTEXITCODE -ne 0) { Fail 'NODETOKEN_GO_TO_CS failed' }
    Write-Host 'NODETOKEN_GO_TO_CS=PASS'
    Ok 'NodeToken Go-to-CS'

    $hashAfter = (Get-FileHash -LiteralPath $FrozenZip -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($hashAfter -ne $expected) {
        Fail "CORE_SHA changed after interop. actual=$hashAfter"
    }

    Write-Host ''
    Write-Host 'All production interop gates passed.'
    exit 0
}
finally {
    if ($null -ne $modBackup -and (Test-Path -LiteralPath $modPath)) {
        $utf8NoBom = New-Object System.Text.UTF8Encoding $false
        [System.IO.File]::WriteAllText($modPath, $modBackup, $utf8NoBom)
    }
    $workFull = [IO.Path]::GetFullPath($work)
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    if ($workFull.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -and
        [IO.Path]::GetFileName($workFull) -match '^nyxveil-core-interop-[a-f0-9]{32}$') {
        try { Remove-Item -LiteralPath $workFull -Recurse -Force -ErrorAction SilentlyContinue } catch { }
    }
}
