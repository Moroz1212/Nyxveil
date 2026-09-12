#Requires -Version 5.1
<#
.SYNOPSIS
  Pack a Control Plane release ZIP with forward-slash ZipArchive entries only.
#>
[CmdletBinding()]
param(
    [string]$SourceDir = '',
    [string]$OutputZip = '',
    [string]$PublishDir = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$scriptRoot = $PSScriptRoot
$licensingRoot = Split-Path -Parent $scriptRoot

if ([string]::IsNullOrWhiteSpace($SourceDir)) {
    $SourceDir = $licensingRoot
}
if ([string]::IsNullOrWhiteSpace($OutputZip)) {
    $verPath = Join-Path $licensingRoot 'VERSION'
    $ver = '1.0.0'
    if (Test-Path $verPath) { $ver = (Get-Content $verPath -Raw).Trim() }
    $OutputZip = Join-Path $licensingRoot ("Nyxveil-ControlPlane-v{0}-release.zip" -f $ver)
}

Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem

function Test-ShouldExclude([string]$FullPath, [string]$Root) {
    $rel = $FullPath.Substring($Root.Length).TrimStart('\', '/')
    $parts = $rel -split '[\\/]'
    foreach ($p in $parts) {
        if ($p -in @('bin', 'obj', 'secrets', '.git', '.vs', 'artifacts', 'publish', 'TestResults', 'logs', 'temp', 'tmp', 'work', '.cache', 'node_modules')) { return $true }
    }
    if ($rel -match '(?i)(^|[/\\])(secrets|artifacts|publish|bin|obj)([/\\]|$)') { return $true }
    if ($rel -match '(?i)\.(pfx|dpapi|user|trx|tmp)$') { return $true }
    if ($rel -match '(?i)\.tmp\.sql$') { return $true }
    if ($rel -match '(?i)(^|[/\\])ef-baseline\.tmp\.sql$') { return $true }
    # Nested release zips / sha sidecars inside source tree
    if ($rel -match '(?i)Nyxveil-ControlPlane-.*\.(zip|sha256)$') { return $true }
    return $false
}

$rootFull = (Resolve-Path -LiteralPath $SourceDir).Path.TrimEnd('\')
if (Test-Path $OutputZip) { Remove-Item -LiteralPath $OutputZip -Force }

$stagingPublish = $null
if ($PublishDir -and (Test-Path $PublishDir)) {
    $stagingPublish = (Resolve-Path -LiteralPath $PublishDir).Path
}

$verPath = Join-Path $licensingRoot 'VERSION'
$ver = '0.0.0'
if (Test-Path $verPath) { $ver = (Get-Content $verPath -Raw).Trim() }

# Machine-readable release identity (no secrets).
$manifestPath = Join-Path $licensingRoot 'release-manifest.json'
$manifestObj = [ordered]@{
    product                   = 'Nyxveil.ControlPlane'
    version                   = $ver
    tag                       = ("control-plane-v{0}" -f $ver)
    schema                    = 5
    package                   = ("Nyxveil-ControlPlane-v{0}-release.zip" -f $ver)
    updater_version           = $ver
    minimum_supported_version = '1.3.8'
    note                      = 'Authoritative package digest is the .sha256 sidecar published with the release.'
}
($manifestObj | ConvertTo-Json -Depth 5) + "`n" | Set-Content -LiteralPath $manifestPath -Encoding utf8

Write-Host "Creating $OutputZip from $rootFull ..."
$fileStream = [System.IO.File]::Open($OutputZip, [System.IO.FileMode]::CreateNew)
try {
    $archive = New-Object System.IO.Compression.ZipArchive($fileStream, [System.IO.Compression.ZipArchiveMode]::Create)
    try {
        $files = Get-ChildItem -LiteralPath $rootFull -Recurse -File -Force -ErrorAction Stop
        foreach ($f in $files) {
            if (Test-ShouldExclude -FullPath $f.FullName -Root $rootFull) { continue }
            # Skip nested output zip if packing from same tree
            if ($f.FullName -eq $OutputZip) { continue }

            $rel = $f.FullName.Substring($rootFull.Length).TrimStart('\', '/')
            $entryName = ($rel -replace '\\', '/')
            [void][System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile(
                $archive, $f.FullName, $entryName, [System.IO.Compression.CompressionLevel]::Optimal)
        }

        if ($stagingPublish) {
            $pubFiles = Get-ChildItem -LiteralPath $stagingPublish -Recurse -File -Force
            foreach ($f in $pubFiles) {
                $rel = $f.FullName.Substring($stagingPublish.Length).TrimStart('\', '/')
                $entryName = ('publish/' + ($rel -replace '\\', '/'))
                [void][System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile(
                    $archive, $f.FullName, $entryName, [System.IO.Compression.CompressionLevel]::Optimal)
            }
        }
    }
    finally {
        $archive.Dispose()
    }
}
finally {
    $fileStream.Dispose()
}

# Authoritative checksum sidecar + on-disk manifest digest (zip keeps identity-only manifest).
$sha = (Get-FileHash -LiteralPath $OutputZip -Algorithm SHA256).Hash.ToUpperInvariant()
$manifestWithSha = [ordered]@{}
foreach ($k in $manifestObj.Keys) { $manifestWithSha[$k] = $manifestObj[$k] }
$manifestWithSha['package_sha256'] = $sha
($manifestWithSha | ConvertTo-Json -Depth 5) + "`n" | Set-Content -LiteralPath $manifestPath -Encoding utf8
$shaFile = "$OutputZip.sha256"
("{0}  {1}" -f $sha, [IO.Path]::GetFileName($OutputZip)) | Set-Content -LiteralPath $shaFile -Encoding ascii

# Validate: zero backslash entry names + report counts
$backslashCount = 0
$entryCount = 0
$check = [System.IO.Compression.ZipFile]::OpenRead($OutputZip)
try {
    foreach ($e in $check.Entries) {
        $entryCount++
        if ($e.FullName.Contains('\')) {
            $backslashCount++
            Write-Host "BACKSLASH ENTRY: $($e.FullName)"
        }
    }
}
finally {
    $check.Dispose()
}

if ($backslashCount -ne 0) {
    throw "pack-release validation failed: backslash_count=$backslashCount (expected 0)."
}

Write-Host "OK: wrote $OutputZip entries=$entryCount backslash_count=0 sha256=$sha"
