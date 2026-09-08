#Requires -Version 5.1
<#
.SYNOPSIS
  Build gomobile AAR for the Android NVP bridge (arm64-v8a).

.DESCRIPTION
  Pins NDK r26b (26.1.10909125). Prefers a short toolchain path to avoid Windows
  MAX_PATH failures during NDK/clang use. Source stays under clients/android/bridge/go.
#>
param(
    [switch]$SkipTests
)

$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent $PSScriptRoot
$GoModDir = Join-Path $Root "bridge\go"
$OutDir = Join-Path $Root "bridge\native"
$AarPath = Join-Path $OutDir "nyxveilbridge.aar"
$PinnedNdkRevision = "26.1.10909125"
$PinnedNdkLabel = "r26b ($PinnedNdkRevision)"

function Test-NdkUsable([string]$Path) {
    if (-not $Path) { return $false }
    $clang = Join-Path $Path "toolchains\llvm\prebuilt\windows-x86_64\bin\clang.exe"
    if (-not (Test-Path $clang)) { return $false }
    $len = (Get-Item $clang).Length
    return $len -gt 1MB
}

function Resolve-PinnedNdk {
    $candidates = @(
        $env:ANDROID_NDK_HOME,
        $env:ANDROID_NDK_ROOT,
        "C:\NvT\ndk",
        (Join-Path $env:LOCALAPPDATA "Nyxveil\Toolchains\ndk-$PinnedNdkRevision"),
        "C:\NyxveilToolchain\ndk-$PinnedNdkRevision",
        (Join-Path $env:ANDROID_HOME "ndk\$PinnedNdkRevision"),
        (Join-Path $env:ANDROID_SDK_ROOT "ndk\$PinnedNdkRevision"),
        "C:\Android\Sdk\ndk\$PinnedNdkRevision",
        (Join-Path $env:LOCALAPPDATA "Android\Sdk\ndk\$PinnedNdkRevision")
    ) | Where-Object { $_ } | Select-Object -Unique

    foreach ($c in $candidates) {
        if (Test-NdkUsable $c) {
            $props = Join-Path $c "source.properties"
            if (Test-Path $props) {
                $revLine = Select-String -Path $props -Pattern "Pkg.Revision\s*=\s*(.+)" | Select-Object -First 1
                if ($revLine) {
                    $rev = $revLine.Matches[0].Groups[1].Value.Trim()
                    if ($rev -ne $PinnedNdkRevision) {
                        Write-Warning "NDK at $c has revision $rev (want $PinnedNdkRevision); skipping"
                        continue
                    }
                }
            }
            return (Resolve-Path $c).Path
        }
    }
    throw @"
Pinned NDK $PinnedNdkLabel not found or unusable (clang missing/truncated).
Install via sdkmanager 'ndk;$PinnedNdkRevision' or extract to a SHORT path, e.g.:
  C:\NvT\ndk
  %LOCALAPPDATA%\Nyxveil\Toolchains\ndk-$PinnedNdkRevision
Then set ANDROID_NDK_HOME to that path.
"@
}

function Resolve-GoBin {
    $go = Get-Command go -ErrorAction SilentlyContinue
    if ($go) { return $go.Source }
    foreach ($c in @(
        (Join-Path $env:USERPROFILE "tools\go\bin\go.exe"),
        (Join-Path $env:USERPROFILE "go\bin\go.exe"),
        "C:\Program Files\Go\bin\go.exe"
    )) {
        if (Test-Path $c) { return $c }
    }
    throw "go not found on PATH"
}

function Ensure-Gomobile([string]$GoExe) {
    $gopath = & $GoExe env GOPATH
    if (-not $gopath) { $gopath = Join-Path $env:USERPROFILE "go" }
    $gomobile = Join-Path $gopath "bin\gomobile.exe"
    if (-not (Test-Path $gomobile)) {
        Write-Host "==> Installing gomobile"
        & $GoExe install golang.org/x/mobile/cmd/gomobile@latest
        if ($LASTEXITCODE -ne 0) { throw "go install gomobile failed" }
    }
    if (-not (Test-Path $gomobile)) { throw "gomobile.exe missing after install: $gomobile" }
    return $gomobile
}

$ndk = Resolve-PinnedNdk
$env:ANDROID_NDK_HOME = $ndk
$env:ANDROID_NDK_ROOT = $ndk
Write-Host "NDK version = $PinnedNdkLabel"
Write-Host "NDK path = $ndk"

$goExe = Resolve-GoBin
$goVer = & $goExe version
Write-Host "Go = $goVer"

$gomobile = Ensure-Gomobile $goExe
Write-Host "gomobile = $gomobile"

Push-Location $GoModDir
try {
    if (-not $SkipTests) {
        Write-Host "==> go test ./..."
        & $goExe test ./...
        if ($LASTEXITCODE -ne 0) { throw "go test failed" }
    }

    Write-Host "==> gomobile init (if needed)"
    & $gomobile init
    if ($LASTEXITCODE -ne 0) { throw "gomobile init failed" }

    New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
    $tmpAar = Join-Path $OutDir "nyxveilbridge.build.aar"
    if (Test-Path $tmpAar) { Remove-Item -Force $tmpAar }
    if (Test-Path $AarPath) { Remove-Item -Force $AarPath }

    Write-Host "==> gomobile bind (android/arm64, api 26)"
    & $gomobile bind -target=android/arm64 -androidapi 26 -o $tmpAar .
    if ($LASTEXITCODE -ne 0) { throw "gomobile bind failed" }
    if (-not (Test-Path $tmpAar)) { throw "AAR not produced" }

    Move-Item -Force $tmpAar $AarPath
}
finally {
    Pop-Location
}

# Validate AAR contents
$sevenZip = @(
    "${env:ProgramFiles}\7-Zip\7z.exe",
    "${env:ProgramFiles(x86)}\7-Zip\7z.exe"
) | Where-Object { Test-Path $_ } | Select-Object -First 1

if (-not $sevenZip) {
    # Fallback: System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [System.IO.Compression.ZipFile]::OpenRead($AarPath)
    try {
        $names = $zip.Entries | ForEach-Object { $_.FullName }
    } finally {
        $zip.Dispose()
    }
} else {
    $list = & $sevenZip l -ba $AarPath
    $names = $list
}

$need = @(
    "classes.jar",
    "jni/arm64-v8a/libgojni.so"
)
foreach ($n in $need) {
    $norm = $n -replace '/', '[\\/]'
    $hit = $names | Where-Object { $_ -match $norm }
    if (-not $hit) { throw "AAR missing required entry: $n" }
}
$bindingHit = $names | Where-Object { $_ -match "nyxveilbridge" }
if (-not $bindingHit) {
    # classes.jar may hide names; extract check
    $tmp = Join-Path $env:TEMP "nyxveil-aar-check"
    New-Item -ItemType Directory -Force -Path $tmp | Out-Null
    if ($sevenZip) {
        & $sevenZip e -y -o"$tmp" $AarPath "classes.jar" | Out-Null
        $jarList = & $sevenZip l -ba (Join-Path $tmp "classes.jar")
        if (-not ($jarList | Where-Object { $_ -match "nyxveilbridge" })) {
            throw "AAR classes.jar missing nyxveilbridge bindings"
        }
    }
}

$size = (Get-Item $AarPath).Length
Write-Host "AAR GENERATED = PASS"
Write-Host "AAR path = $AarPath"
Write-Host ("AAR size = {0:N0} bytes" -f $size)
Write-Host "AAR CONTENT = PASS (classes.jar + jni/arm64-v8a/libgojni.so)"
