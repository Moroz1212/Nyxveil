#Requires -Version 5.1
# Verify official Wintun 0.14.1 hash + amd64 Authenticode.
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
$ExpectedZip = "07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"
$Zip = Join-Path $Root "third_party\wintun\wintun-0.14.1.zip"
$Dll = Join-Path $Root "third_party\wintun\amd64\wintun.dll"

if (-not (Test-Path $Zip)) { throw "missing $Zip" }
$zh = (Get-FileHash $Zip -Algorithm SHA256).Hash.ToLowerInvariant()
if ($zh -ne $ExpectedZip) { throw "Wintun zip SHA mismatch: $zh" }

if (-not (Test-Path $Dll)) { throw "missing $Dll" }
$sig = Get-AuthenticodeSignature -FilePath $Dll
if ($sig.Status -ne "Valid") {
  throw "wintun.dll Authenticode Status=$($sig.Status) (want Valid)"
}
$pub = $sig.SignerCertificate.Subject
if ($pub -notmatch "WireGuard|Joshua\s+Adrian\s+Donenfeld|ZX2C4") {
  # Official builds are signed by WireGuard LLC / related publisher strings.
  Write-Host "Publisher subject: $pub"
  if ($pub -notmatch "WireGuard") {
    throw "unexpected wintun.dll publisher: $pub"
  }
}
Write-Host "assert-wintun: OK zip=$ExpectedZip Authenticode=$($sig.Status) publisher=$pub"
