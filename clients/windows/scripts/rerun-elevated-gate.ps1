# Kill hung elevated gate processes then start a fresh elevated gate.
$ErrorActionPreference = "Continue"
$PublicGate = Join-Path $env:PUBLIC "NyxveilGate"
New-Item -ItemType Directory -Force -Path $PublicGate | Out-Null
$killWrap = Join-Path $PublicGate "gate-kill.ps1"
@"
`$ErrorActionPreference='Continue'
Get-Process powershell,dotnet,testhost,MSBuild,VBCSCompiler -ErrorAction SilentlyContinue | ForEach-Object {
  try {
    `$cl = (Get-CimInstance Win32_Process -Filter ("ProcessId=`$(`$_.Id)") -ErrorAction SilentlyContinue).CommandLine
    if (`$cl -match 'final-windows-gate|gate-elev-wrap|run-elevated-gate|Nyxveil\.Client|nv-native|testhost') {
      Stop-Process -Id `$_.Id -Force -ErrorAction SilentlyContinue
    }
  } catch {}
}
try { & dotnet build-server shutdown 2>`$null | Out-Null } catch {}
# Also stop leftover service so next install is clean
sc.exe stop NyxveilClientService 2>`$null | Out-Null
Start-Sleep -Seconds 2
Set-Content -Path '$PublicGate\gate-kill.done' -Value 'ok' -Encoding ASCII
"@ | Set-Content $killWrap -Encoding UTF8
Remove-Item "$PublicGate\gate-kill.done" -Force -ErrorAction SilentlyContinue
Write-Host "Elevating kill-orphans..."
Start-Process powershell -Verb RunAs -Wait -ArgumentList @("-NoProfile","-ExecutionPolicy","Bypass","-File",$killWrap)
Start-Sleep -Seconds 2
Write-Host "Starting fresh elevated gate..."
& (Join-Path $PSScriptRoot "run-elevated-gate.ps1")
