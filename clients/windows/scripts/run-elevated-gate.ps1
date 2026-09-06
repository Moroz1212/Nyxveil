# Launches final-windows-gate.ps1 elevated; logs to %PUBLIC%\NyxveilGate\gate-run.log
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
$PublicGate = Join-Path $env:PUBLIC "NyxveilGate"
New-Item -ItemType Directory -Force -Path $PublicGate | Out-Null
$log = Join-Path $PublicGate "gate-run.log"
$errLog = Join-Path $PublicGate "gate-run.err"
$exitFile = Join-Path $PublicGate "gate-run.exit"
$live = Join-Path $PublicGate "gate-live.log"
$gate = Join-Path $Root "scripts\final-windows-gate.ps1"
Remove-Item -LiteralPath $log, $errLog, $exitFile, $live -Force -ErrorAction SilentlyContinue
Get-ChildItem $PublicGate -Filter "nv-native-*" -ErrorAction SilentlyContinue | Remove-Item -Force -ErrorAction SilentlyContinue

$wrap = Join-Path $PublicGate "gate-elev-wrap.ps1"
# Single-line Start-Process — line-continuation backticks break under some hosts.
$wrapBody = @"
`$ErrorActionPreference = 'Continue'
`$log = '$log'
`$errLog = '$errLog'
`$exitFile = '$exitFile'
`$code = 1
try { & dotnet build-server shutdown 2>`$null | Out-Null } catch {}
try {
  `$p = Start-Process -FilePath 'powershell.exe' -ArgumentList '-NoProfile','-ExecutionPolicy','Bypass','-File','$gate' -Wait -PassThru -WindowStyle Hidden -RedirectStandardOutput `$log -RedirectStandardError `$errLog
  if (`$null -ne `$p) { `$code = [int]`$p.ExitCode } else { `$code = 1 }
} catch {
  `$_ | Out-File -FilePath `$errLog -Append -Encoding utf8
  `$code = 1
}
Set-Content -Path `$exitFile -Value `$code -Encoding ASCII -Force
exit `$code
"@
Set-Content -Path $wrap -Value $wrapBody -Encoding UTF8

Write-Host "Requesting elevation for final-windows-gate..."
Write-Host "Approve the UAC prompt if shown. Progress: $live"
$p = Start-Process -FilePath "powershell.exe" -Verb RunAs -PassThru -Wait -ArgumentList @(
  "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $wrap
)
Write-Host "Elevated process exit: $($p.ExitCode)"
if (Test-Path $exitFile) {
  Write-Host "Gate exit file: $(Get-Content $exitFile -Raw)"
}
if (Test-Path $live) {
  Write-Host "--- gate-live.log ---"
  Get-Content $live
}
if (Test-Path $log) {
  Write-Host "--- gate-run.log (tail) ---"
  Get-Content $log -Tail 40
}
if (Test-Path $errLog) {
  Write-Host "--- gate-run.err (tail) ---"
  Get-Content $errLog -Tail 40
}
