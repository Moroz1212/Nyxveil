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
        if ($p -in @('bin', 'obj', 'secrets', '.git', '.vs', 'artifacts', 'publish', 'TestResults', 'logs')) { return $true }
    }
    if ($rel -match '(?i)(^|[/\\])(secrets|artifacts|publish|bin|obj)([/\\]|$)') { return $true }
    if ($rel -match '(?i)\.(pfx|dpapi|user)$') { return $true }
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

Write-Host "OK: wrote $OutputZip entries=$entryCount backslash_count=0"
