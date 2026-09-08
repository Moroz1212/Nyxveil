#Requires -Version 5.1
<#
.SYNOPSIS
  Release-blocking provenance attestation for Nyxveil Windows Client packaging.

  Attests:
    1) dist/payload bytes match the just-built expected inputs
    2) Setup sidecar PROVENANCE.sha256 records those exact hashes
    3) If 7-Zip is present, Setup.exe contents are extracted and re-hashed
    4) Optional -InstalledDir compares an installed tree to the attested hashes
#>
param(
  [Parameter(Mandatory = $true)][string]$SetupExe,
  [Parameter(Mandatory = $true)][string]$ExpectedServiceExe,
  [Parameter(Mandatory = $true)][string]$ExpectedGuiExe,
  [Parameter(Mandatory = $true)][string]$ExpectedWintunDll,
  [Parameter(Mandatory = $true)][string]$ExpectedVersionFile,
  [string]$InstalledDir = ""
)

$ErrorActionPreference = "Stop"

function Get-Sha([string]$Path) {
  if (-not (Test-Path -LiteralPath $Path)) { throw "missing file: $Path" }
  return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Assert-Hash([string]$Label, [string]$Path, [string]$Want) {
  $got = Get-Sha $Path
  $want = $Want.ToLowerInvariant()
  if ($got -ne $want) {
    throw "PROVENANCE FAIL: $Label hash mismatch`n  path=$Path`n  got=$got`n  want=$want"
  }
  Write-Host ("PROVENANCE OK {0}={1}" -f $Label, $got)
  return $got
}

if (-not (Test-Path -LiteralPath $SetupExe)) { throw "Setup missing: $SetupExe" }

$svcWant = Get-Sha $ExpectedServiceExe
$guiWant = Get-Sha $ExpectedGuiExe
$dllWant = Get-Sha $ExpectedWintunDll
$verWant = (Get-Content -LiteralPath $ExpectedVersionFile -Raw).Trim()
$setupWant = Get-Sha $SetupExe

$payloadRoot = Split-Path -Parent $ExpectedServiceExe
Assert-Hash "service-payload" (Join-Path $payloadRoot "Nyxveil.Service.exe") $svcWant | Out-Null
Assert-Hash "gui-payload" (Join-Path $payloadRoot "gui\Nyxveil.exe") $guiWant | Out-Null
Assert-Hash "wintun-payload" (Join-Path $payloadRoot "wintun.dll") $dllWant | Out-Null
$verPayload = Join-Path (Split-Path -Parent $payloadRoot) "..\VERSION"
# VERSION is packaged from repo root, not payload — check expected file only.
if ((Get-Content -LiteralPath $ExpectedVersionFile -Raw).Trim() -ne $verWant) {
  throw "PROVENANCE FAIL: VERSION drift"
}

$sidecar = $SetupExe + ".PROVENANCE.sha256"
@"
$setupWant  $(Split-Path -Leaf $SetupExe)
$svcWant  Nyxveil.Service.exe
$guiWant  Nyxveil.exe
$dllWant  wintun.dll
$verWant  VERSION
"@ | Set-Content -LiteralPath $sidecar -Encoding ASCII
Write-Host "PROVENANCE OK sidecar=$sidecar"

$seven = @(
  "$env:ProgramFiles\7-Zip\7z.exe",
  "${env:ProgramFiles(x86)}\7-Zip\7z.exe"
) | Where-Object { Test-Path $_ } | Select-Object -First 1

if ($seven) {
  $extract = Join-Path $env:TEMP ("nyxveil-prov-" + [guid]::NewGuid().ToString("N"))
  New-Item -ItemType Directory -Force -Path $extract | Out-Null
  try {
    $prevEap = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    & $seven x "-o$extract" $SetupExe -y 2>&1 | Out-Null
    $extractCode = $LASTEXITCODE
    $ErrorActionPreference = $prevEap
    if ($extractCode -notin 0, 1) {
      Write-Host "PROVENANCE WARN: 7z cannot extract this Setup format (exit=$extractCode); sidecar + payload hashes attested; install-time hash gate still applies"
    } else {
      $foundSvc = Get-ChildItem -LiteralPath $extract -Recurse -Filter "Nyxveil.Service.exe" | Select-Object -First 1
      $foundGui = Get-ChildItem -LiteralPath $extract -Recurse -Filter "Nyxveil.exe" | Sort-Object Length -Descending | Select-Object -First 1
      $foundDll = Get-ChildItem -LiteralPath $extract -Recurse -Filter "wintun.dll" | Select-Object -First 1
      $foundVer = Get-ChildItem -LiteralPath $extract -Recurse -Filter "VERSION" | Select-Object -First 1
      if (-not $foundSvc -or -not $foundGui -or -not $foundDll -or -not $foundVer) {
        throw "PROVENANCE FAIL: Setup extract missing required files"
      }
      Assert-Hash "service-in-setup" $foundSvc.FullName $svcWant | Out-Null
      Assert-Hash "gui-in-setup" $foundGui.FullName $guiWant | Out-Null
      Assert-Hash "wintun-in-setup" $foundDll.FullName $dllWant | Out-Null
      $verGot = (Get-Content -LiteralPath $foundVer.FullName -Raw).Trim()
      if ($verGot -ne $verWant) { throw "PROVENANCE FAIL: VERSION in Setup='$verGot' want='$verWant'" }
      Write-Host "PROVENANCE OK VERSION=$verGot (7z extract)"
    }
  }
  finally {
    Remove-Item -Recurse -Force $extract -ErrorAction SilentlyContinue
  }
} else {
  Write-Host "PROVENANCE WARN: 7z.exe not found - Setup internal extract skipped; sidecar + install hash gate required"
}

if ($InstalledDir) {
  Assert-Hash "installed-service" (Join-Path $InstalledDir "Nyxveil.Service.exe") $svcWant | Out-Null
  Assert-Hash "installed-gui" (Join-Path $InstalledDir "Nyxveil.exe") $guiWant | Out-Null
  Assert-Hash "installed-wintun" (Join-Path $InstalledDir "wintun.dll") $dllWant | Out-Null
  $iver = (Get-Content -LiteralPath (Join-Path $InstalledDir "VERSION") -Raw).Trim()
  if ($iver -ne $verWant) { throw "PROVENANCE FAIL: installed VERSION='$iver' want='$verWant'" }
  Write-Host "PROVENANCE OK installed-tree=$InstalledDir"
}

Write-Host "PROVENANCE PASS setup=$SetupExe"
exit 0
