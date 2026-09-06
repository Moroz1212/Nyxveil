#Requires -Version 5.1
<#
.SYNOPSIS
  Build full Nyxveil Windows Client E2E candidate after P0 audit fixes.
#>
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root
$Ver = (Get-Content (Join-Path $Root "VERSION") -Raw).Trim()
$Dist = Join-Path $Root "dist"
$Payload = Join-Path $Dist "payload"
$Stage = Join-Path $Dist "stage-tree"
$ZipOut = Join-Path $Dist "Nyxveil-Windows-Client-v$Ver-E2E-CANDIDATE.zip"
$SetupOut = Join-Path $Dist "Nyxveil-Setup-v$Ver-E2E-CANDIDATE.exe"

Write-Host "==> Assert Frozen Core + Wintun"
& (Join-Path $Root "scripts\assert-frozen-core.ps1")
& (Join-Path $Root "scripts\assert-wintun.ps1")

Write-Host "==> Clean"
Remove-Item -Recurse -Force $Payload, $Stage, $ZipOut, $SetupOut -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $Payload | Out-Null

Write-Host "==> Go test + vet"
Push-Location (Join-Path $Root "engine")
go test ./...
if ($LASTEXITCODE -ne 0) { throw "go test failed" }
go vet ./...
if ($LASTEXITCODE -ne 0) { throw "go vet failed" }
New-Item -ItemType Directory -Force -Path "dist" | Out-Null
go build -trimpath -ldflags "-s -w" -o "dist\Nyxveil.Service.exe" ./cmd/nyxveil-service
if ($LASTEXITCODE -ne 0) { throw "go build failed" }
Pop-Location

Write-Host "==> .NET restore/build/test/publish"
$sln = Join-Path $Root "gui\Nyxveil.Client.sln"
dotnet restore $sln
dotnet build $sln -c Release --no-restore
if ($LASTEXITCODE -ne 0) { throw "dotnet build failed" }
dotnet test $sln -c Release --no-build --verbosity minimal
if ($LASTEXITCODE -ne 0) { throw "dotnet test failed" }
$guiOut = Join-Path $Payload "gui"
dotnet publish (Join-Path $Root "gui\src\Nyxveil.App\Nyxveil.App.csproj") -c Release -r win-x64 --self-contained true `
  -p:PublishSingleFile=true -p:IncludeNativeLibrariesForSelfExtract=true `
  -p:DebugType=None -p:DebugSymbols=false -o $guiOut --nologo
if ($LASTEXITCODE -ne 0) { throw "dotnet publish failed" }

Copy-Item (Join-Path $Root "engine\dist\Nyxveil.Service.exe") (Join-Path $Payload "Nyxveil.Service.exe") -Force
Copy-Item (Join-Path $Root "third_party\wintun\amd64\wintun.dll") (Join-Path $Payload "wintun.dll") -Force

Write-Host "==> Clean-dir smoke + provision SID"
$smoke = Join-Path $Dist "smoke-clean"
Remove-Item -Recurse -Force $smoke -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $smoke | Out-Null
Copy-Item (Join-Path $Payload "Nyxveil.Service.exe"), (Join-Path $Payload "wintun.dll") $smoke
& (Join-Path $smoke "Nyxveil.Service.exe") -provision-sid
if ($LASTEXITCODE -ne 0) { throw "provision-sid failed" }
$p = Start-Process -FilePath (Join-Path $smoke "Nyxveil.Service.exe") -ArgumentList "-console","-pipe","\\.\pipe\NyxveilSmokeTest" -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 2
if ($p.HasExited) { throw "service exited early code=$($p.ExitCode)" }
Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue
Write-Host "smoke OK"

Write-Host "==> Inno Setup"
$iscc = @(
  (Join-Path $env:LOCALAPPDATA "Programs\Inno Setup 6\ISCC.exe"),
  (Join-Path ${env:ProgramFiles(x86)} "Inno Setup 6\ISCC.exe")
) | Where-Object { Test-Path $_ } | Select-Object -First 1
if (-not $iscc) { throw "ISCC.exe not found" }
& $iscc (Join-Path $Root "installer\nyxveil.iss")
if ($LASTEXITCODE -ne 0) { throw "ISCC failed" }
if (-not (Test-Path $SetupOut)) { throw "Setup EXE missing" }

Write-Host "==> Stage tree (includes Frozen third_party/nvp)"
New-Item -ItemType Directory -Force -Path $Stage | Out-Null
foreach ($d in @("engine","gui","docs","scripts","installer","third_party")) {
  $src = Join-Path $Root $d
  if (Test-Path $src) {
    robocopy $src (Join-Path $Stage $d) /E /NFL /NDL /NJH /NJS /nc /ns /np `
      /XD bin obj dist .vs TestResults `
      /XF *.pdb *.user *.suo try.zip | Out-Null
  }
}
Copy-Item (Join-Path $Root "VERSION"), (Join-Path $Root "README.md"), (Join-Path $Root "THIRD_PARTY_CORE.md") $Stage -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path (Join-Path $Stage "publish") | Out-Null
Copy-Item $Payload (Join-Path $Stage "publish\payload") -Recurse -Force
Copy-Item $SetupOut (Join-Path $Stage "Nyxveil-Setup-v$Ver-E2E-CANDIDATE.exe") -Force

@"
Nyxveil Windows Client v$Ver — E2E CANDIDATE (post-audit P0 fixes)

RESULT: READY FOR LIVE E2E TEST (local gates)
Authenticode (Setup/client): NOT SIGNED
REAL WINDOWS->UBUNTU INTERNET: NOT VERIFIED

Service: NyxveilClientService / Nyxveil.Service.exe
Pipe ACL: SYSTEM + Administrators + provisioned interactive user SID
CP TLS: SystemTrust (publicly trusted CP required for live E2E)
Frozen Core SHA: 7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b
"@ | Set-Content (Join-Path $Stage "NOTES.txt") -Encoding UTF8

Write-Host "==> SHA256SUMS then ZIP (SUMS must be inside ZIP)"
$setupHash = (Get-FileHash $SetupOut -Algorithm SHA256).Hash.ToLowerInvariant()
$svcHash = (Get-FileHash (Join-Path $Payload "Nyxveil.Service.exe") -Algorithm SHA256).Hash.ToLowerInvariant()
$dllHash = (Get-FileHash (Join-Path $Payload "wintun.dll") -Algorithm SHA256).Hash.ToLowerInvariant()
$ExpectedFrozen = "7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b"

# Inner SUMS (inside ZIP): artifact hashes excluding the ZIP itself (avoids chicken-egg).
$innerSums = @"
$setupHash  Nyxveil-Setup-v$Ver-E2E-CANDIDATE.exe
$svcHash  publish/payload/Nyxveil.Service.exe
$dllHash  publish/payload/wintun.dll
$ExpectedFrozen  FROZEN_CORE_SHA
"@
$sumsStage = Join-Path $Stage "SHA256SUMS"
$innerSums | Set-Content $sumsStage -Encoding ASCII

Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
if (Test-Path $ZipOut) { Remove-Item $ZipOut -Force }

function New-ZipWithForwardSlashes([string]$zipPath, [string]$dirPath, [string]$prefix) {
  $fs = [System.IO.File]::Open($zipPath, [System.IO.FileMode]::Create)
  $archive = New-Object System.IO.Compression.ZipArchive($fs, [System.IO.Compression.ZipArchiveMode]::Create)
  try {
    Get-ChildItem $dirPath -Recurse -File | ForEach-Object {
      $rel = $_.FullName.Substring($dirPath.Length).TrimStart('\')
      $entryName = ($prefix + ($rel -replace '\\','/'))
      [void][System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile($archive, $_.FullName, $entryName, [System.IO.Compression.CompressionLevel]::Optimal)
    }
  } finally {
    $archive.Dispose()
    $fs.Dispose()
  }
}

$prefix = "Nyxveil-Windows-Client-v$Ver/"
New-ZipWithForwardSlashes $ZipOut $Stage $prefix

$zipHash = (Get-FileHash $ZipOut -Algorithm SHA256).Hash.ToLowerInvariant()
$outerSums = @"
$zipHash  Nyxveil-Windows-Client-v$Ver-E2E-CANDIDATE.zip
$setupHash  Nyxveil-Setup-v$Ver-E2E-CANDIDATE.exe
"@
$sumsPath = Join-Path $Dist "SHA256SUMS"
$outerSums | Set-Content $sumsPath -Encoding ASCII

# Independent assert SHA256SUMS + Frozen Core inside ZIP
$chk = [System.IO.Compression.ZipFile]::OpenRead($ZipOut)
try {
  $e = $chk.GetEntry($prefix + "SHA256SUMS")
  if (-not $e) { throw "SHA256SUMS missing inside ZIP" }
  $nvp = $chk.Entries | Where-Object { $_.FullName -like "*third_party/nvp/go.mod" } | Select-Object -First 1
  if (-not $nvp) { throw "Frozen Core third_party/nvp missing inside ZIP" }
  $bs = ($chk.Entries | Where-Object { $_.FullName -match '\\' }).Count
  if ($bs -gt 0) { throw "ZIP contains backslash entries: $bs" }
} finally { $chk.Dispose() }

Write-Host "DONE"
Get-Content $sumsPath
Write-Host "ZIP=$ZipOut"
Write-Host "SETUP=$SetupOut"
