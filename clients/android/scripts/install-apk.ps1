#Requires -Version 5.1
param(
    [string]$Apk = ""
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot

if (-not $Apk) {
    $version = (Get-Content (Join-Path $Root "VERSION") -Raw).Trim()
    $Apk = Join-Path $Root "dist\Nyxveil-Android-v$version-debug.apk"
}
if (-not (Test-Path $Apk)) {
    throw "APK not found: $Apk"
}

function Resolve-Adb {
    $sdkCandidates = @(
        $env:ANDROID_HOME,
        $env:ANDROID_SDK_ROOT,
        "C:\Android\Sdk",
        (Join-Path $env:LOCALAPPDATA "Android\Sdk")
    ) | Where-Object { $_ }
    foreach ($sdk in $sdkCandidates) {
        $adb = Join-Path $sdk "platform-tools\adb.exe"
        if (Test-Path $adb) { return $adb }
    }
    $cmd = Get-Command adb -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    throw "adb not found. Install platform-tools."
}

$adb = Resolve-Adb
Write-Host "Installing $Apk"
& $adb install -r $Apk
if ($LASTEXITCODE -ne 0) {
    throw "adb install failed"
}
