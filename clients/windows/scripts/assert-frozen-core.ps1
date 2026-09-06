#Requires -Version 5.1
# assert-frozen-core.ps1 — fail package if Frozen Core provenance drifts.
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
$Expected = "7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b"
$Doc = Join-Path $Root "THIRD_PARTY_CORE.md"
$Nvp = Join-Path $Root "third_party\nvp"

if (-not (Test-Path $Doc)) { throw "missing THIRD_PARTY_CORE.md" }
if (-not (Select-String -Path $Doc -Pattern $Expected -Quiet)) {
  throw "THIRD_PARTY_CORE.md missing expected SHA $Expected"
}
if (-not (Test-Path (Join-Path $Nvp "go.mod"))) { throw "missing third_party/nvp/go.mod" }
if (-not (Test-Path (Join-Path $Nvp "THIRD_PARTY_CORE.md"))) {
  throw "missing third_party/nvp/THIRD_PARTY_CORE.md"
}
if (-not (Select-String -Path (Join-Path $Nvp "THIRD_PARTY_CORE.md") -Pattern $Expected -Quiet)) {
  throw "vendored THIRD_PARTY_CORE.md SHA mismatch"
}

$sessionGo = Join-Path $Nvp "core\session\session.go"
$ctrlGo = Join-Path $Nvp "core\control\messages.go"
if (-not (Select-String -Path $sessionGo -Pattern 'func \(s \*Session\) sendControl' -Quiet)) {
  throw "Frozen Core missing Session.sendControl"
}
if (-not (Select-String -Path $ctrlGo -Pattern 'TypeConfig' -Quiet)) {
  throw "Frozen Core missing TypeConfig"
}

# Authoritative zip hash is mandatory when the zip is present anywhere nearby.
$zips = @(
  (Join-Path $Root "..\..\Nyxveil-Protocol-Core-v1.0.0-FROZEN.zip"),
  (Join-Path $Root "Nyxveil-Protocol-Core-v1.0.0-FROZEN.zip"),
  (Join-Path $Root "..\Nyxveil-Protocol-Core-v1.0.0-FROZEN.zip"),
  (Join-Path $Root "..\..\dist\Nyxveil-Protocol-Core-v1.0.0-FROZEN.zip")
)
$foundZip = $false
foreach ($z in $zips) {
  if (Test-Path $z) {
    $foundZip = $true
    $got = (Get-FileHash $z -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($got -ne $Expected) { throw "Frozen Core zip SHA mismatch: $got want $Expected ($z)" }
    Write-Host "assert-frozen-core: zip OK $z"
    break
  }
}
if (-not $foundZip) {
  Write-Host "assert-frozen-core: zip not in tree; vendored third_party/nvp + doc SHA enforced"
}
Write-Host "assert-frozen-core: OK $Expected"
exit 0
